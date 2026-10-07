package export

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// recSink is an idempotent consumer: repeated handoffs of the same Write.ID
// after an unconfirmed restart may reach it but appear once in Emitted order.
type recSink struct {
	mu      sync.Mutex
	emitted []Write
	seen    map[string]int
	failAt  map[string]bool
	failed  map[string]bool
}

func newRecSink() *recSink {
	return &recSink{seen: map[string]int{}, failAt: map[string]bool{}, failed: map[string]bool{}}
}

func (s *recSink) Output(w Write) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt[w.ID] && !s.failed[w.ID] {
		s.failed[w.ID] = true
		return errors.New("disk full")
	}
	if _, ok := s.seen[w.ID]; ok {
		return nil
	}
	s.seen[w.ID] = len(s.emitted)
	s.emitted = append(s.emitted, w)
	return nil
}

func (s *recSink) Emitted() []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Write, len(s.emitted))
	copy(out, s.emitted)
	return out
}

func ids(ws []Write) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}
	return out
}

func idsStr(ws []Write) string { return fmt.Sprint(ids(ws)) }

func errKind(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func acceptAll(tb testing.TB, c *Cycle, ws ...Write) {
	tb.Helper()
	for _, w := range ws {
		if _, err := c.Accept(context.Background(), w); err != nil {
			tb.Fatalf("accept %v: %v", w, err)
		}
	}
}

func mustBegin(tb testing.TB, m *Manager, link string, start, end Position) *Cycle {
	tb.Helper()
	c, err := m.Begin(context.Background(), link, Range{start, end})
	if err != nil {
		tb.Fatalf("begin (%d,%d] on %s: %v", start, end, link, err)
	}
	return c
}

func commitCycle(tb testing.TB, m *Manager, c *Cycle, sink Sink) {
	tb.Helper()
	if _, err := c.Deliver(context.Background(), sink); err != nil {
		tb.Fatalf("deliver: %v", err)
	}
	if err := c.Confirm(); err != nil {
		tb.Fatalf("confirm: %v", err)
	}
}
