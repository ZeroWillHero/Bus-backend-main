-- The API connects as a dedicated, non-superuser role rather than the
-- migration-running `slbus` user. This is required, not cosmetic: the
-- `slbus` role (created via docker-compose's POSTGRES_USER) is a
-- superuser, and Postgres superusers bypass Row-Level Security
-- unconditionally — FORCE ROW LEVEL SECURITY (migration 000004) has no
-- effect for a superuser connection. Without this role, every RLS policy
-- in this schema would silently do nothing at runtime, discovered by the
-- Phase 1 RLS contract test (internal/ops/repository/operator_repo_rls_test.go).
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'slbus_app') THEN
        CREATE ROLE slbus_app LOGIN PASSWORD 'slbus_app'
            NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA identity, ops, fleet, booking, finance, notify, review, audit TO slbus_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity TO slbus_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ops TO slbus_app;

-- Future migrations' tables in these schemas should be reachable by the
-- app role without a follow-up GRANT.
ALTER DEFAULT PRIVILEGES IN SCHEMA identity GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO slbus_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA ops GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO slbus_app;
