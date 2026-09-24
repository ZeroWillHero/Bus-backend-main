package repository

import "testing"

// TestNumericRoundTrip guards against a regression this exact bug hit
// during development: pgtype.Numeric.Scan only accepts a string, not a
// raw float64 — passing one directly fails and, if the error is
// discarded, silently leaves the value NULL in the database (caught via
// a real API smoke test: distance_km/base_fare/fare all stored as NULL
// despite non-zero request values).
func TestNumericRoundTrip(t *testing.T) {
	cases := []float64{0, 1, 115.5, 2400, 99999.99, 0.01}
	for _, f := range cases {
		n := numeric(f)
		if !n.Valid {
			t.Errorf("numeric(%v) produced an invalid pgtype.Numeric", f)
			continue
		}
		got := numericToFloat64(n)
		if got != f {
			t.Errorf("numeric round trip: numeric(%v) -> numericToFloat64() = %v", f, got)
		}
	}
}

func TestNumericPtrNilStaysInvalid(t *testing.T) {
	n := numericPtr(nil)
	if n.Valid {
		t.Errorf("expected numericPtr(nil) to produce an invalid (NULL) pgtype.Numeric")
	}
	if got := numericToFloat64Ptr(n); got != nil {
		t.Errorf("expected numericToFloat64Ptr of an invalid Numeric to be nil, got %v", *got)
	}
}
