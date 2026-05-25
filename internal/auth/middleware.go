package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type contextKey struct{}

// SingleTenantMiddleware authenticates by comparing the bearer token to a
// pre-shared secret. All authenticated requests receive full admin scopes.
func SingleTenantMiddleware(secret string) func(http.Handler) http.Handler {
	secretBytes := []byte(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				writeUnauthorized(w, "missing bearer token")
				return
			}
			if subtle.ConstantTimeCompare([]byte(token), secretBytes) != 1 {
				writeUnauthorized(w, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), contextKey{}, Context{
				Token:       token,
				TenantID:    "default",
				PrincipalID: "default",
				Scopes:      AllScopes(),
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func FromContext(ctx context.Context) (Context, bool) {
	c, ok := ctx.Value(contextKey{}).(Context)
	return c, ok
}

func RequireScope(scope Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCtx, ok := FromContext(r.Context())
			if !ok || !authCtx.HasScope(scope) {
				writeForbidden(w, "insufficient scope")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"` + msg + `","code":"UNAUTHORIZED"}`)) //nolint:errcheck
}

func writeForbidden(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"error":"` + msg + `","code":"FORBIDDEN"}`)) //nolint:errcheck
}
