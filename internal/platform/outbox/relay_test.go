package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/sl-bus/backend/internal/platform/outbox"
)

type fakeSource struct {
	events    []map[string]interface{}
	processed map[string]bool
}

func (f *fakeSource) PollUnprocessed(context.Context, int) ([]map[string]interface{}, error) {
	return f.events, nil
}
func (f *fakeSource) MarkProcessed(_ context.Context, id string) error {
	f.processed[id] = true
	return nil
}

func ev(id, typ string, payload map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": id, "event_type": typ, "payload": payload}
}

func newRelay(src *fakeSource, h map[string]outbox.Handler) *outbox.Relay {
	return &outbox.Relay{Source: src, Handlers: h, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestRelayContainsFailuresPerEvent: a failing handler, a panicking handler and
// a malformed event must each be isolated; healthy events in the same batch
// still process, and only successes are marked processed.
func TestRelayContainsFailuresPerEvent(t *testing.T) {
	src := &fakeSource{processed: map[string]bool{}, events: []map[string]interface{}{
		ev("ok", "Good", map[string]interface{}{}),
		ev("err", "Fails", map[string]interface{}{}),
		ev("panic", "Panics", map[string]interface{}{}),
		{"id": "bad", "event_type": "Good"}, // no payload
		ev("after", "Good", map[string]interface{}{}),
		ev("unknown", "Mystery", map[string]interface{}{}),
	}}
	relay := newRelay(src, map[string]outbox.Handler{
		"Good":   func(context.Context, map[string]interface{}) error { return nil },
		"Fails":  func(context.Context, map[string]interface{}) error { return errors.New("boom") },
		"Panics": func(context.Context, map[string]interface{}) error { var m map[string]int; m["x"] = 1; return nil },
	})

	st, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Processed != 2 || st.Failed != 3 || st.Skipped != 1 {
		t.Errorf("stats = %+v, want Processed=2 Failed=3 Skipped=1", st)
	}
	for _, id := range []string{"ok", "after", "unknown"} {
		if !src.processed[id] {
			t.Errorf("%s should be marked processed", id)
		}
	}
	for _, id := range []string{"err", "panic", "bad"} {
		if src.processed[id] {
			t.Errorf("%s must NOT be marked processed (it must be retried)", id)
		}
	}
}
