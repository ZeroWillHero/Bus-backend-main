package domain

import "time"

// RecurrenceRule describes a batch of trip occurrences (FR-FLT-4: one-off
// or recurring daily/weekly schedules). Weekdays is a set of
// time.Weekday values the trip runs on; an empty set means every day
// (daily) in the range.
type RecurrenceRule struct {
	TimeOfDay time.Duration // offset from midnight, e.g. 8*time.Hour for 08:00
	Weekdays  map[time.Weekday]bool
	StartDate time.Time
	EndDate   time.Time
}

// MaxOccurrences guards against a pathological date range accidentally
// generating an unbounded batch of trips (e.g. a multi-year range).
const MaxOccurrences = 366

// GenerateOccurrences returns one departure timestamp per matching day in
// [StartDate, EndDate], inclusive, in the rule's location. Pure and
// unit-testable without a database.
func GenerateOccurrences(rule RecurrenceRule) []time.Time {
	var out []time.Time
	loc := rule.StartDate.Location()
	day := time.Date(rule.StartDate.Year(), rule.StartDate.Month(), rule.StartDate.Day(), 0, 0, 0, 0, loc)
	end := time.Date(rule.EndDate.Year(), rule.EndDate.Month(), rule.EndDate.Day(), 0, 0, 0, 0, loc)

	for !day.After(end) && len(out) < MaxOccurrences {
		if len(rule.Weekdays) == 0 || rule.Weekdays[day.Weekday()] {
			out = append(out, day.Add(rule.TimeOfDay))
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}
