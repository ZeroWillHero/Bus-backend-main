-- A booking caught mid-charge goes back to 'held' (its pre-charge state) so the
-- old constraint can be restored. bookings has FORCE ROW LEVEL SECURITY, so the
-- UPDATE runs as platform_admin; otherwise a non-superuser owner would silently
-- update nothing and the ADD CONSTRAINT below would fail.
BEGIN;
SELECT set_config('app.current_role', 'platform_admin', true),
       set_config('app.current_user_id', '00000000-0000-0000-0000-000000000000', true),
       set_config('app.current_operator_id', '00000000-0000-0000-0000-000000000000', true);
UPDATE booking.bookings SET status = 'held', updated_at = now() WHERE status = 'payment_pending';
ALTER TABLE booking.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE booking.bookings ADD CONSTRAINT bookings_status_check
    CHECK (status IN ('held','confirmed','cancelled','paid_but_expired'));
COMMIT;
