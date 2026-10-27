ALTER TABLE identity.users
    DROP COLUMN IF EXISTS mfa_last_used_step;
