package sms

import (
	"strings"
)

// lkMobilePrefixes are the allocated Sri Lankan mobile operator prefixes
// (070–078 except 073). Only these are accepted: restricting destinations
// to local mobiles shuts off SMS-pumping / toll-fraud to premium or
// international numbers.
var lkMobilePrefixes = map[string]bool{
	"70": true, "71": true, "72": true, "74": true, "75": true,
	"76": true, "77": true, "78": true,
}

// NormalizeLKMobile converts the common ways a Sri Lankan mobile number is
// written (0771234567, 771234567, 94771234567, +94 77 123 4567,
// 0094771234567) to E.164 (+94771234567), or returns ErrInvalidNumber.
func NormalizeLKMobile(raw string) (string, error) {
	var b strings.Builder
	for i, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return "", ErrInvalidNumber
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "0094"):
		d = d[4:]
	case strings.HasPrefix(d, "94") && len(d) == 11:
		d = d[2:]
	case strings.HasPrefix(d, "0") && len(d) == 10:
		d = d[1:]
	}
	if len(d) != 9 || !lkMobilePrefixes[d[:2]] {
		return "", ErrInvalidNumber
	}
	return "+94" + d, nil
}

// Mask hides all but the last three digits of an E.164 number for display
// and logs: +94771234567 -> +94 7•• ••• 567.
func Mask(e164 string) string {
	if len(e164) != 12 || !strings.HasPrefix(e164, "+94") {
		return "•••"
	}
	return "+94 " + e164[3:4] + "•• ••• " + e164[9:]
}
