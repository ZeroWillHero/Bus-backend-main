package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/identity/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/ratelimit"
)

// memUsers is an in-memory domain.UserRepository covering what the
// register → login → MFA flow touches; the embedded interface panics on
// anything else.
type memUsers struct {
	domain.UserRepository
	byID map[string]*domain.User
}

func (m *memUsers) find(match func(*domain.User) bool) (*domain.User, error) {
	for _, u := range m.byID {
		if match(u) {
			copied := *u
			return &copied, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (m *memUsers) FindByEmail(_ context.Context, email string) (*domain.User, error) {
	return m.find(func(u *domain.User) bool { return u.Email == email })
}

func (m *memUsers) FindByID(_ context.Context, id string) (*domain.User, error) {
	return m.find(func(u *domain.User) bool { return u.ID == id })
}

func (m *memUsers) Create(_ context.Context, u *domain.User) (*domain.User, error) {
	if _, err := m.FindByEmail(context.Background(), u.Email); err == nil {
		return nil, domain.ErrEmailTaken
	}
	created := *u
	created.ID = "user-1"
	m.byID[created.ID] = &created
	return &created, nil
}

func (m *memUsers) ResetLockout(context.Context, string) error { return nil }

func (m *memUsers) SetMFASecret(_ context.Context, id, secret string) error {
	m.byID[id].MFATOTPSecret = secret
	return nil
}

type memTrustedDevices struct{ until map[string]time.Time }

func (m *memTrustedDevices) Find(_ context.Context, userID, fingerprint string) (*domain.TrustedDevice, error) {
	until, ok := m.until[userID+"/"+fingerprint]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.TrustedDevice{UserID: userID, DeviceFingerprint: fingerprint, TrustedUntil: until}, nil
}

func (m *memTrustedDevices) Upsert(_ context.Context, userID, fingerprint string, until time.Time) (*domain.TrustedDevice, error) {
	m.until[userID+"/"+fingerprint] = until
	return &domain.TrustedDevice{UserID: userID, DeviceFingerprint: fingerprint, TrustedUntil: until}, nil
}

type memRefreshTokens struct{ domain.RefreshTokenRepository }

func (memRefreshTokens) Create(_ context.Context, userID, tokenHash, deviceInfo string, expiresAt time.Time) (*domain.RefreshToken, error) {
	return &domain.RefreshToken{UserID: userID, TokenHash: tokenHash, DeviceInfo: deviceInfo, ExpiresAt: expiresAt}, nil
}

func newPassengerTestRouter() http.Handler {
	users := &memUsers{byID: map[string]*domain.User{}}
	devices := &memTrustedDevices{until: map[string]time.Time{}}
	preAuth := service.NewMemoryPreAuthStore()
	failures := ratelimit.NewMemoryCounter()
	tokens := service.NewTokenIssuer(auth.NewJWTIssuer("test-signing-key"), memRefreshTokens{})
	return NewRouter(Deps{
		PassengerRegistration: service.NewPassengerRegistrationService(users, auth.NoopBreachChecker{}),
		Login:                 service.NewLoginService(users, devices, auth.NoopBreachChecker{}, nil, preAuth, failures, tokens),
		MFA:                   service.NewMFAService(users, devices, preAuth, failures, tokens),
	})
}

func post(t *testing.T, router http.Handler, path string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)))
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("POST %s: response isn't JSON (%d): %s", path, rec.Code, rec.Body.String())
	}
	return rec.Code, envelope
}

func data(envelope map[string]any) map[string]any {
	d, _ := envelope["data"].(map[string]any)
	return d
}

func errorCode(envelope map[string]any) string {
	e, _ := envelope["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// The passenger app's whole sign-up: register, log in, set up the
// authenticator, verify — then a second login from the same device skips
// the code (trusted device) and one from another device doesn't.
func TestPassengerRegisterLoginAndTOTPFlow(t *testing.T) {
	router := newPassengerTestRouter()
	account := map[string]string{"email": "ann@example.com", "password": "long-enough-password"}

	if status, env := post(t, router, "/passenger/register", account); status != http.StatusCreated {
		t.Fatalf("register = %d %v, want 201", status, env)
	}
	if status, env := post(t, router, "/passenger/register", account); status != http.StatusConflict || errorCode(env) != "AUTH_EMAIL_TAKEN" {
		t.Fatalf("duplicate register = %d %s, want 409 AUTH_EMAIL_TAKEN", status, errorCode(env))
	}
	if status, env := post(t, router, "/passenger/register", map[string]string{"email": "bob@example.com", "password": "short"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("short password register = %d %v, want 422", status, env)
	}

	login := map[string]string{"email": "ann@example.com", "password": "long-enough-password", "deviceFingerprint": "phone-1"}
	status, env := post(t, router, "/login", login)
	if status != http.StatusOK || data(env)["mfaChallengeRequired"] != true || data(env)["mfaEnrollNeeded"] != true {
		t.Fatalf("first login = %d %v, want an MFA enrollment challenge", status, env)
	}
	preAuth := data(env)["preAuthToken"].(string)

	status, env = post(t, router, "/mfa/enroll", map[string]string{"preAuthToken": preAuth})
	if status != http.StatusOK {
		t.Fatalf("enroll = %d %v, want 200", status, env)
	}
	secret := data(env)["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())

	status, env = post(t, router, "/mfa/verify", map[string]string{"preAuthToken": preAuth, "totpCode": code})
	if status != http.StatusOK || data(env)["accessToken"] == nil || data(env)["role"] != "passenger" {
		t.Fatalf("verify = %d %v, want passenger tokens", status, env)
	}

	status, env = post(t, router, "/login", login)
	if status != http.StatusOK || data(env)["accessToken"] == nil {
		t.Fatalf("login from the trusted device = %d %v, want tokens without a code", status, env)
	}

	login["deviceFingerprint"] = "phone-2"
	status, env = post(t, router, "/login", login)
	if status != http.StatusOK || data(env)["mfaChallengeRequired"] != true || data(env)["mfaEnrollNeeded"] == true {
		t.Fatalf("login from a new device = %d %v, want a code challenge (no enrollment)", status, env)
	}
}
