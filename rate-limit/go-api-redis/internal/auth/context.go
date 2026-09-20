package auth

import "context"

type contextKey struct{}

var identityKey contextKey

type Identity struct {
	UserID   string
	Role     string
	AccessID string
}

func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey, identity)
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey).(Identity)
	return identity, ok
}

func UserIDFrom(ctx context.Context) string {
	identity, ok := IdentityFrom(ctx)
	if !ok {
		return ""
	}
	return identity.UserID
}
