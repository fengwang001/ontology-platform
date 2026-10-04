package hold

import (
	"errors"
	"sync"

	"ontology/wip"
)

var (
	ErrUnauthorized = errors.New("hold: operator is not a QE")
	ErrInvalid      = wip.ErrInvalid
	ErrNotFound     = wip.ErrNotFound
	ErrState        = wip.ErrState
)

type Role string

const QE Role = "QE"

type Identity struct {
	Name string
	Role Role
}

type System struct {
	m    *wip.Manager
	y    int
	nmin int64

	mu      sync.Mutex
	stats   map[string]map[int]fpStat
	woLocks map[string]*sync.Mutex
}

type fpStat struct {
	good  int64
	total int64
}

func New(m *wip.Manager, yieldPercent int, nmin int64) (*System, error) {
	if yieldPercent < 1 || yieldPercent > 100 || nmin < 1 {
		return nil, ErrInvalid
	}
	return &System{
		m:       m,
		y:       yieldPercent,
		nmin:    nmin,
		stats:   map[string]map[int]fpStat{},
		woLocks: map[string]*sync.Mutex{},
	}, nil
}

func (s *System) Report(wo string, i, k int, good, scrap, rework int64) error {
	if wo == "" || i < 1 || k < 0 || good < 0 || scrap < 0 || rework < 0 ||
		good+scrap+rework < 1 {
		return ErrInvalid
	}
	woMu := s.lockWO(wo)
	woMu.Lock()
	defer woMu.Unlock()

	st, err := s.m.Snapshot(wo)
	if err != nil {
		return ErrNotFound
	}
	if st.Closed {
		return wip.ErrClosed
	}
	if st.Held {
		return wip.ErrHeld
	}
	if i > st.N || k > st.R {
		return ErrInvalid
	}
	insp := st.Insp[i-1]

	if err := s.m.Report(wo, i, k, good, scrap, rework); err != nil {
		return err
	}

	if insp && k == 0 {
		s.mu.Lock()
		per := s.stats[wo]
		if per == nil {
			per = map[int]fpStat{}
			s.stats[wo] = per
		}
		fp := per[i]
		fp.good += good
		fp.total += good + scrap + rework
		per[i] = fp
		trigger := fp.total >= s.nmin && fp.good*100 < int64(s.y)*fp.total
		s.mu.Unlock()
		if trigger {
			s.m.SetHeld(wo, true)
		}
	}
	return nil
}

func (s *System) lockWO(wo string) *sync.Mutex {
	s.mu.Lock()
	mu := s.woLocks[wo]
	if mu == nil {
		mu = &sync.Mutex{}
		s.woLocks[wo] = mu
	}
	s.mu.Unlock()
	return mu
}

func (s *System) Split(wo, newWO string, i, k int, qty int64) error {
	if wo == "" || newWO == "" || i < 1 || k < 0 || qty < 1 {
		return ErrInvalid
	}
	return s.m.Split(wo, newWO, i, k, qty)
}

func (s *System) Close(wo string) (*wip.CloseResult, error) {
	if wo == "" {
		return nil, ErrInvalid
	}
	return s.m.Close(wo)
}

func (s *System) Resume(wo string, op Identity) error {
	if wo == "" || op.Name == "" {
		return ErrInvalid
	}
	if op.Role != QE {
		return ErrUnauthorized
	}
	held, ok := s.m.IsHeld(wo)
	if !ok {
		return ErrNotFound
	}
	if !held {
		return ErrState
	}
	woMu := s.lockWO(wo)
	woMu.Lock()
	defer woMu.Unlock()

	// Re-check under the per-WO serial lock: a concurrent Report/Resume may
	// have changed the picture.
	st, err := s.m.Snapshot(wo)
	if err != nil {
		return ErrNotFound
	}
	if !st.Held {
		return ErrState
	}
	s.m.SetHeld(wo, false)
	s.mu.Lock()
	delete(s.stats, wo)
	s.mu.Unlock()
	return nil
}

// FPStat is one inspection point's first-pass counters.
type FPStat struct {
	Good  int64
	Total int64
}

func (s *System) Stats(wo string) (map[int]FPStat, error) {
	if _, ok := s.m.IsHeld(wo); !ok {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]FPStat{}
	for i, fp := range s.stats[wo] {
		out[i] = FPStat{Good: fp.good, Total: fp.total}
	}
	return out, nil
}
