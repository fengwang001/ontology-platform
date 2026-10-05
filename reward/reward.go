// Package reward 定义天梯赛季的奖励领域模型：参数校验、截榜时
// 的名次与千分比档位计算，以及单次结算结果上的领奖判定。
package reward

import (
	"errors"
	"sort"
)

var (
	// ErrInvalidParam 参数非法（New 的构造参数或各操作的入参越界）。
	ErrInvalidParam = errors.New("reward: invalid parameter")
	// ErrNotInSettlement 玩家不在本次结算中（截榜时未登记）。
	ErrNotInSettlement = errors.New("reward: player not in settlement")
	// ErrWindowExpired 领奖窗口已过（now >= ts+Wc）。
	ErrWindowExpired = errors.New("reward: claim window expired")
	// ErrNotQualified 对局数不足，截榜时不合格。
	ErrNotQualified = errors.New("reward: player not qualified")
	// ErrAlreadyClaimed 该玩家已领取本份结算的奖励。
	ErrAlreadyClaimed = errors.New("reward: reward already claimed")
)

// Tier 千分比档位：P 为千分比（末档必须为 1000），A 为该档金额。
type Tier struct {
	P int64
	A int64
}

// Config 赛季配置，取值范围见 Validate。
type Config struct {
	Base  int64 // 初始分，0..10^6
	W     int64 // 胜场加分，1..10^4
	L     int64 // 负场减分，1..10^4
	GMin  int64 // 合格对局数，0..10^4
	K     int64 // 固定名次奖覆盖前 K 名，0..10^6
	AK    int64 // 固定名次奖金额，0..10^9
	Tiers []Tier
	Rho   int64 // 软重置比例，0..100
	Wc    int64 // 领奖窗口毫秒数，1..10^10
}

// Validate 校验配置，任何一项越界即返回 ErrInvalidParam。
func (c Config) Validate() error {
	if c.Base < 0 || c.Base > 1_000_000 {
		return ErrInvalidParam
	}
	if c.W < 1 || c.W > 10_000 || c.L < 1 || c.L > 10_000 {
		return ErrInvalidParam
	}
	if c.GMin < 0 || c.GMin > 10_000 {
		return ErrInvalidParam
	}
	if c.K < 0 || c.K > 1_000_000 {
		return ErrInvalidParam
	}
	if c.AK < 0 || c.AK > 1_000_000_000 {
		return ErrInvalidParam
	}
	if len(c.Tiers) < 1 || len(c.Tiers) > 8 {
		return ErrInvalidParam
	}
	prevP := int64(0)
	for i, t := range c.Tiers {
		if t.P <= prevP || t.P > 1000 {
			return ErrInvalidParam
		}
		if t.A < 0 || t.A > 1_000_000_000 {
			return ErrInvalidParam
		}
		if i > 0 && t.A > c.Tiers[i-1].A {
			return ErrInvalidParam // 金额须非增，保证分高者奖励不低于分低者
		}
		prevP = t.P
	}
	if c.Tiers[len(c.Tiers)-1].P != 1000 {
		return ErrInvalidParam
	}
	if c.Rho < 0 || c.Rho > 100 {
		return ErrInvalidParam
	}
	if c.Wc < 1 || c.Wc > 10_000_000_000 {
		return ErrInvalidParam
	}
	return nil
}

// Player 一名已登记玩家的积分与对局数。
type Player struct {
	Score int64
	Games int64
}

// Entry 一名玩家在单次结算中的结果快照。
type Entry struct {
	Rank      int64 // 名次，不合格为 0
	Tier      int   // 档位下标，不合格为 -1
	Amount    int64 // 应发奖励（含固定名次奖）
	Qualified bool
	Claimed   bool
}

// Settlement 一次截榜结算的完整结果，生成后独立于玩家表。
type Settlement struct {
	Ts      int64
	Entries map[string]*Entry
}

// TierOf 返回名次 rank 在 N 名合格者中所属的最小档位下标：
// 取最小的 i 使 rank*1000 <= N*p_i（交叉相乘，取等入档）；
// 例外：rank == 1 恒入第 0 档。调用方保证末档 p == 1000，故必有解。
func TierOf(rank, n int64, tiers []Tier) int {
	if rank == 1 {
		return 0
	}
	for i, t := range tiers {
		if rank*1000 <= n*t.P {
			return i
		}
	}
	return len(tiers) - 1
}

// Compute 截榜：对局数 >= GMin 的玩家为合格并参与排名，不合格者
// 也在结算中（Qualified=false）但不占名次。同分同名次，并列可使
// 某档或固定名次奖人数超出名额，照发。
func Compute(players map[string]*Player, cfg Config, ts int64) *Settlement {
	st := &Settlement{Ts: ts, Entries: make(map[string]*Entry, len(players))}
	type scored struct {
		name  string
		score int64
	}
	qual := make([]scored, 0, len(players))
	for name, p := range players {
		if p.Games >= cfg.GMin {
			qual = append(qual, scored{name, p.Score})
		} else {
			st.Entries[name] = &Entry{Tier: -1}
		}
	}
	sort.Slice(qual, func(i, j int) bool { return qual[i].score > qual[j].score })
	n := int64(len(qual))
	for i := 0; i < len(qual); {
		j := i
		for j < len(qual) && qual[j].score == qual[i].score {
			j++
		}
		rank := int64(i) + 1 // 严格更高分人数 + 1
		tier := TierOf(rank, n, cfg.Tiers)
		amount := cfg.Tiers[tier].A
		if rank <= cfg.K {
			amount += cfg.AK
		}
		for k := i; k < j; k++ {
			st.Entries[qual[k].name] = &Entry{
				Rank:      rank,
				Tier:      tier,
				Amount:    amount,
				Qualified: true,
			}
		}
		i = j
	}
	return st
}

// Claim 判定并执行领奖，成功返回金额。拒绝次序（调用方已先行
// 检查参数、时钟与结算存在性）：不在结算中 > 窗口已过 > 不合格 > 已领。
// 被拒绝时不改任何状态。
func (s *Settlement) Claim(player string, now, wc int64) (int64, error) {
	e, ok := s.Entries[player]
	if !ok {
		return 0, ErrNotInSettlement
	}
	if now >= s.Ts+wc {
		return 0, ErrWindowExpired
	}
	if !e.Qualified {
		return 0, ErrNotQualified
	}
	if e.Claimed {
		return 0, ErrAlreadyClaimed
	}
	e.Claimed = true
	return e.Amount, nil
}

// Result 返回玩家在本次结算中的结果快照。
func (s *Settlement) Result(player string) (Entry, bool) {
	e, ok := s.Entries[player]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}
