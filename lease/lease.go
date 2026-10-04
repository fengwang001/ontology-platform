package lease

import (
	"errors"
	"sort"

	"ontology/history"
)

var (
	ErrLeaseExists    = errors.New("lease already exists")
	ErrLeaseNotFound  = errors.New("lease not found")
	ErrLeaseBack      = errors.New("lease r moved backwards")
	ErrHistoryUnavail = errors.New("history unavailable below horizon")
	ErrLeaseLimit     = errors.New("lease limit exceeded")
	ErrCheckpointBack = errors.New("global checkpoint moved backwards")
)

type lease struct {
	name      string
	r         int64
	lastRenew int64
}

type Set struct {
	hist *history.History
	e    int64
	lmax int
	gcp  int64
	ls   map[string]*lease
}

func NewSet(hist *history.History, e int64, lmax int) (*Set, error) {
	if e < 1 || e > 1_000_000_000 || lmax < 1 || lmax > 1000 {
		return nil, history.ErrInvalidArgument
	}
	return &Set{
		hist: hist,
		e:    e,
		lmax: lmax,
		ls:   make(map[string]*lease),
	}, nil
}

func (s *Set) AddLease(now int64, name string, r int64) error {
	s.hist.Lock()
	defer s.hist.Unlock()
	if now < 0 || now > 1_000_000_000_000 || name == "" || r < 1 || r > s.hist.MaxSeqLocked()+1 {
		return history.ErrInvalidArgument
	}
	if err := s.hist.CheckNowLocked(now); err != nil {
		return err
	}
	if _, ok := s.ls[name]; ok {
		return ErrLeaseExists
	}
	if r < s.hist.HLocked() {
		return ErrHistoryUnavail
	}
	if len(s.ls) >= s.lmax {
		return ErrLeaseLimit
	}
	if err := s.hist.AcceptNowLocked(now); err != nil {
		return err
	}
	s.ls[name] = &lease{name: name, r: r, lastRenew: now}
	return nil
}

func (s *Set) RenewLease(now int64, name string, r int64) error {
	s.hist.Lock()
	defer s.hist.Unlock()
	if now < 0 || now > 1_000_000_000_000 || name == "" || r < 1 || r > s.hist.MaxSeqLocked()+1 {
		return history.ErrInvalidArgument
	}
	if err := s.hist.CheckNowLocked(now); err != nil {
		return err
	}
	l, ok := s.ls[name]
	if !ok {
		return ErrLeaseNotFound
	}
	if r < l.r {
		return ErrLeaseBack
	}
	if err := s.hist.AcceptNowLocked(now); err != nil {
		return err
	}
	l.r = r
	l.lastRenew = now
	return nil
}

func (s *Set) RemoveLease(now int64, name string) error {
	s.hist.Lock()
	defer s.hist.Unlock()
	if now < 0 || now > 1_000_000_000_000 || name == "" {
		return history.ErrInvalidArgument
	}
	if err := s.hist.CheckNowLocked(now); err != nil {
		return err
	}
	if _, ok := s.ls[name]; !ok {
		return ErrLeaseNotFound
	}
	if err := s.hist.AcceptNowLocked(now); err != nil {
		return err
	}
	delete(s.ls, name)
	return nil
}

func (s *Set) SetGlobalCheckpoint(now int64, g int64) error {
	s.hist.Lock()
	defer s.hist.Unlock()
	if now < 0 || now > 1_000_000_000_000 || g < 0 || g > s.hist.MaxSeqLocked() {
		return history.ErrInvalidArgument
	}
	if err := s.hist.CheckNowLocked(now); err != nil {
		return err
	}
	if g < s.gcp {
		return ErrCheckpointBack
	}
	if err := s.hist.AcceptNowLocked(now); err != nil {
		return err
	}
	s.gcp = g
	return nil
}

func (s *Set) PurgeExpiredLocked(now int64) []string {
	var removed []string
	for name, l := range s.ls {
		if now-l.lastRenew > s.e { // 严格大于 E：恰等不过期
			removed = append(removed, name)
			delete(s.ls, name)
		}
	}
	sort.Strings(removed)
	return removed
}

func (s *Set) RetainFloorLocked() int64 {
	floor := s.gcp + 1
	for _, l := range s.ls {
		if l.r < floor {
			floor = l.r
		}
	}
	return floor
}

// GCPlocked 仅供测试观察。
func (s *Set) GCPlocked() int64 { return s.gcp }

// Merge 驱动一次合并：Set 自身就是 Retention，history 负责清除与推进 H。
func (s *Set) Merge(now int64) (history.MergeResult, error) {
	return s.hist.Merge(now, s)
}
