package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ctxKey string

const claimsCtxKey ctxKey = "auth.claims"

// Verifier validates a bearer token and returns its claims. A concrete
// implementation wraps golang-jwt/jwt/v5 with the platform's signing key.
type Verifier interface {
	Verify(tokenString string) (*Claims, error)
}

// Middleware extracts and verifies the Authorization: Bearer header
// (ENGINEERING_STANDARDS.md §8), then stores the claims on the request
// context so downstream handlers can read Role/OperatorID and so the
// repository layer can set the RLS session variables from them
// (DATABASE_DESIGN.md §5, AUTHENTICATION_DESIGN.md §4.2).
func Middleware(verifier Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := bearerToken(r)
			if err != nil {
				writeUnauthorized(w)
				return
			}
			claims, err := verifier.Verify(token)
			if err != nil {
				writeUnauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), claimsCtxKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the verified claims attached by Middleware.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsCtxKey).(*Claims)
	return claims, ok
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errors.New("auth: missing bearer token")
	}
	return strings.TrimPrefix(header, prefix), nil
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"success":false,"error":{"code":"AUTH_TOKEN_EXPIRED","message":"Please sign in again."}}`))
}

var _ = jwt.SigningMethodHS256 // placeholder until the signing method is finalized with golang-jwt v5 (RFC 8725)
