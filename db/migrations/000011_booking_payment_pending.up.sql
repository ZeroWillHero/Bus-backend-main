-- Booking confirm integrity: 'payment_pending' marks a booking whose charge is
-- in flight. Moving held -> payment_pending is a conditional UPDATE, so only one
-- concurrent confirm can ever reach the payment gateway for a booking.
ALTER TABLE booking.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE booking.bookings ADD CONSTRAINT bookings_status_check
    CHECK (status IN ('held','payment_pending','confirmed','cancelled','paid_but_expired'));
