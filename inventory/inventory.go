// Package inventory 记录每名玩家的物品持有量与金币。
//
// Inv 在 Grant 时强制执行 Slots（持有量大于 0 的物品种类数上限）与
// CAP（金币上限）。本类型不做并发同步，调用方（trade.System）负责串行化。
package inventory

import (
	"errors"
	"math"
)

var (
	ErrInvalid  = errors.New("inventory: invalid argument")
	ErrOverflow = errors.New("inventory: amount overflow")
	ErrSlots    = errors.New("inventory: slot limit exceeded")
	ErrGoldCap  = errors.New("inventory: gold cap exceeded")
)

type player struct {
	items map[string]int64 // 只保存持有量大于 0 的物品
	gold  int64
}

// Inv 是库存与金币账本。
type Inv struct {
	goldCap int64
	slots   int64
	players map[string]*player
}

// New 返回一个金币上限为 goldCap、种类数上限为 slots 的空账本。
func New(goldCap, slots int64) *Inv {
	return &Inv{goldCap: goldCap, slots: slots, players: make(map[string]*player)}
}

// Has 报告玩家是否已登记。
func (v *Inv) Has(p string) bool {
	_, ok := v.players[p]
	return ok
}

func (v *Inv) ensure(p string) *player {
	pl, ok := v.players[p]
	if !ok {
		pl = &player{items: make(map[string]int64)}
		v.players[p] = pl
	}
	return pl
}

// Grant 发放 qty 个 item 并登记玩家；会使种类数超过 Slots 的发放被拒绝，
// 被拒绝时不留任何副作用（包括不登记玩家）。
func (v *Inv) Grant(p, item string, qty int64) error {
	if p == "" || item == "" || qty < 0 {
		return ErrInvalid
	}
	cur := v.Holding(p, item)
	if qty > 0 {
		if cur == 0 && v.Kinds(p) >= v.slots {
			return ErrSlots
		}
		if qty > math.MaxInt64-cur {
			return ErrOverflow
		}
	}
	pl := v.ensure(p)
	if qty > 0 {
		pl.items[item] = cur + qty
	}
	return nil
}

// GrantGold 发放 g 金币并登记玩家；会使金币超过 CAP 的发放被拒绝。
func (v *Inv) GrantGold(p string, g int64) error {
	if p == "" || g < 0 {
		return ErrInvalid
	}
	if g > v.goldCap-v.Gold(p) {
		return ErrGoldCap
	}
	v.ensure(p).gold += g
	return nil
}

// Holding 返回玩家当前持有的某物品数量。
func (v *Inv) Holding(p, item string) int64 {
	if pl, ok := v.players[p]; ok {
		return pl.items[item]
	}
	return 0
}

// Gold 返回玩家当前金币。
func (v *Inv) Gold(p string) int64 {
	if pl, ok := v.players[p]; ok {
		return pl.gold
	}
	return 0
}

// Kinds 返回玩家持有量大于 0 的物品种类数。
func (v *Inv) Kinds(p string) int64 {
	if pl, ok := v.players[p]; ok {
		return int64(len(pl.items))
	}
	return 0
}

// Holdings 返回玩家全部持有量的副本。
func (v *Inv) Holdings(p string) map[string]int64 {
	out := make(map[string]int64)
	if pl, ok := v.players[p]; ok {
		for item, q := range pl.items {
			out[item] = q
		}
	}
	return out
}

// KindsAfter 返回按 deltas（负为扣除、正为加入）调整后的种类数，
// 不修改账本。种类数按先扣除、再加入之后持有量大于 0 的种类计。
func (v *Inv) KindsAfter(p string, deltas map[string]int64) int64 {
	merged := v.Holdings(p)
	for item, d := range deltas {
		merged[item] += d
		if merged[item] <= 0 {
			delete(merged, item)
		}
	}
	return int64(len(merged))
}

// ApplyItems 按 deltas 调整持有量，结果不大于 0 的种类被移除。
// 调用方保证不会出现负持有量。
func (v *Inv) ApplyItems(p string, deltas map[string]int64) {
	pl := v.ensure(p)
	for item, d := range deltas {
		if d == 0 {
			continue
		}
		q := pl.items[item] + d
		if q <= 0 {
			delete(pl.items, item)
		} else {
			pl.items[item] = q
		}
	}
}

// ApplyGold 按 delta（可为负）调整金币。调用方保证结果不越界。
func (v *Inv) ApplyGold(p string, delta int64) {
	v.ensure(p).gold += delta
}
