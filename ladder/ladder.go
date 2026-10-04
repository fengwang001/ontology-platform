// Package ladder 维护天梯积分、对局数与 Open/Frozen 状态。
package ladder

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalid = errors.New("ladder: invalid argument")
	ErrClock   = errors.New("ladder: clock moved backwards")
	ErrFrozen  = errors.New("ladder: season frozen")
	ErrOpen    = errors.New("ladder: season not frozen")
)

// Stats 是一名已登记玩家的积分与对局数。
type Stats struct {
	Score int64
	Games int64
}

// Snapshot 是截榜时刻的不可变快照（深拷贝）。
type Snapshot struct {
	TS      int64
	Players []string
	Stats   map[string]Stats
}

// Ladder 是积分榜。零值不可用，须经 New 构造。
type Ladder struct {
	base int64
	w    int64
	l    int64

	mu      sync.Mutex
	lastNow int64
	frozen  bool
	stats   map[string]Stats
}

// New 构造积分榜：初始分 base、胜加 w、负减 l。
func New(base, w, l int64) (*Ladder, error) {
	if base < 0 || base > 1_000_000 || w < 1 || w > 10_000 || l < 1 || l > 10_000 {
		return nil, ErrInvalid
	}
	return &Ladder{base: base, w: w, l: l, stats: map[string]Stats{}}, nil
}

// Report 上报一局比赛结果。
func (ld *Ladder) Report(now int64, winner, loser string) error {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if winner == "" || loser == "" || winner == loser {
		return ErrInvalid
	}
	if now < ld.lastNow {
		return ErrClock
	}
	if ld.frozen {
		return ErrFrozen
	}
	ld.lastNow = now
	ws, ok := ld.stats[winner]
	if !ok {
		ws = Stats{Score: ld.base}
	}
	ls, ok := ld.stats[loser]
	if !ok {
		ls = Stats{Score: ld.base}
	}
	ws.Score += ld.w
	ws.Games++
	ls.Score -= ld.l
	if ls.Score < 0 {
		ls.Score = 0
	}
	ls.Games++
	ld.stats[winner] = ws
	ld.stats[loser] = ls
	return nil
}

// Frozen 返回赛季是否冻结。
func (ld *Ladder) Frozen() bool {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	return ld.frozen
}

// Current 返回当前状态的深拷贝（不改变冻结状态），供观测与测试对照。
func (ld *Ladder) Current() (map[string]Stats, bool, int64) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	cp := make(map[string]Stats, len(ld.stats))
	for name, st := range ld.stats {
		cp[name] = st
	}
	return cp, ld.frozen, ld.lastNow
}

// Freeze 在 Open 状态下冻结并返回截榜快照。
func (ld *Ladder) Freeze(now int64) (*Snapshot, error) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if now < ld.lastNow {
		return nil, ErrClock
	}
	if ld.frozen {
		return nil, ErrFrozen
	}
	ld.lastNow = now
	ld.frozen = true
	players := make([]string, 0, len(ld.stats))
	for name := range ld.stats {
		players = append(players, name)
	}
	sort.Slice(players, func(i, j int) bool {
		si, sj := ld.stats[players[i]].Score, ld.stats[players[j]].Score
		if si != sj {
			return si > sj
		}
		return players[i] < players[j]
	})
	cp := make(map[string]Stats, len(ld.stats))
	for name, st := range ld.stats {
		cp[name] = st
	}
	return &Snapshot{TS: now, Players: players, Stats: cp}, nil
}

// CheckClock 只校验时钟是否回退，不改变任何状态。
func (ld *Ladder) CheckClock(now int64) error {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if now < ld.lastNow {
		return ErrClock
	}
	return nil
}

// SoftReset 在 Frozen 状态下软重置积分并清零对局数，随后解冻。
func (ld *Ladder) SoftReset(now, rho int64) error {
	if rho < 0 || rho > 100 {
		return ErrInvalid
	}
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if now < ld.lastNow {
		return ErrClock
	}
	if !ld.frozen {
		return ErrOpen
	}
	ld.lastNow = now
	for name, st := range ld.stats {
		st.Score = ld.base + floorDiv((st.Score-ld.base)*rho, 100)
		st.Games = 0
		ld.stats[name] = st
	}
	ld.frozen = false
	return nil
}

// floorDiv 返回数学意义上的 floor(a/b)，b 必须为正。
func floorDiv(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && a < 0 {
		q--
	}
	return q
}
