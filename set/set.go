package set

import (
	"errors"
	"math"
	"sort"
	"sync"

	"ontology/iv"
)

var (
	ErrInvalid      = iv.ErrInvalid
	ErrOverflow     = iv.ErrOverflow
	ErrTooManySpans = errors.New("too many spans")
)

type Op struct {
	Lo, Hi int64
	Remove bool
}

type Set struct {
	mu         sync.RWMutex
	maxSpans   int
	spans      []iv.Interval
	probeCount int
}

func New(maxSpans int) *Set {
	return &Set{maxSpans: maxSpans}
}

func (s *Set) ProbeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.probeCount
}

func (s *Set) AddPoint(x int64) error {
	if x == math.MaxInt64 {
		return ErrOverflow
	}
	return s.Add(x, x+1)
}

func (s *Set) Add(lo, hi int64) error {
	in, err := iv.New(lo, hi)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Empty() {
		return nil
	}
	return s.applyLocked(in, false)
}

func (s *Set) Remove(lo, hi int64) error {
	in, err := iv.New(lo, hi)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Empty() {
		return nil
	}
	return s.applyLocked(in, true)
}

func (s *Set) applyLocked(in iv.Interval, remove bool) error {
	old := append([]iv.Interval(nil), s.spans...)
	next, probes, err := mutate(old, in, remove, s.maxSpans)
	if err != nil {
		return err
	}
	s.spans, s.probeCount = next, probes
	return nil
}

func mutate(spans []iv.Interval, in iv.Interval, remove bool, maxSpans int) ([]iv.Interval, int, error) {
	idx := sort.Search(len(spans), func(i int) bool { return spans[i].Lo >= in.Lo })
	first, last := idx, idx
	for first > 0 && spans[first-1].Hi >= in.Lo {
		first--
	}
	for last < len(spans) && spans[last].Lo <= in.Hi {
		last++
	}
	touched := last - first
	parts := make([]iv.Interval, 0, touched+2)
	if !remove {
		lo, hi := in.Lo, in.Hi
		if first < last && spans[first].Lo < lo {
			lo = spans[first].Lo
		}
		if first < last && spans[last-1].Hi > hi {
			hi = spans[last-1].Hi
		}
		parts = append(parts, iv.Interval{Lo: lo, Hi: hi})
	} else {
		if first < last && spans[first].Lo < in.Lo {
			parts = append(parts, iv.Interval{Lo: spans[first].Lo, Hi: in.Lo})
		}
		if first < last && spans[last-1].Hi > in.Hi {
			parts = append(parts, iv.Interval{Lo: in.Hi, Hi: spans[last-1].Hi})
		}
	}
	next := append(append([]iv.Interval(nil), spans[:first]...), parts...)
	next = append(next, spans[last:]...)
	if !remove && len(next) > maxSpans {
		return nil, 0, ErrTooManySpans
	}
	return next, touched, nil
}

func (s *Set) Contains(x int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := sort.Search(len(s.spans), func(i int) bool { return s.spans[i].Hi > x })
	return idx < len(s.spans) && s.spans[idx].Lo <= x
}

func (s *Set) Spans() []iv.Interval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]iv.Interval(nil), s.spans...)
}
