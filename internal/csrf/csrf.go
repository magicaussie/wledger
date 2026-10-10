// Package csrf carries a request-scoped CSRF token from middleware to templates.
//
// It is intentionally dependency-free so both the middleware package and the
// web layout package can use it without creating an import cycle.
package csrf

import "context"

type contextKey struct{}

// WithToken returns a copy of ctx carrying the CSRF token.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, contextKey{}, token)
}

// TokenFromContext returns the CSRF token stored in ctx, or "" if none.
func TokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKey{}).(string); ok {
		return v
	}
	return ""
}
