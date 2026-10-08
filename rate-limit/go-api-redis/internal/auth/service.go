package auth

import (
	"context"
	"errors"
)

var ErrInvalidCredentials = errors.New("invalid email or password")

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

type Service struct {
	store  *Store
	tokens *TokenManager
}

func NewService(store *Store, tokens *TokenManager) *Service {
	return &Service{store: store, tokens: tokens}
}

func (s *Service) Register(ctx context.Context, email, password string) (User, TokenPair, error) {
	user, err := s.store.CreateUser(ctx, email, password)
	if err != nil {
		return User{}, TokenPair{}, err
	}

	pair, err := s.issuePair(user)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return user, pair, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (User, TokenPair, error) {
	user, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, TokenPair{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, TokenPair{}, err
	}
	if !verifyPassword(user.passwordHash, password) {
		return User{}, TokenPair{}, ErrInvalidCredentials
	}

	pair, err := s.issuePair(user)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return user, pair, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	claims, err := s.tokens.Parse(refreshToken, TokenTypeRefresh)
	if err != nil {
		return TokenPair{}, err
	}

	user, err := s.store.UserByID(ctx, claims.Subject)
	if err != nil {
		return TokenPair{}, err
	}

	return s.issuePair(user)
}

func (s *Service) issuePair(user User) (TokenPair, error) {
	accessToken, accessClaims, err := s.tokens.Issue(user.ID, user.Role, TokenTypeAccess)
	if err != nil {
		return TokenPair{}, err
	}

	refreshToken, _, err := s.tokens.Issue(user.ID, user.Role, TokenTypeRefresh)
	if err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(accessClaims.ExpiresAt.Sub(accessClaims.IssuedAt.Time).Seconds()),
	}, nil
}
