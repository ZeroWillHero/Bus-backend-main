-- Operator & Multi-Tenant Management context (DOMAIN_MODEL.md §2.1,
-- DATABASE_DESIGN.md §3 ERD, SRS.md FR-OPS-1..6).
--
-- `status` includes 'rejected' in addition to the ERD's three values —
-- FR-OPS-3 requires four verbs (approve/reject/suspend/reactivate), so
-- 'rejected' must be a distinct terminal state alongside 'pending',
-- 'approved', and 'suspended' (which 'reactivate' returns to 'approved').
CREATE TABLE ops.operators (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL,
    business_reg_no      TEXT NOT NULL UNIQUE,
    status               TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'approved', 'rejected', 'suspended')),
    commission_rate      NUMERIC(5,4),
    bank_account         JSONB,
    cancellation_policy  JSONB,
    contact_email        TEXT,
    contact_phone        TEXT,
    -- Placeholder for the route permit/verification document (FR-OPS-1) —
    -- a URL/reference only; no object-storage upload pipeline exists yet.
    registration_doc_url TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at          TIMESTAMPTZ
);

CREATE INDEX idx_operators_status ON ops.operators (status);

-- Multi-tenancy backstop (DATABASE_DESIGN.md §5). `operators` is the
-- tenant root itself (no operator_id FK — its own `id` is the tenant key),
-- so the policy checks `id` directly rather than an `operator_id` column.
ALTER TABLE ops.operators ENABLE ROW LEVEL SECURITY;
-- FORCE is required because the application connects as the same role
-- that owns this table (single DB user, docker-compose.yml) — without
-- FORCE, Postgres exempts the table owner from RLS by default, which
-- would silently defeat the isolation this table exists to enforce.
ALTER TABLE ops.operators FORCE ROW LEVEL SECURITY;

-- Split by command rather than one blanket USING clause: for INSERT,
-- USING would otherwise double as WITH CHECK, and a brand-new row's id
-- can never equal the inserting session's app.current_operator_id (it
-- doesn't exist yet) — that would make self-registration (FR-OPS-1, an
-- intentionally unauthenticated action) impossible at the DB layer.
-- Authorization for *who* may create an operator is an application-layer
-- concern (there's no endpoint that lets one operator insert a row for
-- another); RLS's job here is read/write isolation of existing rows.
--
-- current_setting(..., true) (missing_ok) is used throughout so a
-- session that never ran SET LOCAL (an unauthenticated request, or code
-- that forgot to scope its transaction) gets NULL — safely denied by the
-- USING/WITH CHECK comparisons — instead of a hard error.
CREATE POLICY operator_insert ON ops.operators
    FOR INSERT WITH CHECK (true);

CREATE POLICY operator_isolation_select ON ops.operators
    FOR SELECT USING (id = current_setting('app.current_operator_id', true)::uuid
                       OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY operator_isolation_update ON ops.operators
    FOR UPDATE USING (id = current_setting('app.current_operator_id', true)::uuid
                       OR current_setting('app.current_role', true) = 'platform_admin')
    WITH CHECK (id = current_setting('app.current_operator_id', true)::uuid
                OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY operator_isolation_delete ON ops.operators
    FOR DELETE USING (id = current_setting('app.current_operator_id', true)::uuid
                       OR current_setting('app.current_role', true) = 'platform_admin');
