package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte("test-only-jwt-secret-with-32-bytes-or-more")

func newTestPair(t *testing.T) (*Issuer, *Verifier) {
	t.Helper()
	issuer, err := NewIssuer(testSecret, DefaultIssuer)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	verifier, err := NewVerifier(testSecret, DefaultIssuer)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return issuer, verifier
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	issuer, verifier := newTestPair(t)

	token, err := issuer.Issue("user-123", DefaultTokenTTL)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	identity, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if identity.UserID != "user-123" {
		t.Fatalf("user id = %q, want user-123", identity.UserID)
	}
	if remaining := time.Until(identity.ExpiresAt); remaining <= DefaultTokenTTL-time.Minute || remaining > DefaultTokenTTL {
		t.Fatalf("expires in %v, want about %v", remaining, DefaultTokenTTL)
	}
}

func TestVerifyClassifiesErrors(t *testing.T) {
	issuer, verifier := newTestPair(t)
	otherIssuer, err := NewIssuer([]byte("another-secret-that-is-also-32-bytes-long"), DefaultIssuer)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	foreignIssuer, err := NewIssuer(testSecret, "another-system")
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	expiredIssuer, err := NewIssuer(testSecret, DefaultIssuer)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	expiredIssuer.now = func() time.Time { return time.Now().Add(-time.Hour) }

	withinLeewayIssuer, err := NewIssuer(testSecret, DefaultIssuer)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	withinLeewayIssuer.now = func() time.Time { return time.Now().Add(-time.Minute) }

	algNone, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject:   "user-123",
		Issuer:    DefaultIssuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign alg none: %v", err)
	}

	cases := []struct {
		name    string
		token   string
		wantErr error
	}{
		{"Missing", "", ErrTokenMissing},
		{"Garbage", "not-a-jwt", ErrTokenInvalid},
		{"WrongSecret", mustIssue(t, otherIssuer, "user-123", time.Hour), ErrTokenInvalid},
		{"WrongIssuer", mustIssue(t, foreignIssuer, "user-123", time.Hour), ErrTokenInvalid},
		{"AlgNone", algNone, ErrTokenInvalid},
		{"Expired", mustIssue(t, expiredIssuer, "user-123", time.Minute), ErrTokenExpired},
		{"ExpiredWithinLeeway", mustIssue(t, withinLeewayIssuer, "user-123", time.Minute-ClockSkewLeeway/2), nil},
		{"Valid", mustIssue(t, issuer, "user-123", time.Minute), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifier.Verify(tc.token)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("verify error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRejectsShortSecret(t *testing.T) {
	short := []byte("too-short")
	if _, err := NewVerifier(short, DefaultIssuer); !errors.Is(err, ErrSecretTooShort) {
		t.Fatalf("NewVerifier error = %v, want ErrSecretTooShort", err)
	}
	if _, err := NewIssuer(short, DefaultIssuer); !errors.Is(err, ErrSecretTooShort) {
		t.Fatalf("NewIssuer error = %v, want ErrSecretTooShort", err)
	}
}

func TestIssueValidatesInput(t *testing.T) {
	issuer, _ := newTestPair(t)
	if _, err := issuer.Issue("", time.Minute); !errors.Is(err, ErrUserIDMissing) {
		t.Fatalf("empty user error = %v, want ErrUserIDMissing", err)
	}
	if _, err := issuer.Issue("user-123", 0); !errors.Is(err, ErrTTLNotPositive) {
		t.Fatalf("zero ttl error = %v, want ErrTTLNotPositive", err)
	}
}

func mustIssue(t *testing.T, issuer *Issuer, userID string, ttl time.Duration) string {
	t.Helper()
	token, err := issuer.Issue(userID, ttl)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return token
}
