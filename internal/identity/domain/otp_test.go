package domain

import (
	"regexp"
	"testing"
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

func TestGenerateOTP(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		code, err := GenerateOTP()
		if err != nil {
			t.Fatalf("GenerateOTP: %v", err)
		}
		if !sixDigits.MatchString(code) {
			t.Fatalf("GenerateOTP() = %q, want a 6-digit numeric string", code)
		}
		seen[code] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected GenerateOTP to produce varying codes across 50 calls, got %d distinct values", len(seen))
	}
}

func TestRequiresMFA(t *testing.T) {
	cases := []struct {
		role Role
		want bool
	}{
		{RolePassenger, false},
		{RoleOperatorStaff, false},
		{RoleOperatorAdmin, true},
		{RoleFinanceAdmin, true},
		{RoleSupport, true},
		{RolePlatformAdmin, true},
	}
	for _, c := range cases {
		if got := RequiresMFA(c.role); got != c.want {
			t.Errorf("RequiresMFA(%s) = %v, want %v", c.role, got, c.want)
		}
	}
}

func TestTrustedDeviceEligible(t *testing.T) {
	if TrustedDeviceEligible(RolePlatformAdmin) {
		t.Errorf("platform_admin must never be trusted-device eligible (FR-AUTH-4)")
	}
	if !TrustedDeviceEligible(RoleOperatorAdmin) {
		t.Errorf("operator_admin should be trusted-device eligible")
	}
	if TrustedDeviceEligible(RolePassenger) {
		t.Errorf("passenger doesn't use MFA at all, so it can't be trusted-device eligible")
	}
}
