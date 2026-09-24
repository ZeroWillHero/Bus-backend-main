package auth

import (
	"context"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("a-reasonably-long-passphrase")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(hash, "a-reasonably-long-passphrase") {
		t.Errorf("expected correct password to verify")
	}
	if VerifyPassword(hash, "wrong-passphrase") {
		t.Errorf("expected incorrect password not to verify")
	}
}

func TestNoopBreachCheckerNeverFlags(t *testing.T) {
	breached, err := NoopBreachChecker{}.IsBreached(context.Background(), "password123")
	if err != nil {
		t.Fatalf("IsBreached: %v", err)
	}
	if breached {
		t.Errorf("expected NoopBreachChecker to never flag a password as breached")
	}
}
