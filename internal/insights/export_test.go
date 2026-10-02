package insights

import "time"

// SetNow pins the clock; tests only.
func SetNow(s *Service, now func() time.Time) { s.now = now }
