package hotcount

import (
	"math"
	"slices"
	"sync"
)

// Config configures a hot-element sketch.
type Config struct {
	Rows          int
	Width         int
	MaxCandidates int
	MaxElement    uint64
}

// Candidate is one tracked hot element.
type Candidate struct {
	Element  uint64
	Estimate uint64
}

// Sketch is a concurrency-safe approximate hot-element counter.
type Sketch struct {
	mu sync.RWMutex

	rows          int
	width         int
	maxCandidates int
	maxElement    uint64

	// table[r][c] holds the accumulated weight in row r, column c.
	table [][]uint64

	// candidates is always kept in the total order:
	// estimate descending, then element ascending.
	candidates []Candidate
}

// New creates an empty sketch.
func New(cfg Config) (*Sketch, error) {
	if cfg.Rows <= 0 || cfg.Width <= 0 || cfg.MaxCandidates <= 0 {
		return nil, ErrInvalidConfig
	}
	table := make([][]uint64, cfg.Rows)
	for r := range table {
		table[r] = make([]uint64, cfg.Width)
	}
	return &Sketch{
		rows:          cfg.Rows,
		width:         cfg.Width,
		maxCandidates: cfg.MaxCandidates,
		maxElement:    cfg.MaxElement,
		table:         table,
	}, nil
}

// Add records one weighted element arrival.
//
// The whole arrival is rejected, without mutating the sketch or the
// candidate list, when the element is out of range, the count is not
// positive, or any sketched counter would overflow.
func (s *Sketch) Add(element uint64, count uint64) error {
	if element > s.maxElement {
		return ErrElementOutOfRange
	}
	if count == 0 {
		return ErrNonPositiveCount
	}

	cols := make([]int, s.rows)

	s.mu.Lock()
	defer s.mu.Unlock()

	for r := 0; r < s.rows; r++ {
		cols[r] = bucket(element, r, s.width)
		current := s.table[r][cols[r]]
		if math.MaxUint64-current < count {
			return ErrCountOverflow
		}
	}

	for r := 0; r < s.rows; r++ {
		s.table[r][cols[r]] += count
	}

	min := uint64(math.MaxUint64)
	for r := 0; r < s.rows; r++ {
		if v := s.table[r][cols[r]]; v < min {
			min = v
		}
	}
	estimate := min

	if idx := indexOf(s.candidates, element); idx >= 0 {
		s.candidates[idx].Estimate = estimate
		slices.SortFunc(s.candidates, compareCandidate)
		return nil
	}

	if len(s.candidates) < s.maxCandidates {
		s.candidates = append(s.candidates, Candidate{Element: element, Estimate: estimate})
		slices.SortFunc(s.candidates, compareCandidate)
		return nil
	}

	worst := s.candidates[len(s.candidates)-1]
	if betterCandidate(Candidate{Element: element, Estimate: estimate}, worst) {
		s.candidates[len(s.candidates)-1] = Candidate{Element: element, Estimate: estimate}
		slices.SortFunc(s.candidates, compareCandidate)
	}
	return nil
}

// Estimate returns the overestimated frequency of an element.
func (s *Sketch) Estimate(element uint64) (uint64, error) {
	if element > s.maxElement {
		return 0, ErrElementOutOfRange
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	cols := make([]int, s.rows)
	for r := 0; r < s.rows; r++ {
		cols[r] = bucket(element, r, s.width)
	}
	min := uint64(math.MaxUint64)
	for r := 0; r < s.rows; r++ {
		if v := s.table[r][cols[r]]; v < min {
			min = v
		}
	}
	return min, nil
}

// Candidates returns the current ordered candidate list.
func (s *Sketch) Candidates() []Candidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.candidates)
}

// SelfCheck verifies internal invariants.
func (s *Sketch) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.table) != s.rows {
		return ErrSelfCheck
	}
	for _, row := range s.table {
		if len(row) != s.width {
			return ErrSelfCheck
		}
	}
	if len(s.candidates) > s.maxCandidates {
		return ErrSelfCheck
	}
	seen := make(map[uint64]struct{}, len(s.candidates))
	for i, c := range s.candidates {
		if c.Element > s.maxElement {
			return ErrSelfCheck
		}
		if _, dup := seen[c.Element]; dup {
			return ErrSelfCheck
		}
		seen[c.Element] = struct{}{}

		// A candidate is only refreshed when its own element arrives, so its
		// stored estimate may lag the live one when colliders inflate cells.
		// It must never exceed the live estimate, which is monotonic.
		live := s.estimateLocked(c.Element)
		if c.Estimate > live {
			return ErrSelfCheck
		}
		if i > 0 && compareCandidate(s.candidates[i-1], c) > 0 {
			return ErrSelfCheck
		}
	}
	return nil
}

func (s *Sketch) estimateLocked(element uint64) uint64 {
	min := uint64(math.MaxUint64)
	for r := 0; r < s.rows; r++ {
		if v := s.table[r][bucket(element, r, s.width)]; v < min {
			min = v
		}
	}
	return min
}

func compareCandidate(a, b Candidate) int {
	switch {
	case a.Estimate > b.Estimate:
		return -1
	case a.Estimate < b.Estimate:
		return 1
	case a.Element < b.Element:
		return -1
	case a.Element > b.Element:
		return 1
	default:
		return 0
	}
}

func betterCandidate(a, b Candidate) bool {
	return compareCandidate(a, b) < 0
}

func indexOf(list []Candidate, element uint64) int {
	for i, c := range list {
		if c.Element == element {
			return i
		}
	}
	return -1
}
