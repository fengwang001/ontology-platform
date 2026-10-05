// Package season 实现赛季状态机（Open/Frozen）、截榜结算与软重置。
// Machine 本身不加锁，并发串行化由上层（ladder.System）负责。
package season

import (
	"errors"

	"ontology/reward"
)

var (
	// ErrSeasonFrozen 赛季处于 Frozen，拒绝 Report/Settle。
	ErrSeasonFrozen = errors.New("season: season is frozen")
	// ErrNotFrozen 赛季未冻结（Open），拒绝 Start。
	ErrNotFrozen = errors.New("season: season is not frozen")
)

// Machine 赛季状态机：记录是否冻结、最近一次结算与其时刻。
// 只保留最近一次结算结果，下一次 Settle 覆盖它（未领的奖作废），
// Start 不影响已生成的结算结果。
type Machine struct {
	Frozen     bool
	Ts         int64
	Settlement *reward.Settlement
}

// Settle 须为 Open，否则报 ErrSeasonFrozen。截榜生成结算结果并
// 转入 Frozen，记结算时刻 now。
func (m *Machine) Settle(players map[string]*reward.Player, cfg reward.Config, now int64) error {
	if m.Frozen {
		return ErrSeasonFrozen
	}
	m.Settlement = reward.Compute(players, cfg, now)
	m.Ts = now
	m.Frozen = true
	return nil
}

// Start 须为 Frozen，否则报 ErrNotFrozen。对所有已登记玩家做软
// 重置并清零对局数，转入 Open。
func (m *Machine) Start(players map[string]*reward.Player, cfg reward.Config) error {
	if !m.Frozen {
		return ErrNotFrozen
	}
	for _, p := range players {
		p.Score = cfg.Base + floorDiv((p.Score-cfg.Base)*cfg.Rho, 100)
		p.Games = 0
	}
	m.Frozen = false
	return nil
}

// floorDiv 数学向下取整除法（b > 0）：负余数向负无穷取整，
// 而非 Go 原生 / 的向零截断。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}
