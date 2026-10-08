package auth

import "context"

// WithUser returns a context carrying the authenticated principal. It is set
// by the HTTP authentication middleware immediately before protected handlers
// run.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey{}, u)
}

// UserFrom extracts the authenticated principal from a context, if present.
func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey{}).(*User)
	if !ok || u == nil {
		return nil, false
	}
	return u, true
}
