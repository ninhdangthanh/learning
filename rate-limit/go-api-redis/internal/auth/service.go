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

	pair, err := s.issuePair(ctx, user)
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

	pair, err := s.issuePair(ctx, user)
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

	err = s.store.ConsumeSession(ctx, claims.Subject, claims.ID, s.tokens.RefreshTTL())
	if errors.Is(err, ErrTokenReused) {
		if revokeErr := s.store.RevokeAllSessions(ctx, claims.Subject); revokeErr != nil {
			return TokenPair{}, revokeErr
		}
		return TokenPair{}, ErrTokenReused
	}
	if err != nil {
		return TokenPair{}, err
	}

	user, err := s.store.UserByID(ctx, claims.Subject)
	if err != nil {
		return TokenPair{}, err
	}

	return s.issuePair(ctx, user)
}

func (s *Service) Logout(ctx context.Context, identity Identity, refreshToken string) error {
	if err := s.store.DenyAccessToken(ctx, identity.AccessID, s.tokens.AccessTTL()); err != nil {
		return err
	}

	if refreshToken == "" {
		return s.store.RevokeAllSessions(ctx, identity.UserID)
	}

	claims, err := s.tokens.Parse(refreshToken, TokenTypeRefresh)
	if err != nil || claims.Subject != identity.UserID {
		return s.store.RevokeAllSessions(ctx, identity.UserID)
	}

	err = s.store.ConsumeSession(ctx, claims.Subject, claims.ID, s.tokens.RefreshTTL())
	if errors.Is(err, ErrNoSession) || errors.Is(err, ErrTokenReused) {
		return nil
	}
	return err
}

func (s *Service) issuePair(ctx context.Context, user User) (TokenPair, error) {
	accessToken, accessClaims, err := s.tokens.Issue(user.ID, user.Role, TokenTypeAccess)
	if err != nil {
		return TokenPair{}, err
	}

	refreshToken, refreshClaims, err := s.tokens.Issue(user.ID, user.Role, TokenTypeRefresh)
	if err != nil {
		return TokenPair{}, err
	}

	if err := s.store.SaveSession(ctx, user.ID, refreshClaims.ID, s.tokens.RefreshTTL()); err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(accessClaims.ExpiresAt.Sub(accessClaims.IssuedAt.Time).Seconds()),
	}, nil
}
