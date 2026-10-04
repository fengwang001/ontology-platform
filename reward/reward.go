// Package reward 实现截榜名次档位判定与领奖。
package reward

import (
	"errors"
	"sync"

	"ontology/ladder"
	"ontology/season"
)

var (
	ErrInvalid  = errors.New("reward: invalid argument")
	ErrClock    = errors.New("reward: clock moved backwards")
	ErrFrozen   = errors.New("season: season frozen")
	ErrOpen     = errors.New("season: season not frozen")
	ErrNoSettle = errors.New("reward: no settlement yet")
	ErrAbsent   = errors.New("reward: player absent from settlement")
	ErrExpired  = errors.New("reward: claim window expired")
	ErrInelig   = errors.New("reward: player not qualified")
	ErrClaimed  = errors.New("reward: already claimed")
)

// Tier 是一档千分比门槛与金额。
type Tier struct {
	P int64
	A int64
}

// Entry 是玩家在某次结算中的结果。
type Entry struct {
	Player   string
	Rank     int64
	Tier     int // 1 起；不合格为 0
	Amount   int64
	Eligible bool
	Claimed  bool
}

// System 组合 ladder、season 与领奖窗口。
type System struct {
	ld *ladder.Ladder
	ss *season.Season

	base  int64
	gmin  int64
	k     int64
	ak    int64
	wc    int64
	tiers []Tier

	mu      sync.Mutex
	lastNow int64
	result  *settlement

	touched int
}

type settlement struct {
	ts      int64
	entries map[string]*Entry
	claimed map[string]bool
}

// New 构造完整系统。
func New(base, w, l, gmin, k, ak int64, tiers []Tier, rho, wc int64) (*System, error) {
	if base < 0 || base > 1_000_000 ||
		w < 1 || w > 10_000 || l < 1 || l > 10_000 ||
		gmin < 0 || gmin > 10_000 ||
		k < 0 || k > 1_000_000 ||
		ak < 0 || ak > 1_000_000_000 ||
		rho < 0 || rho > 100 ||
		wc < 1 || wc > 10_000_000_000 {
		return nil, ErrInvalid
	}
	if len(tiers) < 1 || len(tiers) > 8 {
		return nil, ErrInvalid
	}
	for i, t := range tiers {
		if t.P <= 0 || t.P > 1000 || t.A < 0 || t.A > 1_000_000_000 {
			return nil, ErrInvalid
		}
		if i > 0 {
			if t.P <= tiers[i-1].P {
				return nil, ErrInvalid
			}
			if t.A > tiers[i-1].A {
				return nil, ErrInvalid
			}
		}
	}
	if tiers[len(tiers)-1].P != 1000 {
		return nil, ErrInvalid
	}
	ld, err := ladder.New(base, w, l)
	if err != nil {
		return nil, mapErr(err)
	}
	ss, err := season.New(ld, rho)
	if err != nil {
		return nil, mapErr(err)
	}
	cp := append([]Tier(nil), tiers...)
	return &System{
		ld:    ld,
		ss:    ss,
		gmin:  gmin,
		k:     k,
		ak:    ak,
		wc:    wc,
		tiers: cp,
	}, nil
}

// Ladder 暴露底层积分榜。
func (sys *System) Ladder() *ladder.Ladder { return sys.ld }

// Report 上报对局。
func (sys *System) Report(now int64, winner, loser string) error {
	if winner == "" || loser == "" || winner == loser || !validNow(now) {
		return ErrInvalid
	}
	if err := sys.checkNow(now); err != nil {
		return err
	}
	if err := mapErr(sys.ld.Report(now, winner, loser)); err != nil {
		return err
	}
	sys.advanceClock(now)
	return nil
}

// Settle 截榜结算。
func (sys *System) Settle(now int64) error {
	if err := sys.checkNow(now); err != nil {
		return err
	}
	_, snap, err := sys.ss.Settle(now)
	if err != nil {
		return mapErr(err)
	}
	sys.mu.Lock()
	defer sys.mu.Unlock()
	sys.result = sys.buildSettlement(snap)
	sys.lastNow = now
	return nil
}

// Start 开启下一赛季。
func (sys *System) Start(now int64) error {
	if err := sys.checkNow(now); err != nil {
		return err
	}
	if err := mapErr(sys.ss.Start(now)); err != nil {
		return err
	}
	sys.mu.Lock()
	defer sys.mu.Unlock()
	sys.lastNow = now
	return nil
}

// Claim 在窗口内领取奖励，返回金额。
func (sys *System) Claim(now int64, player string) (int64, error) {
	if err := sys.checkNow(now); err != nil {
		return 0, err
	}
	sys.mu.Lock()
	defer sys.mu.Unlock()
	sys.touched = 0
	if player == "" {
		return 0, ErrInvalid
	}
	if sys.result == nil {
		return 0, ErrNoSettle
	}
	res := sys.result
	sys.touched++ // 读取 entries 一次
	e, ok := res.entries[player]
	if !ok {
		return 0, ErrAbsent
	}
	if now >= res.ts+sys.wc {
		return 0, ErrExpired
	}
	if !e.Eligible {
		return 0, ErrInelig
	}
	sys.touched++ // 读取 claimed 一次
	if res.claimed[player] {
		return 0, ErrClaimed
	}
	res.claimed[player] = true
	e.Claimed = true
	sys.lastNow = now
	return e.Amount, nil
}

// Result 返回玩家在最近一次结算中的结果。
func (sys *System) Result(player string) (Entry, error) {
	if player == "" {
		return Entry{}, ErrInvalid
	}
	sys.mu.Lock()
	defer sys.mu.Unlock()
	if sys.result == nil {
		return Entry{}, ErrNoSettle
	}
	if e, ok := sys.result.entries[player]; ok {
		return *e, nil
	}
	return Entry{}, ErrAbsent
}

// touchedCount 返回自上次重置以来 Claim 读取的玩家记录数（测试用）。
func (sys *System) touchedCount() int {
	sys.mu.Lock()
	defer sys.mu.Unlock()
	return sys.touched
}

// resetTouched 清零 touched（测试用）。
func (sys *System) resetTouched() {
	sys.mu.Lock()
	defer sys.mu.Unlock()
	sys.touched = 0
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}

func (sys *System) checkNow(now int64) error {
	if !validNow(now) {
		return ErrInvalid
	}
	sys.mu.Lock()
	defer sys.mu.Unlock()
	if now < sys.lastNow {
		return ErrClock
	}
	return nil
}

func (sys *System) advanceClock(now int64) {
	sys.mu.Lock()
	defer sys.mu.Unlock()
	if now > sys.lastNow {
		sys.lastNow = now
	}
}

func (sys *System) buildSettlement(snap *ladder.Snapshot) *settlement {
	res := &settlement{
		ts:      snap.TS,
		entries: make(map[string]*Entry, len(snap.Stats)),
		claimed: make(map[string]bool),
	}
	elig := make([]string, 0, len(snap.Players))
	for _, name := range snap.Players {
		st := snap.Stats[name]
		e := &Entry{Player: name}
		if st.Games >= sys.gmin {
			e.Eligible = true
			elig = append(elig, name)
		}
		res.entries[name] = e
	}
	n := int64(len(elig))
	// snap.Players 按分数降序，r = 严格高分人数 + 1。
	var higher int64
	for i, name := range elig {
		if i > 0 && snap.Stats[elig[i-1]].Score != snap.Stats[name].Score {
			higher = int64(i)
		}
		r := higher + 1
		e := res.entries[name]
		e.Rank = r
		ti := len(sys.tiers) - 1
		if r == 1 {
			ti = 0
		} else {
			for j, t := range sys.tiers {
				if r*1000 <= n*t.P {
					ti = j
					break
				}
			}
		}
		e.Tier = ti + 1
		e.Amount = sys.tiers[ti].A
		if r <= sys.k {
			e.Amount += sys.ak
		}
	}
	return res
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, ladder.ErrInvalid) || errors.Is(err, season.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, ladder.ErrClock) || errors.Is(err, season.ErrClock):
		return ErrClock
	case errors.Is(err, ladder.ErrFrozen) || errors.Is(err, season.ErrFrozen):
		return ErrFrozen
	case errors.Is(err, ladder.ErrOpen) || errors.Is(err, season.ErrOpen):
		return ErrOpen
	default:
		return err
	}
}
