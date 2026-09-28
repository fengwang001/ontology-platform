// Package sampler implements weighted sampling without replacement from an
// element stream, using the A-Res (Efraimidis-Spirakis) key scheme.
package sampler

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"sort"
	"sync"
)

// Element is one weighted item offered to the sampler.
type Element struct {
	ID     string
	Weight float64
}

// Selected is a sampled element together with the bookkeeping of its draw.
type Selected struct {
	ID     string
	Weight float64
	Key    float64
	Seq    int
}

// RandomSource is the caller-injected deterministic stream of uniform values
// in the open interval (0, 1). Peek must not move the cursor; Advance moves it
// exactly once, so a sampler can reject an element after peeking without
// consuming the value.
type RandomSource interface {
	Peek() (float64, error)
	Advance()
	Consumed() int
	Remaining() int
}

// cursorRestorer is implemented by sources that can snapshot and restore their
// cursor. It is used to roll OfferAll back without leaving any trace.
type cursorRestorer interface {
	snapshotCursor() int
	restoreCursor(int)
}

// Sampler draws at most K accepted elements from a weighted stream. All methods
// are safe for concurrent use by multiple goroutines; every accepted element
// consumes exactly one value from the injected source and rejected elements
// consume none.
type Sampler struct {
	mu       sync.Mutex
	k        int
	src      RandomSource
	heap     minHeap
	seen     map[string]struct{}
	accepted int
	log      *slog.Logger
}

var (
	// ErrInvalidSampleSize is returned when the requested sample count is < 1.
	ErrInvalidSampleSize = errors.New("sampler: invalid sample size")
	// ErrEmptyID is returned when an element carries an empty identifier.
	ErrEmptyID = errors.New("sampler: empty element id")
	// ErrZeroWeight is returned when an element has weight zero.
	ErrZeroWeight = errors.New("sampler: zero weight")
	// ErrNegativeWeight is returned when an element has non-finite or negative weight.
	ErrNegativeWeight = errors.New("sampler: invalid weight")
	// ErrDuplicateID is returned when an identifier was accepted before.
	ErrDuplicateID = errors.New("sampler: duplicate element id")
	// ErrInvalidRandom is returned when a drawn value is not in (0, 1).
	ErrInvalidRandom = errors.New("sampler: invalid random value")
	// ErrRandomExhausted is returned when the deterministic source has no value.
	ErrRandomExhausted = errors.New("sampler: random source exhausted")
	// ErrNilRandomSource is returned when New gets a nil source.
	ErrNilRandomSource = errors.New("sampler: nil random source")
)

// Option customizes a Sampler.
type Option func(*Sampler)

// WithLogWriter redirects the per-step decision log (input, key, rationale).
func WithLogWriter(w io.Writer) Option {
	return func(s *Sampler) {
		if w != nil {
			s.log = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
		}
	}
}

// New constructs an empty sampler for exactly k samples.
func New(k int, src RandomSource, opts ...Option) (*Sampler, error) {
	if k < 1 {
		return nil, ErrInvalidSampleSize
	}
	if src == nil {
		return nil, ErrNilRandomSource
	}
	s := &Sampler{
		k:    k,
		src:  src,
		seen: make(map[string]struct{}),
		log:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Offer feeds one element; a rejected element never consumes a random value.
func (s *Sampler) Offer(e Element) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offerLocked(e)
}

// OfferAll feeds a sequence atomically: any rejection rolls every step back.
func (s *Sampler) OfferAll(es []Element) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	savedHeap := append(minHeap(nil), s.heap...)
	savedSeen := make(map[string]struct{}, len(s.seen))
	for id := range s.seen {
		savedSeen[id] = struct{}{}
	}
	savedAccepted := s.accepted
	var savedCursor int
	restorer, restorable := s.src.(cursorRestorer)
	if restorable {
		savedCursor = restorer.snapshotCursor()
	}

	for _, e := range es {
		if err := s.offerLocked(e); err != nil {
			s.heap = savedHeap
			s.seen = savedSeen
			s.accepted = savedAccepted
			if restorable {
				restorer.restoreCursor(savedCursor)
			}
			s.log.Error("batch rejected; all steps rolled back",
				"id", e.ID, "weight", e.Weight, "reason", err.Error())
			return err
		}
	}
	return nil
}

// Sample returns the current top-k elements ordered by descending key.
func (s *Sampler) Sample() []Selected {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Selected, len(s.heap))
	for i, e := range s.heap {
		out[i] = Selected{ID: e.id, Weight: e.weight, Key: e.key, Seq: e.seq}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key > out[j].Key
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// Consumed reports how many random values accepted elements have drawn.
func (s *Sampler) Consumed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src.Consumed()
}

// Remaining reports how many values are still available in the source.
func (s *Sampler) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src.Remaining()
}

// K returns the configured sample count.
func (s *Sampler) K() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.k
}

// SelfCheck verifies the sampler's internal invariants.
func (s *Sampler) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.heap) > s.k {
		return errors.New("sampler: reservoir exceeds sample size")
	}
	if s.accepted != s.src.Consumed() {
		return errors.New("sampler: consumed random values differ from accepted count")
	}
	inHeap := make(map[string]struct{}, len(s.heap))
	for i, e := range s.heap {
		if !math.IsFinite(e.key) {
			return errors.New("sampler: non-finite key in reservoir")
		}
		if _, dup := inHeap[e.id]; dup {
			return errors.New("sampler: duplicate id in reservoir")
		}
		inHeap[e.id] = struct{}{}
		if _, ok := s.seen[e.id]; !ok {
			return errors.New("sampler: reservoir id missing from seen set")
		}
		if parent := (i - 1) / 2; i > 0 && worse(s.heap[parent], s.heap[i]) {
			return errors.New("sampler: min-heap invariant violated")
		}
	}
	return nil
}

func (s *Sampler) offerLocked(e Element) error {
	if e.ID == "" {
		s.log.Warn("element rejected", "id", e.ID, "weight", e.Weight,
			"reason", ErrEmptyID.Error())
		return ErrEmptyID
	}
	if math.IsNaN(e.Weight) || math.IsInf(e.Weight, 0) || e.Weight < 0 {
		s.log.Warn("element rejected", "id", e.ID, "weight", e.Weight,
			"reason", ErrNegativeWeight.Error())
		return ErrNegativeWeight
	}
	if e.Weight == 0 {
		s.log.Warn("element rejected", "id", e.ID, "weight", e.Weight,
			"reason", ErrZeroWeight.Error())
		return ErrZeroWeight
	}
	if _, dup := s.seen[e.ID]; dup {
		s.log.Warn("element rejected", "id", e.ID, "weight", e.Weight,
			"reason", ErrDuplicateID.Error())
		return ErrDuplicateID
	}

	u, err := s.src.Peek()
	if err != nil {
		s.log.Warn("element rejected", "id", e.ID, "weight", e.Weight,
			"reason", err.Error())
		return err
	}
	if !validUniform(u) {
		s.log.Warn("element rejected", "id", e.ID, "weight", e.Weight, "u", u,
			"reason", ErrInvalidRandom.Error())
		return ErrInvalidRandom
	}

	candidate := entry{id: e.ID, weight: e.Weight, key: key(u, e.Weight), seq: s.accepted}

	if len(s.heap) < s.k {
		s.src.Advance()
		s.heap.push(candidate)
		s.seen[e.ID] = struct{}{}
		s.accepted++
		s.log.Info("element accepted", "id", e.ID, "weight", e.Weight, "u", u,
			"key", candidate.key, "seq", candidate.seq, "decision", "fill")
		return nil
	}

	worst := s.heap[0]
	if !worse(worst, candidate) {
		// Equal keys never replace: the earlier arrival keeps its seat.
		s.log.Info("element rejected", "id", e.ID, "weight", e.Weight, "u", u,
			"key", candidate.key, "seq", candidate.seq, "decision", "below_min",
			"min_key", worst.key, "min_id", worst.id, "consumed_random", false)
		return nil
	}

	s.src.Advance()
	s.heap.replaceRoot(candidate)
	s.seen[e.ID] = struct{}{}
	s.accepted++
	s.log.Info("element accepted", "id", e.ID, "weight", e.Weight, "u", u,
		"key", candidate.key, "seq", candidate.seq, "decision", "replace",
		"evicted_id", worst.id, "evicted_key", worst.key)
	return nil
}
