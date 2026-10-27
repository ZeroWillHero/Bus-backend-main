package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestVerifyTOTPCode(t *testing.T) {
	secret, uri, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if secret == "" || uri == "" {
		t.Fatalf("expected non-empty secret and provisioning URI")
	}

	validCode, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode: %v", err)
	}
	if !VerifyTOTPCode(secret, validCode) {
		t.Errorf("expected valid code %q to verify against secret", validCode)
	}

	if VerifyTOTPCode(secret, "000000") && validCode != "000000" {
		// Extremely unlikely collision; guards against a no-op verifier.
		t.Errorf("expected arbitrary code not to verify")
	}

	if VerifyTOTPCode("not-a-real-secret", validCode) {
		t.Errorf("expected code not to verify against a different secret")
	}
}

func TestMatchTOTPStep(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := time.Unix(1_700_000_015, 0) // mid-step
	nowStep := now.Unix() / TOTPPeriod
	for _, skew := range []int64{-1, 0, 1} {
		code, err := totp.GenerateCode(secret, time.Unix((nowStep+skew)*TOTPPeriod, 0))
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		step, ok := MatchTOTPStep(secret, code, now)
		if !ok || step != nowStep+skew {
			t.Errorf("skew %d: step=%d ok=%v, want step=%d ok=true", skew, step, ok, nowStep+skew)
		}
	}
	old, _ := totp.GenerateCode(secret, time.Unix((nowStep-2)*TOTPPeriod, 0))
	if _, ok := MatchTOTPStep(secret, old, now); ok {
		t.Errorf("code two steps old was accepted")
	}
}
