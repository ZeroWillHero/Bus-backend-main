-- Account-lockout support for the email/password login flow
-- (AUTHENTICATION_DESIGN.md §6 — 5 consecutive failed attempts triggers a
-- temporary, exponential-backoff lockout, not a permanent one).

ALTER TABLE identity.users
    ADD COLUMN failed_login_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN locked_until TIMESTAMPTZ;
