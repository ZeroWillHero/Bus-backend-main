-- Fleet & Scheduling context (DOMAIN_MODEL.md §2.3, DATABASE_DESIGN.md §3
-- ERD, SRS.md FR-FLT-1..6/FR-SRCH-1..3).

CREATE TABLE fleet.buses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_id     UUID NOT NULL,
    registration_no TEXT NOT NULL UNIQUE,
    bus_class       TEXT NOT NULL,
    amenities       JSONB,
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_buses_operator_id ON fleet.buses (operator_id);

ALTER TABLE fleet.buses ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.buses FORCE ROW LEVEL SECURITY;

CREATE POLICY buses_insert ON fleet.buses FOR INSERT WITH CHECK (true);

CREATE POLICY buses_select ON fleet.buses FOR SELECT
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY buses_update ON fleet.buses FOR UPDATE
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin')
    WITH CHECK (operator_id = current_setting('app.current_operator_id', true)::uuid
                OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY buses_delete ON fleet.buses FOR DELETE
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin');

-- 1:1 with buses per the ERD (SEAT_MAPS ||--|| SEATS).
CREATE TABLE fleet.seat_maps (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bus_id      UUID NOT NULL UNIQUE REFERENCES fleet.buses (id) ON DELETE CASCADE,
    total_seats INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE fleet.seat_maps ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.seat_maps FORCE ROW LEVEL SECURITY;

CREATE POLICY seat_maps_insert ON fleet.seat_maps FOR INSERT WITH CHECK (true);

CREATE POLICY seat_maps_select ON fleet.seat_maps FOR SELECT
    USING (EXISTS (
        SELECT 1 FROM fleet.buses b WHERE b.id = seat_maps.bus_id
        AND (b.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE POLICY seat_maps_update ON fleet.seat_maps FOR UPDATE
    USING (EXISTS (
        SELECT 1 FROM fleet.buses b WHERE b.id = seat_maps.bus_id
        AND (b.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ))
    WITH CHECK (true);

CREATE POLICY seat_maps_delete ON fleet.seat_maps FOR DELETE
    USING (EXISTS (
        SELECT 1 FROM fleet.buses b WHERE b.id = seat_maps.bus_id
        AND (b.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

-- row_no/col_no form the logical grid the operator's seat-map canvas snaps
-- to; irregular layouts (e.g. a wider back row, aisles in different
-- places per row) are expressed by non-contiguous col_no values per row,
-- not by a separate continuous pixel-coordinate system.
CREATE TABLE fleet.seats (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seat_map_id UUID NOT NULL REFERENCES fleet.seat_maps (id) ON DELETE CASCADE,
    seat_number TEXT NOT NULL,
    seat_type   TEXT NOT NULL,
    row_no      INT NOT NULL,
    col_no      INT NOT NULL,

    UNIQUE (seat_map_id, seat_number)
);

ALTER TABLE fleet.seats ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.seats FORCE ROW LEVEL SECURITY;

CREATE POLICY seats_insert ON fleet.seats FOR INSERT WITH CHECK (true);

CREATE POLICY seats_select ON fleet.seats FOR SELECT
    USING (EXISTS (
        SELECT 1 FROM fleet.seat_maps sm JOIN fleet.buses b ON b.id = sm.bus_id
        WHERE sm.id = seats.seat_map_id
        AND (b.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE POLICY seats_update ON fleet.seats FOR UPDATE
    USING (EXISTS (
        SELECT 1 FROM fleet.seat_maps sm JOIN fleet.buses b ON b.id = sm.bus_id
        WHERE sm.id = seats.seat_map_id
        AND (b.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ))
    WITH CHECK (true);

CREATE POLICY seats_delete ON fleet.seats FOR DELETE
    USING (EXISTS (
        SELECT 1 FROM fleet.seat_maps sm JOIN fleet.buses b ON b.id = sm.bus_id
        WHERE sm.id = seats.seat_map_id
        AND (b.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE TABLE fleet.routes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_id UUID NOT NULL,
    origin      TEXT NOT NULL,
    destination TEXT NOT NULL,
    distance_km NUMERIC(6,2),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_routes_operator_id ON fleet.routes (operator_id);
-- Search's core access path (FR-SRCH-1): origin/destination lookup.
CREATE INDEX idx_routes_origin_destination ON fleet.routes (origin, destination);

ALTER TABLE fleet.routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.routes FORCE ROW LEVEL SECURITY;

CREATE POLICY routes_insert ON fleet.routes FOR INSERT WITH CHECK (true);

CREATE POLICY routes_select ON fleet.routes FOR SELECT
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY routes_update ON fleet.routes FOR UPDATE
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin')
    WITH CHECK (operator_id = current_setting('app.current_operator_id', true)::uuid
                OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY routes_delete ON fleet.routes FOR DELETE
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin');

CREATE TABLE fleet.route_stops (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id            UUID NOT NULL REFERENCES fleet.routes (id) ON DELETE CASCADE,
    stop_name           TEXT NOT NULL,
    sequence            INT NOT NULL,
    arrival_offset_min  INT,
    departure_offset_min INT,

    UNIQUE (route_id, sequence)
);

ALTER TABLE fleet.route_stops ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.route_stops FORCE ROW LEVEL SECURITY;

CREATE POLICY route_stops_insert ON fleet.route_stops FOR INSERT WITH CHECK (true);

CREATE POLICY route_stops_select ON fleet.route_stops FOR SELECT
    USING (EXISTS (
        SELECT 1 FROM fleet.routes r WHERE r.id = route_stops.route_id
        AND (r.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE POLICY route_stops_update ON fleet.route_stops FOR UPDATE
    USING (EXISTS (
        SELECT 1 FROM fleet.routes r WHERE r.id = route_stops.route_id
        AND (r.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ))
    WITH CHECK (true);

CREATE POLICY route_stops_delete ON fleet.route_stops FOR DELETE
    USING (EXISTS (
        SELECT 1 FROM fleet.routes r WHERE r.id = route_stops.route_id
        AND (r.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE TABLE fleet.trips (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_id     UUID NOT NULL,
    route_id        UUID NOT NULL REFERENCES fleet.routes (id) ON DELETE RESTRICT,
    bus_id          UUID NOT NULL REFERENCES fleet.buses (id) ON DELETE RESTRICT,
    departure_at    TIMESTAMPTZ NOT NULL,
    status          TEXT NOT NULL DEFAULT 'scheduled'
                        CHECK (status IN ('scheduled', 'cancelled', 'completed')),
    base_fare       NUMERIC(10,2) NOT NULL,
    -- Denormalized for the search read-path (DATABASE_DESIGN.md §5) —
    -- maintained by the same transaction that seeds/mutates
    -- trip_seat_inventory, never recomputed per search hit.
    available_seats INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_trips_route_departure ON fleet.trips (route_id, departure_at);
CREATE INDEX idx_trips_operator_status ON fleet.trips (operator_id, status);

ALTER TABLE fleet.trips ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.trips FORCE ROW LEVEL SECURITY;

CREATE POLICY trips_insert ON fleet.trips FOR INSERT WITH CHECK (true);

CREATE POLICY trips_select ON fleet.trips FOR SELECT
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY trips_update ON fleet.trips FOR UPDATE
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin')
    WITH CHECK (operator_id = current_setting('app.current_operator_id', true)::uuid
                OR current_setting('app.current_role', true) = 'platform_admin');

CREATE POLICY trips_delete ON fleet.trips FOR DELETE
    USING (operator_id = current_setting('app.current_operator_id', true)::uuid
           OR current_setting('app.current_role', true) = 'platform_admin');

-- Per-seat-class fares (FR-FLT-5) — not in DATABASE_DESIGN.md's ERD, which
-- only has trips.base_fare, but FR-FLT-5 explicitly asks for fares "per
-- seat class per trip/route." Seeded from base_fare per distinct
-- seat_type on the bus's seat map at trip-creation time.
CREATE TABLE fleet.trip_fares (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id  UUID NOT NULL REFERENCES fleet.trips (id) ON DELETE CASCADE,
    seat_type TEXT NOT NULL,
    fare     NUMERIC(10,2) NOT NULL,

    UNIQUE (trip_id, seat_type)
);

ALTER TABLE fleet.trip_fares ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.trip_fares FORCE ROW LEVEL SECURITY;

CREATE POLICY trip_fares_insert ON fleet.trip_fares FOR INSERT WITH CHECK (true);

CREATE POLICY trip_fares_select ON fleet.trip_fares FOR SELECT
    USING (EXISTS (
        SELECT 1 FROM fleet.trips t WHERE t.id = trip_fares.trip_id
        AND (t.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE POLICY trip_fares_update ON fleet.trip_fares FOR UPDATE
    USING (EXISTS (
        SELECT 1 FROM fleet.trips t WHERE t.id = trip_fares.trip_id
        AND (t.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ))
    WITH CHECK (true);

CREATE POLICY trip_fares_delete ON fleet.trip_fares FOR DELETE
    USING (EXISTS (
        SELECT 1 FROM fleet.trips t WHERE t.id = trip_fares.trip_id
        AND (t.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

-- Seeded (status='available') at trip creation, one row per seat. The
-- hold/release/confirm state machine on this table is Booking's
-- responsibility (Phase 3) — Phase 2 only creates and reads these rows.
-- booking_id has no FK yet: booking.bookings doesn't exist until Phase 3.
CREATE TABLE fleet.trip_seat_inventory (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id         UUID NOT NULL REFERENCES fleet.trips (id) ON DELETE CASCADE,
    seat_number     TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'available'
                        CHECK (status IN ('available', 'held', 'booked')),
    booking_id      UUID,
    hold_expires_at TIMESTAMPTZ,
    version         INT NOT NULL DEFAULT 0,

    UNIQUE (trip_id, seat_number)
);

ALTER TABLE fleet.trip_seat_inventory ENABLE ROW LEVEL SECURITY;
ALTER TABLE fleet.trip_seat_inventory FORCE ROW LEVEL SECURITY;

CREATE POLICY trip_seat_inventory_insert ON fleet.trip_seat_inventory FOR INSERT WITH CHECK (true);

CREATE POLICY trip_seat_inventory_select ON fleet.trip_seat_inventory FOR SELECT
    USING (EXISTS (
        SELECT 1 FROM fleet.trips t WHERE t.id = trip_seat_inventory.trip_id
        AND (t.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

CREATE POLICY trip_seat_inventory_update ON fleet.trip_seat_inventory FOR UPDATE
    USING (EXISTS (
        SELECT 1 FROM fleet.trips t WHERE t.id = trip_seat_inventory.trip_id
        AND (t.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ))
    WITH CHECK (true);

CREATE POLICY trip_seat_inventory_delete ON fleet.trip_seat_inventory FOR DELETE
    USING (EXISTS (
        SELECT 1 FROM fleet.trips t WHERE t.id = trip_seat_inventory.trip_id
        AND (t.operator_id = current_setting('app.current_operator_id', true)::uuid
             OR current_setting('app.current_role', true) = 'platform_admin')
    ));

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA fleet TO slbus_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA fleet GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO slbus_app;
