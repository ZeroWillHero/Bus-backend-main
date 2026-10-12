-- Audit fixes: Row-Level Security for the booking path.
--
-- 1. Once any transaction on a connection has run SET LOCAL "app.current_*",
--    Postgres keeps that custom setting defined as '' for the rest of the
--    session, and every policy's `current_setting(..)::uuid` then fails with
--    `invalid input syntax for type uuid: ""`. Give the API role a valid
--    default so a reverted setting is the zero uuid (matches no row), never ''.
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'slbus_app') THEN
        ALTER ROLE slbus_app SET "app.current_operator_id" = '00000000-0000-0000-0000-000000000000';
        ALTER ROLE slbus_app SET "app.current_user_id"     = '00000000-0000-0000-0000-000000000000';
        ALTER ROLE slbus_app SET "app.current_role"        = '';
    END IF;
END $$;

-- 2. Checking a ticket in UPDATEs booking.tickets, which had no UPDATE policy,
--    so under RLS it matched zero rows. Same visibility as SELECT: the
--    booking's passenger, the trip's operator, or a platform admin.
CREATE POLICY tickets_update ON booking.tickets
    FOR UPDATE
    USING (
        EXISTS (
            SELECT 1 FROM booking.bookings b
            WHERE b.id = booking.tickets.booking_id
            AND (
                b.passenger_id = current_setting('app.current_user_id', true)::uuid
                OR EXISTS (
                    SELECT 1 FROM fleet.trips t
                    WHERE t.id = b.trip_id
                    AND t.operator_id = current_setting('app.current_operator_id', true)::uuid
                )
                OR current_setting('app.current_role', true) = 'platform_admin'
            )
        )
    );

-- 3. platform.outbox_events was created before the ALTER DEFAULT PRIVILEGES in
--    000007 took effect, so slbus_app had no access and every confirm/cancel
--    failed at the outbox write ("permission denied for table outbox_events").
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA platform TO slbus_app;
