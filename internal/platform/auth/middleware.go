package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sl-bus/backend/internal/platform/httpx"
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
				writeUnauthorized(w, err)
				return
			}
			claims, err := verifier.Verify(token)
			if err != nil {
				writeUnauthorized(w, err)
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

// RequireRole gates a subroute to a set of roles, per the RBAC matrix in
// AUTHENTICATION_DESIGN.md §5. Must run after Middleware, which populates
// the claims this reads.
func RequireRole(roles ...Role) func(http.Handler) http.Handler {
	allowed := make(map[Role]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok {
				writeUnauthorized(w, errors.New("auth: missing claims"))
				return
			}
			if _, ok := allowed[claims.Role]; !ok {
				httpx.WriteError(w, http.StatusForbidden, httpx.APIError{
					Code:    "AUTH_FORBIDDEN",
					Message: "You don't have permission to perform this action.",
				}, nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errors.New("auth: missing bearer token")
	}
	return strings.TrimPrefix(header, prefix), nil
}

// writeUnauthorized reports the specific reason a token was rejected
// (expired vs missing/malformed/invalid-signature) rather than always
// claiming AUTH_TOKEN_EXPIRED, so a client can tell "refresh" apart from
// "sign in again."
func writeUnauthorized(w http.ResponseWriter, err error) {
	code := "AUTH_TOKEN_INVALID"
	message := "Please sign in again."
	if errors.Is(err, ErrTokenExpired) {
		code = "AUTH_TOKEN_EXPIRED"
		message = "Your session has expired. Please sign in again."
	}
	httpx.WriteError(w, http.StatusUnauthorized, httpx.APIError{
		Code:    code,
		Message: message,
	}, nil)
}
