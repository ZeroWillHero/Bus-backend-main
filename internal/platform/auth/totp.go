package auth

import "github.com/pquerna/otp/totp"

// GenerateTOTPSecret creates a new TOTP secret for MFA enrollment
// (AUTHENTICATION_DESIGN.md §3.3) and returns both the raw secret (stored
// on identity.users.mfa_totp_secret) and a provisioning URI an
// authenticator app can consume directly (e.g. rendered as a QR code by
// the caller).
func GenerateTOTPSecret(accountName string) (secret string, provisioningURI string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "SL-Bus",
		AccountName: accountName,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// VerifyTOTPCode reports whether code is a valid current TOTP code for secret.
func VerifyTOTPCode(secret, code string) bool {
	return totp.Validate(code, secret)
}
