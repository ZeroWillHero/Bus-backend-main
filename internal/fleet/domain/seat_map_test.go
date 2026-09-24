package domain

import "testing"

func TestValidateSeats(t *testing.T) {
	cases := []struct {
		name    string
		seats   []Seat
		wantErr error
	}{
		{"empty", nil, ErrEmptySeatMap},
		{
			"duplicate seat number",
			[]Seat{{Number: "1A", Row: 0, Col: 0}, {Number: "1A", Row: 0, Col: 1}},
			ErrDuplicateSeatNumber,
		},
		{
			"empty seat number",
			[]Seat{{Number: "", Row: 0, Col: 0}},
			ErrInvalidSeatMap,
		},
		{
			"negative row",
			[]Seat{{Number: "1A", Row: -1, Col: 0}},
			ErrInvalidSeatMap,
		},
		{
			"valid irregular layout",
			[]Seat{
				{Number: "1A", Row: 0, Col: 0},
				{Number: "1B", Row: 0, Col: 1},
				// Row 1 has 3 seats (wider back row) — irregular but valid.
				{Number: "2A", Row: 1, Col: 0},
				{Number: "2B", Row: 1, Col: 1},
				{Number: "2C", Row: 1, Col: 2},
			},
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSeats(c.seats)
			if err != c.wantErr {
				t.Errorf("ValidateSeats() = %v, want %v", err, c.wantErr)
			}
		})
	}
}
