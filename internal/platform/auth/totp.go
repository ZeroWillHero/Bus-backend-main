package auth

import (
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPPeriod is the TOTP time-step length (RFC 6238 default, and what
// GenerateTOTPSecret's provisioning URI advertises).
const TOTPPeriod = 30

// totpSkew is how many steps either side of now a code is accepted for,
// matching totp.Validate's default.
const totpSkew = 1

var totpStepOpts = totp.ValidateOpts{
	Period:    TOTPPeriod,
	Skew:      0,
	Digits:    otp.DigitsSix,
	Algorithm: otp.AlgorithmSHA1,
}

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
// It does not stop replays; login uses MatchTOTPStep plus a stored
// last-used step for that.
func VerifyTOTPCode(secret, code string) bool {
	_, ok := MatchTOTPStep(secret, code, time.Now())
	return ok
}

// MatchTOTPStep reports which time step (unix seconds / TOTPPeriod) code
// belongs to, accepting the same ±1 step window as totp.Validate. Callers
// persist the step and refuse any code whose step isn't newer, so a code
// can't be replayed within its window.
func MatchTOTPStep(secret, code string, now time.Time) (step int64, ok bool) {
	for skew := -totpSkew; skew <= totpSkew; skew++ {
		t := now.Add(time.Duration(skew*TOTPPeriod) * time.Second)
		valid, err := totp.ValidateCustom(code, secret, t, totpStepOpts)
		if err == nil && valid {
			return t.Unix() / TOTPPeriod, true
		}
	}
	return 0, false
}
