package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	DefaultIssuer   = "ws-chat"
	DefaultTokenTTL = 15 * time.Minute
	MinSecretLength = 32
	ClockSkewLeeway = 30 * time.Second
)

var (
	ErrTokenMissing   = errors.New("token missing")
	ErrTokenExpired   = errors.New("token expired")
	ErrTokenInvalid   = errors.New("token invalid")
	ErrSecretTooShort = fmt.Errorf("jwt secret must be at least %d bytes", MinSecretLength)
	ErrUserIDMissing  = errors.New("user id is required")
	ErrTTLNotPositive = errors.New("token ttl must be positive")
)

var signingMethod = jwt.SigningMethodHS256

type Claims struct {
	jwt.RegisteredClaims
}

type Identity struct {
	UserID    string
	ExpiresAt time.Time
}

type Verifier struct {
	secret []byte
	issuer string
}

func NewVerifier(secret []byte, issuer string) (*Verifier, error) {
	if len(secret) < MinSecretLength {
		return nil, ErrSecretTooShort
	}
	return &Verifier{secret: secret, issuer: issuer}, nil
}

func (v *Verifier) Verify(token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrTokenMissing
	}

	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims, v.signingKey,
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(ClockSkewLeeway),
	)
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return Identity{}, fmt.Errorf("%w: %w", ErrTokenExpired, err)
	case err != nil:
		return Identity{}, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	case claims.Subject == "":
		return Identity{}, fmt.Errorf("%w: subject claim is empty", ErrTokenInvalid)
	}
	return Identity{UserID: claims.Subject, ExpiresAt: claims.ExpiresAt.Time}, nil
}

func (v *Verifier) signingKey(*jwt.Token) (any, error) {
	return v.secret, nil
}

type Issuer struct {
	secret []byte
	issuer string
	now    func() time.Time
}

func NewIssuer(secret []byte, issuer string) (*Issuer, error) {
	if len(secret) < MinSecretLength {
		return nil, ErrSecretTooShort
	}
	return &Issuer{secret: secret, issuer: issuer, now: time.Now}, nil
}

func (i *Issuer) Issue(userID string, ttl time.Duration) (string, error) {
	if userID == "" {
		return "", ErrUserIDMissing
	}
	if ttl <= 0 {
		return "", ErrTTLNotPositive
	}
	issuedAt := i.now()
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   userID,
		Issuer:    i.issuer,
		IssuedAt:  jwt.NewNumericDate(issuedAt),
		ExpiresAt: jwt.NewNumericDate(issuedAt.Add(ttl)),
	}}
	return jwt.NewWithClaims(signingMethod, claims).SignedString(i.secret)
}
