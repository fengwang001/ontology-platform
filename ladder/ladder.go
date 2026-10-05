// Package ladder 提供天梯赛季系统的门面 System：积分与对局数
// 维护（Report）、时钟与并发串行化，并委托 season/reward 完成
// 结算、软重置与领奖。所有操作可并发调用，效果等价于某个串行顺序。
package ladder

import (
	"errors"
	"sync"

	"ontology/reward"
	"ontology/season"
)

var (
	// ErrInvalidParam 参数非法（now 越界、玩家名为空或对战双方相同等）。
	ErrInvalidParam = reward.ErrInvalidParam
	// ErrClockRollback now 小于已接受操作的最大 now。
	ErrClockRollback = errors.New("ladder: clock rollback")
	// ErrNoSettlement 尚无结算结果。
	ErrNoSettlement = errors.New("ladder: no settlement yet")
)

// maxNow 为 now 的合法上界（10^12 毫秒）。
const maxNow = int64(1_000_000_000_000)

// System 天梯赛季系统。clock 为已接受操作的最大 now；touched 统计
// 通过 player() 读取的玩家记录数，用于证明 Claim 不随玩家总数扫榜。
type System struct {
	mu      sync.Mutex
	cfg     reward.Config
	players map[string]*reward.Player
	machine season.Machine
	clock   int64
	touched int64
}

// New 构造系统；任一参数越界（含 tiers 金额非非增）报 ErrInvalidParam。
func New(base, w, l, gMin, k, aK int64, tiers []reward.Tier, rho, wc int64) (*System, error) {
	cfg := reward.Config{
		Base:  base,
		W:     w,
		L:     l,
		GMin:  gMin,
		K:     k,
		AK:    aK,
		Tiers: append([]reward.Tier(nil), tiers...),
		Rho:   rho,
		Wc:    wc,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &System{cfg: cfg, players: make(map[string]*reward.Player)}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// player 读取（必要时以 base 分、0 局登记）玩家记录。
func (s *System) player(name string) *reward.Player {
	p, ok := s.players[name]
	if !ok {
		p = &reward.Player{Score: s.cfg.Base}
		s.players[name] = p
	}
	s.touched++
	return p
}

// Report 记录一场对局：胜者加 w，败者减 l（触底 0），双方对局数加 1。
// Frozen 期间报 ErrSeasonFrozen 且积分与对局数不得有任何变化。
func (s *System) Report(now int64, winner, loser string) error {
	if !validNow(now) || winner == "" || loser == "" || winner == loser {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	if s.machine.Frozen {
		return season.ErrSeasonFrozen
	}
	wp := s.player(winner)
	lp := s.player(loser)
	wp.Score += s.cfg.W
	lp.Score -= s.cfg.L
	if lp.Score < 0 {
		lp.Score = 0
	}
	wp.Games++
	lp.Games++
	s.clock = now
	return nil
}

// Settle 截榜生成结算结果并转入 Frozen；须为 Open。
func (s *System) Settle(now int64) error {
	if !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	if err := s.machine.Settle(s.players, s.cfg, now); err != nil {
		return err
	}
	s.clock = now
	return nil
}

// Start 对所有已登记玩家软重置并清零对局数，转入 Open；须为 Frozen。
// 不影响已生成的结算结果。
func (s *System) Start(now int64) error {
	if !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	if err := s.machine.Start(s.players, s.cfg); err != nil {
		return err
	}
	s.clock = now
	return nil
}

// Claim 领取玩家在最近一次结算中的奖励，每人每份结算至多一次。
// 拒绝次序：参数非法 > 时钟回退 > 尚无结算 > 不在结算中 >
// 窗口已过 > 不合格 > 已领。被拒绝时不改状态也不推进时钟。
func (s *System) Claim(now int64, player string) (int64, error) {
	if !validNow(now) || player == "" {
		return 0, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return 0, ErrClockRollback
	}
	st := s.machine.Settlement
	if st == nil {
		return 0, ErrNoSettlement
	}
	amount, err := st.Claim(player, now, s.cfg.Wc)
	if err != nil {
		return 0, err
	}
	s.clock = now
	return amount, nil
}

// Result 返回玩家在最近一次结算中的名次、档位、是否合格与是否已领；
// 无结算或玩家不在结算中时 ok 为 false。
func (s *System) Result(player string) (reward.Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.machine.Settlement
	if st == nil {
		return reward.Entry{}, false
	}
	return st.Result(player)
}
