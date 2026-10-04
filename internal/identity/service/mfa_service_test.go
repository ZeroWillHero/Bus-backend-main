package service

import (
	"context"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

// fakeUserRepo keeps users in memory. Embedding the interface leaves the
// methods these tests never call unimplemented (they'd panic if reached).
type fakeUserRepo struct {
	domain.UserRepository
	users map[string]*domain.User
}

func newFakeUserRepo(users ...*domain.User) *fakeUserRepo {
	r := &fakeUserRepo{users: map[string]*domain.User{}}
	for _, u := range users {
		r.users[u.ID] = u
	}
	return r
}

func (r *fakeUserRepo) FindByID(_ context.Context, id string) (*domain.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	copied := *u
	return &copied, nil
}

func (r *fakeUserRepo) SetMFASecret(_ context.Context, id string, secret string) error {
	r.users[id].MFATOTPSecret = secret
	return nil
}

func (r *fakeUserRepo) Create(_ context.Context, u *domain.User) (*domain.User, error) {
	for _, existing := range r.users {
		if existing.Email == u.Email {
			return nil, domain.ErrEmailTaken
		}
	}
	created := *u
	created.ID = "user-" + u.Email
	r.users[created.ID] = &created
	return &created, nil
}

type fakeTrustedDevices struct {
	upserted []string
}

func (f *fakeTrustedDevices) Find(context.Context, string, string) (*domain.TrustedDevice, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeTrustedDevices) Upsert(_ context.Context, userID, fingerprint string, until time.Time) (*domain.TrustedDevice, error) {
	f.upserted = append(f.upserted, fingerprint)
	return &domain.TrustedDevice{UserID: userID, DeviceFingerprint: fingerprint, TrustedUntil: until}, nil
}

type fakeRefreshTokens struct {
	domain.RefreshTokenRepository
}

func (fakeRefreshTokens) Create(_ context.Context, userID, tokenHash, deviceInfo string, expiresAt time.Time) (*domain.RefreshToken, error) {
	return &domain.RefreshToken{UserID: userID, TokenHash: tokenHash, DeviceInfo: deviceInfo, ExpiresAt: expiresAt}, nil
}

type mfaFixture struct {
	users   *fakeUserRepo
	devices *fakeTrustedDevices
	preAuth *PreAuthTokenStore
	svc     *MFAService
}

func newMFAFixture(user *domain.User) *mfaFixture {
	f := &mfaFixture{
		users:   newFakeUserRepo(user),
		devices: &fakeTrustedDevices{},
		preAuth: NewPreAuthTokenStore(),
	}
	tokens := NewTokenIssuer(auth.NewJWTIssuer("test-signing-key"), fakeRefreshTokens{})
	f.svc = NewMFAService(f.users, f.devices, f.preAuth, tokens)
	return f
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

// wrongCode is a valid-looking code that isn't the current one.
func wrongCode(t *testing.T, secret string) string {
	if currentCode(t, secret) == "000000" {
		return "111111"
	}
	return "000000"
}

func TestEnrollMFAOnlySavesTheSecretOnceACodeVerifies(t *testing.T) {
	ctx := context.Background()
	f := newMFAFixture(&domain.User{ID: "u1", Email: "ann@example.com", Role: domain.RolePassenger})
	token := f.preAuth.Issue("u1", "device-1", "test")

	secret, uri, err := f.svc.EnrollMFA(ctx, token)
	if err != nil {
		t.Fatalf("EnrollMFA: %v", err)
	}
	if secret == "" || uri == "" {
		t.Fatalf("EnrollMFA returned secret=%q uri=%q, want both set", secret, uri)
	}
	if got := f.users.users["u1"].MFATOTPSecret; got != "" {
		t.Fatalf("secret saved before verification: %q", got)
	}

	if _, err := f.svc.VerifyMFA(ctx, token, currentCode(t, secret)); err != nil {
		t.Fatalf("VerifyMFA: %v", err)
	}
	if got := f.users.users["u1"].MFATOTPSecret; got != secret {
		t.Fatalf("saved secret = %q, want the enrolled one", got)
	}
	if len(f.devices.upserted) != 1 || f.devices.upserted[0] != "device-1" {
		t.Fatalf("trusted devices = %v, want [device-1]", f.devices.upserted)
	}
}

func TestEnrollMFARejectsAnAlreadyEnrolledAccount(t *testing.T) {
	f := newMFAFixture(&domain.User{ID: "u1", Email: "ann@example.com", Role: domain.RolePassenger, MFATOTPSecret: "EXISTINGSECRET"})
	token := f.preAuth.Issue("u1", "", "test")

	if _, _, err := f.svc.EnrollMFA(context.Background(), token); err != domain.ErrMFAAlreadyEnrolled {
		t.Fatalf("EnrollMFA error = %v, want ErrMFAAlreadyEnrolled", err)
	}
	if got := f.users.users["u1"].MFATOTPSecret; got != "EXISTINGSECRET" {
		t.Fatalf("existing secret was replaced with %q", got)
	}
}

func TestVerifyMFAAllowsRetryingAWrongCode(t *testing.T) {
	ctx := context.Background()
	secret, _, _ := auth.GenerateTOTPSecret("ann@example.com")
	f := newMFAFixture(&domain.User{ID: "u1", Email: "ann@example.com", Role: domain.RolePassenger, MFATOTPSecret: secret})
	token := f.preAuth.Issue("u1", "", "test")

	if _, err := f.svc.VerifyMFA(ctx, token, wrongCode(t, secret)); err != domain.ErrInvalidMFACode {
		t.Fatalf("wrong code error = %v, want ErrInvalidMFACode", err)
	}
	if _, err := f.svc.VerifyMFA(ctx, token, currentCode(t, secret)); err != nil {
		t.Fatalf("retry with the right code: %v", err)
	}
	if _, err := f.svc.VerifyMFA(ctx, token, currentCode(t, secret)); err != domain.ErrTokenInvalid {
		t.Fatalf("reusing a consumed token error = %v, want ErrTokenInvalid", err)
	}
}

func TestVerifyMFACapsAttemptsPerToken(t *testing.T) {
	ctx := context.Background()
	secret, _, _ := auth.GenerateTOTPSecret("ann@example.com")
	f := newMFAFixture(&domain.User{ID: "u1", Email: "ann@example.com", Role: domain.RolePassenger, MFATOTPSecret: secret})
	token := f.preAuth.Issue("u1", "", "test")

	for i := 0; i < maxPreAuthAttempts; i++ {
		if _, err := f.svc.VerifyMFA(ctx, token, wrongCode(t, secret)); err != domain.ErrInvalidMFACode {
			t.Fatalf("attempt %d error = %v, want ErrInvalidMFACode", i+1, err)
		}
	}
	if _, err := f.svc.VerifyMFA(ctx, token, currentCode(t, secret)); err != domain.ErrTokenInvalid {
		t.Fatalf("after %d wrong codes error = %v, want ErrTokenInvalid", maxPreAuthAttempts, err)
	}
}

func TestRegisterPassenger(t *testing.T) {
	ctx := context.Background()
	users := newFakeUserRepo()
	svc := NewPassengerRegistrationService(users, auth.NoopBreachChecker{})

	if _, err := svc.RegisterPassenger(ctx, "ann@example.com", "short"); err != domain.ErrPasswordTooShort {
		t.Fatalf("short password error = %v, want ErrPasswordTooShort", err)
	}

	user, err := svc.RegisterPassenger(ctx, "  Ann@Example.com ", "long-enough-password")
	if err != nil {
		t.Fatalf("RegisterPassenger: %v", err)
	}
	if user.Email != "ann@example.com" {
		t.Errorf("email = %q, want it trimmed and lower-cased", user.Email)
	}
	if user.Role != domain.RolePassenger {
		t.Errorf("role = %q, want passenger", user.Role)
	}
	if user.MFATOTPSecret != "" {
		t.Errorf("new account already has a TOTP secret")
	}
	if !auth.VerifyPassword(user.PasswordHash, "long-enough-password") {
		t.Errorf("stored hash doesn't match the password")
	}

	if _, err := svc.RegisterPassenger(ctx, "ann@example.com", "another-long-password"); err != domain.ErrEmailTaken {
		t.Fatalf("duplicate email error = %v, want ErrEmailTaken", err)
	}
}
