package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

// LockoutBaseDelay is the base of the exponential backoff applied after
// MaxFailedLoginAttempts (AUTHENTICATION_DESIGN.md §6).
const LockoutBaseDelay = 30 * time.Second

// LoginResult is either a full TokenPair (MFA not required, or already
// satisfied via a trusted device) or an MFA challenge the caller must
// complete via MFAService.VerifyMFA.
type LoginResult struct {
	Tokens          *TokenPair
	MFAChallenge    bool
	PreAuthToken    string
	MFAEnrollNeeded bool
}

// OperatorStatusChecker lets LoginService reject a login for an
// operator_admin/operator_staff user whose operator has been suspended
// (OPS_OPERATOR_SUSPENDED) — identity depends on this narrow port rather
// than importing internal/ops directly, keeping the dependency direction
// the context map specifies (ops depends on identity, not the reverse).
type OperatorStatusChecker interface {
	IsOperatorSuspended(ctx context.Context, operatorID string) (bool, error)
}

type LoginService struct {
	userRepo       domain.UserRepository
	trustedRepo    domain.TrustedDeviceRepository
	breachChecker  auth.PasswordBreachChecker
	operatorStatus OperatorStatusChecker
	preAuth        *PreAuthTokenStore
	tokens         *TokenIssuer
}

func NewLoginService(
	userRepo domain.UserRepository,
	trustedRepo domain.TrustedDeviceRepository,
	breachChecker auth.PasswordBreachChecker,
	operatorStatus OperatorStatusChecker,
	preAuth *PreAuthTokenStore,
	tokens *TokenIssuer,
) *LoginService {
	return &LoginService{
		userRepo:       userRepo,
		trustedRepo:    trustedRepo,
		breachChecker:  breachChecker,
		operatorStatus: operatorStatus,
		preAuth:        preAuth,
		tokens:         tokens,
	}
}

func (s *LoginService) Login(ctx context.Context, email, password, deviceFingerprint, deviceInfo string) (*LoginResult, error) {
	user, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		if err == domain.ErrUserNotFound {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}

	now := time.Now()
	if user.IsLocked(now) {
		return nil, domain.ErrAccountLocked
	}

	if !auth.VerifyPassword(user.PasswordHash, password) {
		updated, incErr := s.userRepo.IncrementFailedAttempts(ctx, user.ID)
		if incErr != nil {
			return nil, incErr
		}
		if updated.FailedLoginAttempts >= domain.MaxFailedLoginAttempts {
			backoff := time.Duration(math.Pow(2, float64(updated.FailedLoginAttempts-domain.MaxFailedLoginAttempts))) * LockoutBaseDelay
			if err := s.userRepo.SetLockedUntil(ctx, user.ID, now.Add(backoff)); err != nil {
				return nil, err
			}
		}
		return nil, domain.ErrInvalidCredentials
	}

	if user.OperatorID != "" && s.operatorStatus != nil {
		suspended, err := s.operatorStatus.IsOperatorSuspended(ctx, user.OperatorID)
		if err != nil {
			return nil, err
		}
		if suspended {
			return nil, ErrOperatorSuspended
		}
	}

	if err := s.userRepo.ResetLockout(ctx, user.ID); err != nil {
		return nil, err
	}

	if !domain.RequiresMFA(user.Role) {
		tokens, err := s.tokens.IssuePair(ctx, user, deviceInfo)
		if err != nil {
			return nil, err
		}
		return &LoginResult{Tokens: tokens}, nil
	}

	if user.MFATOTPSecret == "" {
		// Mandatory-MFA role that hasn't enrolled yet (e.g. right after
		// self-registration) — caller must hit /auth/mfa/enroll next.
		preAuth := s.preAuth.Issue(user.ID, deviceFingerprint, deviceInfo)
		return &LoginResult{MFAChallenge: true, PreAuthToken: preAuth, MFAEnrollNeeded: true}, nil
	}

	if domain.TrustedDeviceEligible(user.Role) && deviceFingerprint != "" {
		device, err := s.trustedRepo.Find(ctx, user.ID, deviceFingerprint)
		if err != nil && err != domain.ErrNotFound {
			return nil, err
		}
		if device != nil && device.Trusted(now) {
			tokens, err := s.tokens.IssuePair(ctx, user, deviceInfo)
			if err != nil {
				return nil, err
			}
			return &LoginResult{Tokens: tokens}, nil
		}
	}

	preAuth := s.preAuth.Issue(user.ID, deviceFingerprint, deviceInfo)
	return &LoginResult{MFAChallenge: true, PreAuthToken: preAuth}, nil
}

// ErrOperatorSuspended surfaces as OPS_OPERATOR_SUSPENDED at the HTTP layer.
var ErrOperatorSuspended = errors.New("identity: operator suspended")

// PreAuthTokenStore holds short-lived, single-use, in-memory tokens that
// carry a user through the gap between "password verified" and "MFA
// verified." In-memory is sufficient for a single API instance — flagged
// as a follow-up if the service ever scales horizontally without sticky
// sessions (would need a shared store, e.g. Redis).
type PreAuthTokenStore struct {
	mu      sync.Mutex
	entries map[string]preAuthEntry
}

type preAuthEntry struct {
	userID            string
	deviceFingerprint string
	deviceInfo        string
	expiresAt         time.Time

	// pendingMFASecret is the TOTP secret from /mfa/enroll, held here
	// until a code generated from it verifies — so an enrollment the user
	// abandons before adding it to their authenticator never locks them
	// out on their next login.
	pendingMFASecret string
	attempts         int
}

func NewPreAuthTokenStore() *PreAuthTokenStore {
	return &PreAuthTokenStore{entries: make(map[string]preAuthEntry)}
}

const preAuthTokenTTL = 5 * time.Minute

// maxPreAuthAttempts caps MFA codes tried against one pre-auth token, so
// a mistyped code can be retried without letting the token be used to
// brute-force the 6-digit code.
const maxPreAuthAttempts = 5

func (s *PreAuthTokenStore) Issue(userID, deviceFingerprint, deviceInfo string) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[token] = preAuthEntry{
		userID:            userID,
		deviceFingerprint: deviceFingerprint,
		deviceInfo:        deviceInfo,
		expiresAt:         time.Now().Add(preAuthTokenTTL),
	}
	return token
}

// Peek reads the entry for token without consuming it — used by MFA
// enrollment, which must happen using the pre-auth token (no access token
// exists yet at that point) without invalidating it before the
// subsequent /mfa/verify call.
func (s *PreAuthTokenStore) Peek(token string) (userID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.entries[token]
	if !found || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.userID, true
}

// SetPendingMFASecret attaches a not-yet-confirmed TOTP secret to token.
func (s *PreAuthTokenStore) SetPendingMFASecret(token, secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.entries[token]
	if !found || time.Now().After(e.expiresAt) {
		return false
	}
	e.pendingMFASecret = secret
	s.entries[token] = e
	return true
}

// BeginAttempt counts one MFA code attempt against token and returns its
// entry, or false once the token is expired or out of attempts. The count
// is taken before the code is checked, so concurrent requests can't get
// past the cap. A successful check must then Consume the token.
func (s *PreAuthTokenStore) BeginAttempt(token string) (preAuthEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.entries[token]
	if !found {
		return preAuthEntry{}, false
	}
	if time.Now().After(e.expiresAt) || e.attempts >= maxPreAuthAttempts {
		delete(s.entries, token)
		return preAuthEntry{}, false
	}
	e.attempts++
	s.entries[token] = e
	return e, true
}

// Consume returns and deletes the entry for token — single use, per the
// pre-auth token's purpose.
func (s *PreAuthTokenStore) Consume(token string) (userID, deviceFingerprint, deviceInfo string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.entries[token]
	if !found {
		return "", "", "", false
	}
	delete(s.entries, token)
	if time.Now().After(e.expiresAt) {
		return "", "", "", false
	}
	return e.userID, e.deviceFingerprint, e.deviceInfo, true
}
