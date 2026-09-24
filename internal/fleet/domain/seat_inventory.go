package domain

// SeatInventoryItem is one row of a trip's seat inventory. Phase 2 only
// creates these (status "available", one per seat) at trip creation; the
// hold/held/booked state machine on them is Booking's responsibility
// (Phase 3) — see fleet.trip_seat_inventory's migration comment.
type SeatInventoryItem struct {
	TripID     string
	SeatNumber string
	Status     string
}
