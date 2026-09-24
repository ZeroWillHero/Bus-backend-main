package domain

import (
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestGenerateOccurrences_Daily(t *testing.T) {
	rule := RecurrenceRule{
		TimeOfDay: 8 * time.Hour,
		StartDate: date(2026, time.January, 1),
		EndDate:   date(2026, time.January, 5),
	}
	got := GenerateOccurrences(rule)
	if len(got) != 5 {
		t.Fatalf("expected 5 daily occurrences, got %d", len(got))
	}
	if !got[0].Equal(date(2026, time.January, 1).Add(8 * time.Hour)) {
		t.Errorf("first occurrence = %v, want Jan 1 08:00", got[0])
	}
	if !got[4].Equal(date(2026, time.January, 5).Add(8 * time.Hour)) {
		t.Errorf("last occurrence = %v, want Jan 5 08:00", got[4])
	}
}

func TestGenerateOccurrences_WeeklyOnSelectedWeekdays(t *testing.T) {
	// Jan 1 2026 is a Thursday. Select only Mon/Wed over two weeks.
	rule := RecurrenceRule{
		TimeOfDay: 6 * time.Hour,
		Weekdays:  map[time.Weekday]bool{time.Monday: true, time.Wednesday: true},
		StartDate: date(2026, time.January, 1),
		EndDate:   date(2026, time.January, 14),
	}
	got := GenerateOccurrences(rule)
	for _, occ := range got {
		if occ.Weekday() != time.Monday && occ.Weekday() != time.Wednesday {
			t.Errorf("occurrence %v falls on %v, want Monday or Wednesday", occ, occ.Weekday())
		}
	}
	// Jan 5 (Mon), Jan 7 (Wed), Jan 12 (Mon), Jan 14 (Wed) = 4 occurrences.
	if len(got) != 4 {
		t.Fatalf("expected 4 occurrences, got %d: %v", len(got), got)
	}
}

func TestGenerateOccurrences_EndBeforeStart(t *testing.T) {
	rule := RecurrenceRule{
		StartDate: date(2026, time.January, 5),
		EndDate:   date(2026, time.January, 1),
	}
	got := GenerateOccurrences(rule)
	if len(got) != 0 {
		t.Errorf("expected 0 occurrences when end precedes start, got %d", len(got))
	}
}

func TestGenerateOccurrences_SingleDay(t *testing.T) {
	rule := RecurrenceRule{
		StartDate: date(2026, time.January, 1),
		EndDate:   date(2026, time.January, 1),
	}
	got := GenerateOccurrences(rule)
	if len(got) != 1 {
		t.Errorf("expected exactly 1 occurrence for a single-day range, got %d", len(got))
	}
}

func TestGenerateOccurrences_CapsAtMaxOccurrences(t *testing.T) {
	rule := RecurrenceRule{
		StartDate: date(2020, time.January, 1),
		EndDate:   date(2030, time.January, 1),
	}
	got := GenerateOccurrences(rule)
	if len(got) != MaxOccurrences {
		t.Errorf("expected generation to cap at %d, got %d", MaxOccurrences, len(got))
	}
}
