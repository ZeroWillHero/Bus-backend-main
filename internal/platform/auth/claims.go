// Package auth defines the JWT claims schema and the middleware that turns
// a verified token into the Postgres session variables Row-Level Security
// policies check, per AUTHENTICATION_DESIGN.md §4.
package auth

import "github.com/golang-jwt/jwt/v5"

// Role is one of the six roles defined in AUTHENTICATION_DESIGN.md §1.
type Role string

const (
	RolePassenger     Role = "passenger"
	RoleOperatorStaff Role = "operator_staff"
	RoleOperatorAdmin Role = "operator_admin"
	RoleFinanceAdmin  Role = "finance_admin"
	RoleSupport       Role = "support"
	RolePlatformAdmin Role = "platform_admin"
)

// Claims mirrors the JWT schema in AUTHENTICATION_DESIGN.md §4.1.
type Claims struct {
	jwt.RegisteredClaims
	Role         Role   `json:"role"`
	OperatorID   string `json:"operator_id,omitempty"`
	MFASatisfied bool   `json:"mfa_satisfied"`
}
