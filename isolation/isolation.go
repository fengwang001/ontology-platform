// Package isolation implements a batch-write isolator with bisection-based
// poison-pill detection and a bounded known-poison table.
//
// The isolator writes batches of record ids to an injected sink
// (WriteFunc) that succeeds or fails atomically for the whole batch.
// Errors matching errors.Is(err, ErrTransient) are transient and retried
// up to R times; any other non-nil error is permanent. When a batch
// fails permanently, the isolator bisects it (left half takes
// ceil(n/2) ids) to locate poison-pill records, delivering everything
// else in original order exactly once. A budget Cmax caps the number of
// sink calls per Submit; known poisons are remembered in a FIFO
// evicting table of capacity Km.
package isolation

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrTransient classifies a sink error as transient. Any non-nil sink
// error that does not match ErrTransient (via errors.Is) is permanent.
var ErrTransient = errors.New("isolation: transient sink error")

// ErrInvalidArgument is returned when constructor parameters or Submit
// arguments are out of range or malformed.
var ErrInvalidArgument = errors.New("isolation: invalid argument")

// ErrClosed is returned by Submit after Close has been called.
var ErrClosed = errors.New("isolation: isolator is closed")

const (
	maxR        = 10
	maxKm       = 1000
	maxCmax     = 1_000_000
	maxBatchLen = 100_000
)

// Reason explains why a record was dead-lettered.
type Reason int

const (
	// ReasonKnown: the id was already in the known-poison table.
	ReasonKnown Reason = iota
	// ReasonPoison: the id was confirmed (or inferred) to be a poison pill.
	ReasonPoison
	// ReasonExhausted: a singleton batch kept failing transiently after
	// R+1 attempts.
	ReasonExhausted
	// ReasonBudget: the per-Submit sink-call budget was exhausted before
	// this record could be adjudicated.
	ReasonBudget
)

func (r Reason) String() string {
	switch r {
	case ReasonKnown:
		return "Known"
	case ReasonPoison:
		return "Poison"
	case ReasonExhausted:
		return "Exhausted"
	case ReasonBudget:
		return "Budget"
	default:
		return fmt.Sprintf("Reason(%d)", int(r))
	}
}

// DeadLetter is a record that was not delivered, with its reason.
type DeadLetter struct {
	ID     string
	Reason Reason
}

// Result is the outcome of one Submit call.
type Result struct {
	// Delivered lists ids delivered to the sink, in original order.
	Delivered []string
	// Dead lists dead-lettered ids with reasons, in original order.
	Dead []DeadLetter
	// Calls is the total number of sink calls made by this Submit.
	Calls int
}

// WriteFunc writes a batch of ids atomically: it either succeeds for the
// whole batch or fails with an error.
type WriteFunc func(ids []string) error

// Isolator is a batch-write isolator. It is safe for concurrent use.
type Isolator struct {
	sink WriteFunc
	r    int
	km   int
	cmax int

	mu     sync.Mutex
	known  map[string]struct{}
	order  []string // known-table ids in append order (for FIFO eviction)
	closed bool
	wg     sync.WaitGroup
}

// New constructs an Isolator. r is the number of transient retries
// (0..10), km the known-poison table capacity (0..1000), cmax the
// per-Submit sink-call budget (1..1e6). Out-of-range parameters or a nil
// sink are rejected with ErrInvalidArgument.
func New(r, km, cmax int, sink WriteFunc) (*Isolator, error) {
	if r < 0 || r > maxR || km < 0 || km > maxKm ||
		cmax < 1 || cmax > maxCmax || sink == nil {
		return nil, fmt.Errorf("%w: r=%d km=%d cmax=%d sinkNil=%t",
			ErrInvalidArgument, r, km, cmax, sink == nil)
	}
	return &Isolator{
		sink:  sink,
		r:     r,
		km:    km,
		cmax:  cmax,
		known: make(map[string]struct{}),
	}, nil
}

// Submit processes one batch of 1..1e5 distinct non-empty ids. It
// reports the first rejection reason in the order: invalid argument,
// then closed. Rejected calls never touch the sink or the known table.
func (iso *Isolator) Submit(ids []string) (Result, error) {
	if err := validateIDs(ids); err != nil {
		return Result{}, err
	}

	iso.mu.Lock()
	if iso.closed {
		iso.mu.Unlock()
		return Result{}, ErrClosed
	}
	iso.wg.Add(1)
	// The known table is read once, at Submit start; entries added by
	// concurrent Submits are invisible to this one.
	snapshot := make(map[string]struct{}, len(iso.known))
	for id := range iso.known {
		snapshot[id] = struct{}{}
	}
	iso.mu.Unlock()
	defer iso.wg.Done()

	origIdx := make(map[string]int, len(ids))
	batch := make([]string, 0, len(ids))
	var dead []indexedDead
	for i, id := range ids {
		origIdx[id] = i
		if _, ok := snapshot[id]; ok {
			dead = append(dead, indexedDead{i, DeadLetter{id, ReasonKnown}})
		} else {
			batch = append(batch, id)
		}
	}

	s := &session{sink: iso.sink, r: iso.r, cmax: iso.cmax}
	if len(batch) > 0 {
		s.solve(batch, false)
	}

	for _, d := range s.dead {
		dead = append(dead, indexedDead{origIdx[d.ID], d})
	}
	sort.SliceStable(dead, func(i, j int) bool { return dead[i].idx < dead[j].idx })
	sort.SliceStable(s.delivered, func(i, j int) bool {
		return origIdx[s.delivered[i]] < origIdx[s.delivered[j]]
	})

	res := Result{Delivered: s.delivered, Calls: s.calls}
	res.Dead = make([]DeadLetter, len(dead))
	for i, d := range dead {
		res.Dead[i] = d.dl
	}

	// Merge newly confirmed poisons at Submit end, in determination
	// order; concurrent Submits do not see them until then.
	iso.mu.Lock()
	for _, id := range s.newPoisons {
		iso.recordLocked(id)
	}
	iso.mu.Unlock()
	return res, nil
}

// Known returns the current known-poison table in append order.
func (iso *Isolator) Known() []string {
	iso.mu.Lock()
	defer iso.mu.Unlock()
	out := make([]string, len(iso.order))
	copy(out, iso.order)
	return out
}

// Close marks the isolator closed, waits for in-flight Submits to
// finish, and is idempotent.
func (iso *Isolator) Close() error {
	iso.mu.Lock()
	if iso.closed {
		iso.mu.Unlock()
		return nil
	}
	iso.closed = true
	iso.mu.Unlock()
	iso.wg.Wait()
	return nil
}

// recordLocked appends id to the known table, evicting the oldest entry
// when full. Existing entries keep their position. Km == 0 disables
// recording. Caller must hold iso.mu.
func (iso *Isolator) recordLocked(id string) {
	if iso.km == 0 {
		return
	}
	if _, ok := iso.known[id]; ok {
		return
	}
	for len(iso.order) >= iso.km {
		delete(iso.known, iso.order[0])
		iso.order = iso.order[1:]
	}
	iso.known[id] = struct{}{}
	iso.order = append(iso.order, id)
}

func validateIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > maxBatchLen {
		return fmt.Errorf("%w: ids length %d outside [1, %d]",
			ErrInvalidArgument, len(ids), maxBatchLen)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("%w: empty id", ErrInvalidArgument)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("%w: duplicate id %q", ErrInvalidArgument, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

type indexedDead struct {
	idx int
	dl  DeadLetter
}

// session carries the per-Submit state: the non-exported calls counter,
// the budget, and the accumulated verdicts.
type session struct {
	sink  WriteFunc
	r     int
	cmax  int
	calls int

	budgetStop bool
	delivered  []string
	dead       []DeadLetter
	newPoisons []string
}

// solve adjudicates batch. certain=true means the batch is already known
// to contain a poison (parent failed permanently and its left half
// succeeded), so no whole-batch sink call is made. It reports whether
// the whole batch was delivered.
func (s *session) solve(batch []string, certain bool) bool {
	if len(batch) == 0 {
		return true
	}
	if s.budgetStop {
		s.deadBudget(batch)
		return false
	}
	perm := certain
	if !certain {
		ok, p := s.tryBatch(batch)
		if ok {
			s.delivered = append(s.delivered, batch...)
			return true
		}
		perm = p
		if s.budgetStop {
			s.deadBudget(batch)
			return false
		}
	}
	if len(batch) == 1 {
		if perm {
			s.dead = append(s.dead, DeadLetter{batch[0], ReasonPoison})
			s.newPoisons = append(s.newPoisons, batch[0])
		} else {
			s.dead = append(s.dead, DeadLetter{batch[0], ReasonExhausted})
		}
		return false
	}
	mid := (len(batch) + 1) / 2 // left half takes ceil(n/2)
	lok := s.solve(batch[:mid], false)
	s.solve(batch[mid:], perm && lok)
	return false
}

// tryBatch calls the sink up to r+1 times. It reports success, and on
// failure whether the failure is permanent. A permanent error stops
// retries immediately; r+1 transient errors mean perm=false.
func (s *session) tryBatch(batch []string) (ok, perm bool) {
	for attempt := 0; attempt <= s.r; attempt++ {
		if s.calls >= s.cmax {
			s.budgetStop = true
			return false, false
		}
		s.calls++
		cp := make([]string, len(batch))
		copy(cp, batch)
		err := s.sink(cp)
		if err == nil {
			return true, false
		}
		if !errors.Is(err, ErrTransient) {
			return false, true
		}
	}
	return false, false
}

func (s *session) deadBudget(batch []string) {
	for _, id := range batch {
		s.dead = append(s.dead, DeadLetter{id, ReasonBudget})
	}
}
