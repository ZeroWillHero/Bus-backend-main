-- TOTP replay protection: the last accepted TOTP time step (unix/30) per
-- user. VerifyMFA only accepts a code whose step is strictly greater, so a
-- code can't be replayed within its validity window, nor an older code used
-- after a newer one.
ALTER TABLE identity.users
    ADD COLUMN mfa_last_used_step BIGINT;
