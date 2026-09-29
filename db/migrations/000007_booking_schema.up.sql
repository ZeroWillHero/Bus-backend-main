-- Phase 3: Booking Core schema — the seat-hold → pay → confirm state machine,
-- with P0 mitigations for duplicate webhooks (2.1) and late payments (1.3).
-- Per DATABASE_DESIGN.md §3 and MITIGATION_PLAN.md §1/§2.

CREATE SCHEMA IF NOT EXISTS booking;
GRANT USAGE ON SCHEMA booking TO slbus_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA booking GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO slbus_app;

CREATE TABLE booking.bookings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id UUID NOT NULL REFERENCES fleet.trips(id) ON DELETE RESTRICT,
    passenger_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'held'
        CHECK (status IN ('held','confirmed','cancelled','paid_but_expired')),
    total_amount NUMERIC(10,2) NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'LKR',
    hold_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ
);

CREATE TABLE booking.booking_seats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES booking.bookings(id) ON DELETE RESTRICT,
    seat_number TEXT NOT NULL,
    passenger_name TEXT NOT NULL,
    passenger_nic TEXT,
    fare_amount NUMERIC(10,2) NOT NULL,
    UNIQUE (booking_id, seat_number)
);

CREATE TABLE booking.tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL UNIQUE REFERENCES booking.bookings(id) ON DELETE RESTRICT,
    qr_token TEXT NOT NULL UNIQUE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked_in_at TIMESTAMPTZ
);

-- Mitigation 2.1: reject duplicate webhook events before they reach domain logic
CREATE TABLE booking.processed_webhook_events (
    gateway_event_id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Outbox pattern (ARCHITECTURE.md §3.3): events written atomically with state changes
ALTER TABLE IF EXISTS platform.outbox_events DROP CONSTRAINT IF EXISTS platform_outbox_events_pkey CASCADE;
DROP TABLE IF EXISTS platform.outbox_events;
CREATE TABLE platform.outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);
GRANT USAGE ON SCHEMA platform TO slbus_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA platform GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO slbus_app;

-- RLS policies: bookings are visible to the booking's passenger, the trip's operator, or platform admins
ALTER TABLE booking.bookings ENABLE ROW LEVEL SECURITY;
ALTER TABLE booking.bookings FORCE ROW LEVEL SECURITY;

CREATE POLICY bookings_isolation ON booking.bookings
    FOR SELECT
    USING (
        passenger_id = current_setting('app.current_user_id', true)::uuid
        OR EXISTS (
            SELECT 1 FROM fleet.trips t
            WHERE t.id = bookings.trip_id
            AND t.operator_id = current_setting('app.current_operator_id', true)::uuid
        )
        OR current_setting('app.current_role', true) = 'platform_admin'
    );

CREATE POLICY bookings_insert ON booking.bookings
    FOR INSERT
    WITH CHECK (true);

CREATE POLICY bookings_update ON booking.bookings
    FOR UPDATE
    USING (
        passenger_id = current_setting('app.current_user_id', true)::uuid
        OR EXISTS (
            SELECT 1 FROM fleet.trips t
            WHERE t.id = bookings.trip_id
            AND t.operator_id = current_setting('app.current_operator_id', true)::uuid
        )
        OR current_setting('app.current_role', true) = 'platform_admin'
    );

-- booking_seats: visible if the booking is visible
ALTER TABLE booking.booking_seats ENABLE ROW LEVEL SECURITY;
ALTER TABLE booking.booking_seats FORCE ROW LEVEL SECURITY;

CREATE POLICY booking_seats_isolation ON booking.booking_seats
    FOR SELECT
    USING (
        EXISTS (
            SELECT 1 FROM booking.bookings b
            WHERE b.id = booking_seats.booking_id
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

CREATE POLICY booking_seats_insert ON booking.booking_seats
    FOR INSERT
    WITH CHECK (true);

-- tickets: visible if the booking is visible
ALTER TABLE booking.tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE booking.tickets FORCE ROW LEVEL SECURITY;

CREATE POLICY tickets_isolation ON booking.tickets
    FOR SELECT
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

CREATE POLICY tickets_insert ON booking.tickets
    FOR INSERT
    WITH CHECK (true);

-- processed_webhook_events: service-only writes, no passenger/operator read access
ALTER TABLE booking.processed_webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE booking.processed_webhook_events FORCE ROW LEVEL SECURITY;

CREATE POLICY processed_webhook_events_all_deny ON booking.processed_webhook_events
    AS PERMISSIVE FOR SELECT
    USING (false);

CREATE POLICY processed_webhook_events_insert ON booking.processed_webhook_events
    FOR INSERT
    WITH CHECK (true);

-- outbox_events: worker-only, no passenger/operator access
ALTER TABLE platform.outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.outbox_events FORCE ROW LEVEL SECURITY;

CREATE POLICY outbox_events_all_deny ON platform.outbox_events
    AS PERMISSIVE FOR SELECT
    USING (false);

CREATE POLICY outbox_events_insert ON platform.outbox_events
    FOR INSERT
    WITH CHECK (true);

CREATE POLICY outbox_events_update ON platform.outbox_events
    FOR UPDATE
    USING (false);

-- Indexes for common queries
CREATE INDEX idx_bookings_trip_id ON booking.bookings (trip_id);
CREATE INDEX idx_bookings_passenger_id ON booking.bookings (passenger_id);
CREATE INDEX idx_bookings_status ON booking.bookings (status);
CREATE INDEX idx_bookings_hold_expires_at ON booking.bookings (hold_expires_at) WHERE status = 'held';
CREATE INDEX idx_booking_seats_booking_id ON booking.booking_seats (booking_id);
CREATE INDEX idx_tickets_booking_id ON booking.tickets (booking_id);
CREATE INDEX idx_outbox_events_processed_at ON platform.outbox_events (processed_at) WHERE processed_at IS NULL;
