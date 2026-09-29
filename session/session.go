// Package session implements keyed session-window splitting over a stream of
// timestamped events that may arrive out of order.
package session

import "errors"

// Sentinel errors giving distinguishable rejection reasons.
var (
	ErrNonPositiveGap  = errors.New("session: gap must be positive")
	ErrEmptyKey        = errors.New("session: event key must not be empty")
	ErrTooManyKeys     = errors.New("session: distinct key count exceeds capacity")
	ErrNilSplitter     = errors.New("session: splitter is nil")
)

// Event is a single timestamped observation for a key. Timestamp is an
// arbitrary monotonic value (milliseconds, nanoseconds, ...); only ordering
// and differences are meaningful.
type Event struct {
	Key       string
	Timestamp int64
}

// Session is one connected component of events for a key: every event
// timestamp lies in [Start, End] and consecutive sorted timestamps differ by
// at most the configured gap.
type Session struct {
	Key        string
	Start      int64
	End        int64
	EventCount int
}

// Splitter accumulates events per key and answers session queries. Its zero
// value is not usable; construct one with New.
type Splitter struct{}

// New creates a Splitter with the given inclusive connection gap and the
// maximum number of distinct keys it is allowed to hold.
func New(gap int64, maxKeys int) (*Splitter, error) {
	return nil, nil
}

// Add inserts one event, atomically rejecting the whole call on invalid input.
func (s *Splitter) Add(e Event) error {
	return nil
}

// AddBatch inserts multiple events as one transaction: on any error no state
// changes. Events at the same (key, timestamp) are deduplicated.
func (s *Splitter) AddBatch(events []Event) error {
	return nil
}

// Sessions returns the sessions of one key ordered by start time.
func (s *Splitter) Sessions(key string) ([]Session, error) {
	return nil, nil
}

// AllSessions returns every session grouped by key; keys and sessions are
// ordered deterministically.
func (s *Splitter) AllSessions() (map[string][]Session, error) {
	return nil, nil
}

// KeyCount and Len support self-checks and monitoring.
func (s *Splitter) KeyCount() int { return 0 }

func (s *Splitter) Len(key string) int { return 0 }
