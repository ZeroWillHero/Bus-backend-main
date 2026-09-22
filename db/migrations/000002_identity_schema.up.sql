-- Identity & Access context (DOMAIN_MODEL.md §2.1, AUTHENTICATION_DESIGN.md).

CREATE TABLE identity.users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone           TEXT UNIQUE,           -- passengers: phone + OTP, no password (AUTH_DESIGN §2)
    email           TEXT UNIQUE,           -- operator/admin/staff: email + password (AUTH_DESIGN §3)
    password_hash   TEXT,                  -- NULL for passenger-only accounts
    role            TEXT NOT NULL CHECK (role IN (
                        'passenger', 'operator_staff', 'operator_admin',
                        'finance_admin', 'support', 'platform_admin')),
    operator_id     UUID,                  -- NULL for passenger/platform-level roles
    mfa_totp_secret TEXT,                  -- set once MFA is enrolled (mandatory for admin/finance/support/platform roles)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT users_identifier_present CHECK (phone IS NOT NULL OR email IS NOT NULL)
);

CREATE INDEX idx_users_operator_id ON identity.users (operator_id) WHERE operator_id IS NOT NULL;

-- One-time passwords for the passenger phone-verification flow
-- (AUTHENTICATION_DESIGN.md §2.2). Rows are short-lived and swept by the
-- worker; never reused across requests.
CREATE TABLE identity.otp_codes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone       TEXT NOT NULL,
    code_hash   TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_otp_codes_phone ON identity.otp_codes (phone, created_at DESC);

-- Refresh tokens are stored (hashed) specifically so they can be revoked
-- (NFR-18, AUTHENTICATION_DESIGN.md §4.3) — access tokens are stateless
-- and never stored.
CREATE TABLE identity.refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    device_info TEXT,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user_id ON identity.refresh_tokens (user_id);

-- Trusted-device MFA skip (AUTHENTICATION_DESIGN.md §3.3, NFR-50) — never
-- applies to the platform_admin role; enforced in application logic, not
-- by a constraint here, since it's a business rule rather than a data
-- integrity rule.
CREATE TABLE identity.trusted_devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    device_fingerprint TEXT NOT NULL,
    trusted_until TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, device_fingerprint)
);
