// Package cardinality provides a cardinality estimator that supports
// additions and removals.
//
// The estimator counts exactly with an explicit set while cardinality
// is small (sparse mode). Once the number of keys exceeds a threshold
// it converts, exactly once, to an approximate sketch (dense mode) and
// never falls back. In dense mode a deterministic hash maps every key
// to a fixed register and rank; additions increment and removals
// decrement the corresponding counters. When a removal deletes the
// current maximum rank of a register, the register falls back to the
// maximum remaining rank instead of being cleared.
package cardinality

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// Distinguishable failure reasons, all matchable with errors.Is.
var (
	// ErrInvalidPrecision reports a precision outside the allowed range.
	ErrInvalidPrecision = errors.New("cardinality: invalid precision")
	// ErrInvalidThreshold reports an illegal sparse threshold.
	ErrInvalidThreshold = errors.New("cardinality: invalid threshold")
	// ErrEmptyKey reports an empty key.
	ErrEmptyKey = errors.New("cardinality: empty key")
	// ErrKeyNotFound reports the removal of a key that does not exist.
	ErrKeyNotFound = errors.New("cardinality: key not found")
)

const (
	// MinPrecision is the smallest supported precision.
	MinPrecision = 4
	// MaxPrecision is the largest supported precision.
	MaxPrecision = 16
)

// Mode identifies the current counting mode of an Estimator.
type Mode int

const (
	// ModeSparse counts exactly with an explicit set.
	ModeSparse Mode = iota
	// ModeDense estimates with an approximate sketch.
	ModeDense
)

// String returns a human readable mode name.
func (m Mode) String() string {
	switch m {
	case ModeSparse:
		return "sparse"
	case ModeDense:
		return "dense"
	default:
		return "unknown"
	}
}

// Estimator is a cardinality estimator supporting additions and
// removals. It is safe for concurrent use.
//
// In dense mode regs[i] holds the current maximum rank of register i,
// while hists[i] counts how many held keys contribute each rank, so a
// removal can restore the previous maximum instead of clearing the
// register.
type Estimator struct {
	mu        sync.RWMutex
	precision uint8
	threshold int
	mode      Mode
	keys      map[string]struct{}
	regs      []uint8
	hists     []map[uint8]uint32
}

// New creates an Estimator. precision fixes the register count
// (2^precision); threshold is the maximum number of keys kept in
// sparse mode before the one-way conversion to dense mode.
func New(precision uint8, threshold int) (*Estimator, error) {
	if precision < MinPrecision || precision > MaxPrecision {
		return nil, fmt.Errorf("%w: got %d, want %d..%d",
			ErrInvalidPrecision, precision, MinPrecision, MaxPrecision)
	}
	if threshold < 1 {
		return nil, fmt.Errorf("%w: got %d, want >= 1", ErrInvalidThreshold, threshold)
	}
	return &Estimator{
		precision: precision,
		threshold: threshold,
		mode:      ModeSparse,
		keys:      make(map[string]struct{}),
	}, nil
}

// Mode returns the current counting mode.
func (e *Estimator) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// Len returns the exact number of keys currently held.
func (e *Estimator) Len() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.keys)
}

// Add inserts a key. Adding the same key twice is an idempotent no-op.
// An empty key is rejected without touching any state.
func (e *Estimator) Add(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.keys[key]; ok {
		return nil
	}
	e.keys[key] = struct{}{}
	if e.mode == ModeSparse {
		if len(e.keys) > e.threshold {
			e.convertToDenseLocked()
		}
		return nil
	}
	index, rank := locate(hashKey(key), e.precision)
	e.addRankLocked(index, rank)
	return nil
}

// Remove withdraws a key. Removing a key that does not exist fails
// as a whole and leaves the explicit set, registers and estimate
// untouched.
func (e *Estimator) Remove(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.keys[key]; !ok {
		return fmt.Errorf("%w: %q", ErrKeyNotFound, key)
	}
	if e.mode == ModeDense {
		index, rank := locate(hashKey(key), e.precision)
		e.removeRankLocked(index, rank)
	}
	delete(e.keys, key)
	return nil
}

// Estimate returns the current cardinality estimate: exact in sparse
// mode, derived from register ranks via a fixed formula in dense mode.
func (e *Estimator) Estimate() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.mode == ModeSparse {
		return uint64(len(e.keys))
	}
	return estimate(e.regs)
}

// Registers returns a copy of the dense register ranks, or nil in
// sparse mode.
func (e *Estimator) Registers() []uint8 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.mode == ModeSparse {
		return nil
	}
	out := make([]uint8, len(e.regs))
	copy(out, e.regs)
	return out
}

// SelfCheck verifies internal invariants and reports any inconsistency:
// sparse mode must hold no sketch and estimate exactly; dense mode
// registers must equal the maximum rank of their histograms, histogram
// counts must cover every held key, and every key must hash to a rank
// present in its register.
func (e *Estimator) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.mode == ModeSparse {
		if e.regs != nil || e.hists != nil {
			return errors.New("cardinality: sparse mode holds a sketch")
		}
		return nil
	}
	m := len(e.regs)
	if m != 1<<e.precision || len(e.hists) != m {
		return fmt.Errorf("cardinality: sketch size mismatch: regs=%d hists=%d want=%d",
			len(e.regs), len(e.hists), 1<<e.precision)
	}
	var total uint64
	for i := 0; i < m; i++ {
		var want uint8
		for rank, count := range e.hists[i] {
			if count == 0 {
				return fmt.Errorf("cardinality: register %d keeps zero count for rank %d", i, rank)
			}
			if rank > want {
				want = rank
			}
			total += uint64(count)
		}
		if e.regs[i] != want {
			return fmt.Errorf("cardinality: register %d rank=%d, histogram max=%d", i, e.regs[i], want)
		}
	}
	if total != uint64(len(e.keys)) {
		return fmt.Errorf("cardinality: histogram holds %d ranks for %d keys", total, len(e.keys))
	}
	for key := range e.keys {
		index, rank := locate(hashKey(key), e.precision)
		if e.hists[index][rank] == 0 {
			return fmt.Errorf("cardinality: key %q missing rank %d in register %d", key, rank, index)
		}
	}
	return nil
}

// EstimateBatch recomputes an estimate from scratch for a batch of
// keys, for cross-checking an incremental estimator. The batch
// estimator is forced into dense mode on its first key so the result
// is always derived from register ranks.
func EstimateBatch(keys []string, precision uint8) (uint64, error) {
	est, err := New(precision, 1)
	if err != nil {
		return 0, err
	}
	for _, key := range keys {
		if err := est.Add(key); err != nil {
			return 0, err
		}
	}
	return est.Estimate(), nil
}

// convertToDenseLocked builds the sketch from the explicit set. It
// runs exactly once and the estimator never falls back to sparse.
func (e *Estimator) convertToDenseLocked() {
	m := 1 << e.precision
	e.regs = make([]uint8, m)
	e.hists = make([]map[uint8]uint32, m)
	for key := range e.keys {
		index, rank := locate(hashKey(key), e.precision)
		e.addRankLocked(index, rank)
	}
	e.mode = ModeDense
}

// addRankLocked records one key at the given register and rank.
func (e *Estimator) addRankLocked(index uint32, rank uint8) {
	hist := e.hists[index]
	if hist == nil {
		hist = make(map[uint8]uint32, 1)
		e.hists[index] = hist
	}
	hist[rank]++
	if rank > e.regs[index] {
		e.regs[index] = rank
	}
}

// removeRankLocked withdraws one key from the given register. When the
// removed rank was the register maximum, the register falls back to
// the maximum remaining rank, or to zero only when none remain.
func (e *Estimator) removeRankLocked(index uint32, rank uint8) {
	hist := e.hists[index]
	hist[rank]--
	if hist[rank] == 0 {
		delete(hist, rank)
	}
	if e.regs[index] != rank {
		return
	}
	var max uint8
	for r := range hist {
		if r > max {
			max = r
		}
	}
	e.regs[index] = max
}

// estimate applies the fixed HyperLogLog formula with linear counting
// for small ranges. It is a pure function of the register ranks.
func estimate(regs []uint8) uint64 {
	m := len(regs)
	var sum float64
	var zeros int
	for _, rank := range regs {
		sum += math.Ldexp(1, -int(rank))
		if rank == 0 {
			zeros++
		}
	}
	var alpha float64
	switch m {
	case 16:
		alpha = 0.673
	case 32:
		alpha = 0.697
	case 64:
		alpha = 0.709
	default:
		alpha = 0.7213 / (1 + 1.079/float64(m))
	}
	raw := alpha * float64(m) * float64(m) / sum
	if raw <= 2.5*float64(m) && zeros > 0 {
		raw = float64(m) * math.Log(float64(m)/float64(zeros))
	}
	return uint64(math.Round(raw))
}
