package snapshot

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrInvalidN      = errors.New("snapshot: n must be between 1 and 16")
	ErrInvalidIndex  = errors.New("snapshot: unit index out of range")
	ErrInvalidCaller = errors.New("snapshot: caller can only update its own unit")
)

type record struct {
	value    int
	epoch    uint64
	point    uint64
	commit   uint64
	snapshot []int
	prev     *record
}

type Snapshot struct {
	n        int
	records  []atomic.Pointer[record]
	epoch    atomic.Uint64
	commit   atomic.Uint64
	maxScan  atomic.Uint64
	commitMu sync.Mutex
}

// New creates a wait-free atomic snapshot with n zero-valued units.
func New(n int) (*Snapshot, error) {
	if n < 1 || n > 16 {
		return nil, ErrInvalidN
	}

	s := &Snapshot{
		n:       n,
		records: make([]atomic.Pointer[record], n),
	}
	initial := &record{}
	for i := range s.records {
		s.records[i].Store(initial)
	}
	return s, nil
}

// Update writes value to index. Only writer index may update that unit.
func (s *Snapshot) Update(writer, index int, value int) error {
	if index < 0 || index >= s.n {
		return ErrInvalidIndex
	}
	if writer != index {
		return ErrInvalidCaller
	}

	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	snapshot, epoch, point, _ := s.scan(false)
	commit := s.commit.Add(1)
	old := s.records[index].Load()
	s.records[index].Store(&record{
		value:    value,
		epoch:    epoch,
		point:    point,
		commit:   commit,
		snapshot: snapshot,
		prev:     old,
	})
	return nil
}

// Read returns an atomic vector snapshot of every unit.
func (s *Snapshot) Read() []int {
	snapshot, _, _, _ := s.scan(true)
	return snapshot
}

// MaxCollections returns the largest number of collects used by one Read.
func (s *Snapshot) MaxCollections() uint64 {
	return s.maxScan.Load()
}

func (s *Snapshot) readForTest() ([]int, uint64) {
	snapshot, _, point, _ := s.scan(true)
	return snapshot, point
}

func (s *Snapshot) collect() []*record {
	values := make([]*record, s.n)
	for i := range values {
		values[i] = s.records[i].Load()
	}
	return values
}

func (s *Snapshot) scan(external bool) ([]int, uint64, uint64, int) {
	startEpoch := s.epoch.Add(1)
	previous := s.collect()
	collections := 1

	for _, currentRecord := range previous {
		if currentRecord.epoch >= startEpoch {
			snapshot := append([]int(nil), currentRecord.snapshot...)
			if external {
				s.recordMax(collections)
			}
			return snapshot, currentRecord.epoch, currentRecord.point, collections
		}
	}

	for {
		current := s.collect()
		collections++

		for i := range current {
			if current[i].epoch >= startEpoch {
				snapshot := append([]int(nil), current[i].snapshot...)
				if external {
					s.recordMax(collections)
				}
				return snapshot, current[i].epoch, current[i].point, collections
			}
		}

		if equalRecords(current, previous) {
			snapshot := make([]int, s.n)
			var point uint64
			for i := range current {
				snapshot[i] = current[i].value
				if current[i].commit > point {
					point = current[i].commit
				}
			}
			if external {
				s.recordMax(collections)
			}
			return snapshot, startEpoch, point, collections
		}

		previous = current
	}
}

func equalRecords(a, b []*record) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Snapshot) recordMax(collections int) {
	for {
		current := s.maxScan.Load()
		if uint64(collections) <= current {
			return
		}
		if s.maxScan.CompareAndSwap(current, uint64(collections)) {
			return
		}
	}
}
