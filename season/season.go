// Package season 在 ladder 之上实现赛季状态机与软重置。
package season

import (
	"errors"
	"sync"

	"ontology/ladder"
)

var (
	ErrInvalid = errors.New("season: invalid argument")
	ErrClock   = errors.New("season: clock moved backwards")
	ErrFrozen  = errors.New("season: season frozen")
	ErrOpen    = errors.New("season: season not frozen")
)

// Season 是赛季状态机。
type Season struct {
	ld  *ladder.Ladder
	rho int64

	mu sync.Mutex
}

// New 构造赛季机：rho 为软重置比例（0–100）。
func New(ld *ladder.Ladder, rho int64) (*Season, error) {
	if ld == nil || rho < 0 || rho > 100 {
		return nil, ErrInvalid
	}
	return &Season{ld: ld, rho: rho}, nil
}

// Settle 在 Open 时截榜，转 Frozen，返回时刻 ts 与快照。
func (s *Season) Settle(now int64) (int64, *ladder.Snapshot, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return 0, nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ld.CheckClock(now); err != nil {
		return 0, nil, ErrClock
	}
	if s.ld.Frozen() {
		return 0, nil, ErrFrozen
	}
	snap, err := s.ld.Freeze(now)
	if err != nil {
		if errors.Is(err, ladder.ErrClock) {
			return 0, nil, ErrClock
		}
		if errors.Is(err, ladder.ErrFrozen) {
			return 0, nil, ErrFrozen
		}
		return 0, nil, err
	}
	return snap.TS, snap, nil
}

// Start 在 Frozen 时软重置并转 Open。
func (s *Season) Start(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ld.CheckClock(now); err != nil {
		return ErrClock
	}
	if !s.ld.Frozen() {
		return ErrOpen
	}
	if err := s.ld.SoftReset(now, s.rho); err != nil {
		if errors.Is(err, ladder.ErrClock) {
			return ErrClock
		}
		if errors.Is(err, ladder.ErrOpen) {
			return ErrOpen
		}
		return err
	}
	return nil
}
