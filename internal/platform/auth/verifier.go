package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTokenTTL and RefreshTokenTTL are the lifetimes fixed in
// AUTHENTICATION_DESIGN.md §4.3.
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
)

// JWTIssuer signs and verifies access tokens using a single HMAC secret
// (env JWT_SIGNING_KEY). HS256 is the pragmatic Phase 1 choice — revisit
// RS256/JWKS if a third-party token consumer ever appears.
type JWTIssuer struct {
	secret []byte
}

func NewJWTIssuer(secret string) *JWTIssuer {
	return &JWTIssuer{secret: []byte(secret)}
}

// Issue mints a signed access token for the given identity/role, per the
// claims schema in AUTHENTICATION_DESIGN.md §4.1.
func (j *JWTIssuer) Issue(userID string, role Role, operatorID string, mfaSatisfied bool) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
		},
		Role:         role,
		OperatorID:   operatorID,
		MFASatisfied: mfaSatisfied,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// Verify implements the Verifier interface consumed by Middleware.
func (j *JWTIssuer) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method %v", t.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	if !token.Valid {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

var (
	// ErrTokenExpired distinguishes an expired token from a malformed one
	// so Middleware doesn't lie about which happened.
	ErrTokenExpired = errors.New("auth: token expired")
	ErrTokenInvalid = errors.New("auth: token invalid")
)
