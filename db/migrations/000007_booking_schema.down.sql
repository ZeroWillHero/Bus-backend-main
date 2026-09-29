-- Rollback Phase 3: Booking schema
DROP TABLE IF EXISTS booking.processed_webhook_events;
DROP TABLE IF EXISTS booking.tickets;
DROP TABLE IF EXISTS booking.booking_seats;
DROP TABLE IF EXISTS booking.bookings;
DROP SCHEMA IF EXISTS booking CASCADE;

-- Rollback outbox (only if this migration created it; if shared with future schemas, coord with team)
DROP TABLE IF EXISTS platform.outbox_events;
