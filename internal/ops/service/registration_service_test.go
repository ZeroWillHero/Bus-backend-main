package service

import "testing"

func TestValidatePasswordLength(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"exactly min length", "1234567890", false},
		{"one under min length", "123456789", true},
		{"well above min", "a-reasonably-long-passphrase", false},
		{"empty", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePasswordLength(c.password)
			if (err != nil) != c.wantErr {
				t.Errorf("validatePasswordLength(%q) error = %v, wantErr %v", c.password, err, c.wantErr)
			}
		})
	}
}
