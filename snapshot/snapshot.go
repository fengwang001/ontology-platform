package snapshot

import (
	"errors"
	"sync/atomic"
)

const MaxN = 16

var (
	ErrInvalidN       = errors.New("snapshot: invalid n")
	ErrCellOutOfRange = errors.New("snapshot: cell index out of range")
	ErrWriterMismatch = errors.New("snapshot: writer id does not match cell")
)

type record struct {
	value int64
	seq   uint64
	snap  []int64
}

type Snapshot struct {
	n           int
	cells       []atomic.Pointer[record]
	maxCollects atomic.Int64
}

func New(n int) (*Snapshot, error) {
	if n < 1 || n > MaxN {
		return nil, ErrInvalidN
	}
	s := &Snapshot{n: n, cells: make([]atomic.Pointer[record], n)}
	for i := range s.cells {
		s.cells[i].Store(&record{})
	}
	return s, nil
}

func (s *Snapshot) N() int { return s.n }

func (s *Snapshot) Update(writer, cell int, value int64) error {
	if cell < 0 || cell >= s.n {
		return ErrCellOutOfRange
	}
	if writer != cell {
		return ErrWriterMismatch
	}
	embedded, _ := s.scan()
	for {
		old := s.cells[cell].Load()
		next := &record{value: value, seq: old.seq + 1, snap: embedded}
		if s.cells[cell].CompareAndSwap(old, next) {
			return nil
		}
	}
}

func (s *Snapshot) Scan() []int64 {
	values, collects := s.scan()
	for {
		cur := s.maxCollects.Load()
		if int64(collects) <= cur || s.maxCollects.CompareAndSwap(cur, int64(collects)) {
			break
		}
	}
	return values
}

func (s *Snapshot) MaxCollects() int {
	return int(s.maxCollects.Load())
}

func (s *Snapshot) collect() []*record {
	recs := make([]*record, s.n)
	for i := range s.cells {
		recs[i] = s.cells[i].Load()
	}
	return recs
}

func (s *Snapshot) scan() ([]int64, int) {
	collects := 0
	prev := s.collect()
	collects++
	first := make([]uint64, s.n)
	for i, r := range prev {
		first[i] = r.seq
	}
	for {
		cur := s.collect()
		collects++
		clean := true
		for i := range cur {
			if cur[i].seq != prev[i].seq {
				clean = false
				break
			}
		}
		if clean {
			return valuesOf(cur), collects
		}
		for i := range cur {
			if cur[i].seq >= first[i]+2 {
				borrowed := make([]int64, s.n)
				copy(borrowed, cur[i].snap)
				return borrowed, collects
			}
		}
		prev = cur
	}
}

func valuesOf(recs []*record) []int64 {
	values := make([]int64, len(recs))
	for i, r := range recs {
		values[i] = r.value
	}
	return values
}
