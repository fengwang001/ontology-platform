// Package escrow 维护按会话的报价锁定，以及 (玩家,物品) 与 (玩家,金币)
// 两个维度的锁定合计表。
//
// 本类型不做并发同步，调用方（trade.System）负责串行化。
// 内部非导出计数器 touched 记录读写的锁定记录条数：一次 Set 的开销为
// 新旧报价条目数之和加 2（两条金币记录），与该玩家的其他会话数量无关。
package escrow

// Entry 是一方在单个会话中的锁定量。
type Entry struct {
	Items map[string]int64
	Gold  int64
}

// Escrow 是所有有效会话的锁定台账。
type Escrow struct {
	bySess  map[int64]map[string]*Entry // sid -> 玩家 -> 锁定
	items   map[string]map[string]int64 // 玩家 -> 物品 -> 锁定合计
	gold    map[string]int64            // 玩家 -> 金币锁定合计
	touched int64                       // 读写的锁定记录条数
}

// New 返回空台账。
func New() *Escrow {
	return &Escrow{
		bySess: make(map[int64]map[string]*Entry),
		items:  make(map[string]map[string]int64),
		gold:   make(map[string]int64),
	}
}

func (e *Escrow) addItem(player, item string, delta int64) {
	m, ok := e.items[player]
	if !ok {
		m = make(map[string]int64)
		e.items[player] = m
	}
	q := m[item] + delta
	if q <= 0 {
		delete(m, item)
	} else {
		m[item] = q
	}
}

func (e *Escrow) addGold(player string, delta int64) {
	e.gold[player] += delta
}

// Set 整体替换某玩家在会话 sid 中的锁定：先撤掉旧报价的合计，再计入新报价。
// items 会被复制，调用方可以随后修改自己的副本。
func (e *Escrow) Set(sid int64, player string, items map[string]int64, gold int64) {
	m, ok := e.bySess[sid]
	if !ok {
		m = make(map[string]*Entry)
		e.bySess[sid] = m
	}
	var records int64
	if old := m[player]; old != nil {
		for item, q := range old.Items {
			e.addItem(player, item, -q)
			records++
		}
		e.addGold(player, -old.Gold)
	}
	next := &Entry{Items: make(map[string]int64, len(items)), Gold: gold}
	for item, q := range items {
		if q <= 0 {
			continue
		}
		next.Items[item] = q
		e.addItem(player, item, q)
		records++
	}
	e.addGold(player, gold)
	records += 2 // 旧金币记录 + 新金币记录
	e.touched += records
	m[player] = next
}

// Release 释放会话 sid 中双方的全部锁定。
func (e *Escrow) Release(sid int64) {
	m, ok := e.bySess[sid]
	if !ok {
		return
	}
	for player, ent := range m {
		for item, q := range ent.Items {
			e.addItem(player, item, -q)
			e.touched++
		}
		e.addGold(player, -ent.Gold)
		e.touched++
	}
	delete(e.bySess, sid)
}

// Locked 返回玩家某物品在所有有效会话中的锁定合计。
func (e *Escrow) Locked(player, item string) int64 {
	if m, ok := e.items[player]; ok {
		return m[item]
	}
	return 0
}

// LockedGold 返回玩家金币的锁定合计。
func (e *Escrow) LockedGold(player string) int64 {
	return e.gold[player]
}

// Touched 返回非导出计数器 touched 的当前值，用于证明 Set 的开销与
// 该玩家的其他会话数量无关。
func (e *Escrow) Touched() int64 {
	return e.touched
}
