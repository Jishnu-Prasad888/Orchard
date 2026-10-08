package library

import "context"

type actorKey struct{}

// WithActor returns a context carrying the acting user id. The HTTP layer sets
// it before invoking library operations so audit records capture who acted.
func WithActor(ctx context.Context, actorUserID string) context.Context {
	return context.WithValue(ctx, actorKey{}, actorUserID)
}

// actorIDFrom extracts the acting user id, if present.
func actorIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(actorKey{}).(string); ok {
		return v
	}
	return ""
}
