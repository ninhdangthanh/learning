package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("expired token")
)

type Claims struct {
	jwt.RegisteredClaims
	TokenType string `json:"typ"`
	Role      string `json:"role,omitempty"`
}

type TokenManagerConfig struct {
	Secret     []byte
	Issuer     string
	Audience   string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type TokenManager struct {
	config TokenManagerConfig
}

func NewTokenManager(config TokenManagerConfig) *TokenManager {
	return &TokenManager{config: config}
}

func (m *TokenManager) Issue(userID, role, tokenType string) (string, Claims, error) {
	ttl := m.config.AccessTTL
	if tokenType == TokenTypeRefresh {
		ttl = m.config.RefreshTTL
	}

	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   userID,
			Issuer:    m.config.Issuer,
			Audience:  jwt.ClaimStrings{m.config.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		TokenType: tokenType,
		Role:      role,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.config.Secret)
	if err != nil {
		return "", Claims{}, err
	}
	return signed, claims, nil
}

func (m *TokenManager) Parse(raw, expectedType string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.config.Secret, nil
	},
		jwt.WithIssuer(m.config.Issuer),
		jwt.WithAudience(m.config.Audience),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Claims{}, ErrExpiredToken
		}
		return Claims{}, ErrInvalidToken
	}
	if claims.TokenType != expectedType {
		return Claims{}, ErrInvalidToken
	}
	if claims.Subject == "" || claims.ID == "" {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}
