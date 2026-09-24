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
