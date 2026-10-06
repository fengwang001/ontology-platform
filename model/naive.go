// Package model 是与 van 包独立编写的朴素参照实现：
// 放置判定对每个分区逐条枚举全部在车货物（O(Z*N)），
// 不引用 van 包任何内部代码，仅复用其公开数据类型。
// 用途是随机差分测试中作为行为基准。
package model

import (
	"errors"
	"sort"

	"ontology/van"
)

// Naive 朴素模型。
type Naive struct {
	specs   []van.ZoneSpec
	board   map[int]van.Cargo // 货物编号 -> 货物
	zoneOf  map[int]int       // 货物编号 -> 分区
	arrived int
}

func New(specs []van.ZoneSpec) *Naive {
	cp := make([]van.ZoneSpec, len(specs))
	copy(cp, specs)
	return &Naive{specs: cp, board: map[int]van.Cargo{}, zoneOf: map[int]int{}}
}

func valid(c van.Cargo) bool {
	return c.ID > 0 && c.Weight > 0 && c.Volume > 0 && c.Stop > 0 &&
		c.Kind >= van.General && c.Kind <= van.Food
}

func compatible(a, b van.Category) bool {
	if a == b {
		return true
	}
	if a == van.Food && (b == van.Flammable || b == van.Oxidizer) {
		return false
	}
	if b == van.Food && (a == van.Flammable || a == van.Oxidizer) {
		return false
	}
	if (a == van.Flammable && b == van.Oxidizer) || (a == van.Oxidizer && b == van.Flammable) {
		return false
	}
	return true
}

// why 朴素判定：对分区 z 中每条在车货物逐一检查隔离，
// 再累加全部重量/体积。顺序约束由 feasible 独立枚举每一对货物给出。
func (m *Naive) why(z int, c van.Cargo) string {
	weight, volume := 0, 0
	for _, g := range m.board {
		if m.zoneOf[g.ID] != z {
			continue
		}
		weight += g.Weight
		volume += g.Volume
		if !compatible(g.Kind, c.Kind) {
			return "isolation"
		}
	}
	if weight+c.Weight > m.specs[z-1].WeightLimit {
		return "overweight"
	}
	if volume+c.Volume > m.specs[z-1].VolumeLimit {
		return "overvolume"
	}
	return ""
}

// feasible 朴素全局顺序校验：直接枚举“候选货物放入 z 后”的每一对在车货物，
// 校验题面原始规则——停靠点序号更小者所在分区编号不得小于序号更大者。
// 与 van 包推导后的聚合条件等价，但这里完全独立地逐条枚举，便于交叉验证。
func (m *Naive) feasible(z int, c van.Cargo) bool {
	board := make([]van.Cargo, 0, len(m.board)+1)
	for _, g := range m.board {
		board = append(board, g)
	}
	board = append(board, c)
	zoneOf := func(id int) int {
		if id == c.ID {
			return z
		}
		return m.zoneOf[id]
	}
	for i := 0; i < len(board); i++ {
		for j := i + 1; j < len(board); j++ {
			a, b := board[i], board[j]
			if a.Stop == b.Stop {
				continue
			}
			early, late := a, b
			if a.Stop > b.Stop {
				early, late = b, a
			}
			// zone1=车头，早卸货须靠车尾：zone(early) >= zone(late)。
			// 同区(相等)允许混装；唯一违规是 zone(early) < zone(late)。
			if zoneOf(early.ID) < zoneOf(late.ID) {
				return false
			}
		}
	}
	return true
}

// choose 返回最小编号可行分区；否则返回 0 与按严重度归并的原因。
func (m *Naive) choose(c van.Cargo) (int, van.RejectReason) {
	worst := ""
	chosen := 0
	for z := 1; z <= len(m.specs); z++ {
		if !m.feasible(z, c) {
			worst = minSeverity(worst, "order")
			continue
		}
		reason := m.why(z, c)
		if reason == "" {
			chosen = z
			break
		}
		worst = minSeverity(worst, reason)
	}
	if chosen != 0 {
		return chosen, van.ReasonOrder
	}
	return 0, mapReason(worst)
}

// 严重度 order > isolation > overweight > overvolume；
// 归并取严重度最低者，即字符串等级最深者。
func minSeverity(a, b string) string {
	level := map[string]int{
		"order": 0, "isolation": 1, "overweight": 2, "overvolume": 3,
	}
	if a == "" || level[b] > level[a] {
		return b
	}
	return a
}

func mapReason(r string) van.RejectReason {
	switch r {
	case "order":
		return van.ReasonOrder
	case "isolation":
		return van.ReasonIsolation
	case "overweight":
		return van.ReasonOverWeight
	default:
		return van.ReasonOverVolume
	}
}

func (m *Naive) place(z int, c van.Cargo) {
	m.board[c.ID] = c
	m.zoneOf[c.ID] = z
}

// Load 单件装货，返回分区号与错误哨兵。
func (m *Naive) Load(c van.Cargo) (int, error) {
	if !valid(c) {
		return 0, van.ErrInvalid
	}
	if _, ok := m.board[c.ID]; ok {
		return 0, van.ErrDuplicate
	}
	if m.arrived > 0 && c.Stop <= m.arrived {
		return 0, van.ErrStopPassed
	}
	z, reason := m.choose(c)
	if z == 0 {
		return 0, &van.RejectError{Reason: reason}
	}
	m.place(z, c)
	return z, nil
}

// BatchError 与 van.BatchError 对应的朴素错误。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string { return e.Err.Error() }

func (e *BatchError) Unwrap() error { return e.Err }

// LoadBatch 朴素批量：先整体参数校验，再逐件试放，失败回滚。
func (m *Naive) LoadBatch(cs []van.Cargo) (map[int]int, error) {
	for _, c := range cs {
		if !valid(c) {
			return nil, van.ErrInvalid
		}
	}
	seen := map[int]bool{}
	for _, c := range cs {
		if seen[c.ID] {
			return nil, van.ErrInvalid
		}
		seen[c.ID] = true
	}
	snap := m.snapshot()
	for i, c := range cs {
		z, err := m.Load(c)
		if err != nil {
			m.restore(snap)
			return nil, &BatchError{Index: i, Err: err}
		}
		_ = z
	}
	res := make(map[int]int, len(cs))
	for _, c := range cs {
		res[c.ID] = m.zoneOf[c.ID]
	}
	return res, nil
}

type snap struct {
	board   map[int]van.Cargo
	zoneOf  map[int]int
	arrived int
}

func (m *Naive) snapshot() snap {
	b := make(map[int]van.Cargo, len(m.board))
	z := make(map[int]int, len(m.zoneOf))
	for id, c := range m.board {
		b[id] = c
		z[id] = m.zoneOf[id]
	}
	return snap{b, z, m.arrived}
}

func (m *Naive) restore(s snap) {
	m.board = s.board
	m.zoneOf = s.zoneOf
	m.arrived = s.arrived
}

// Unload 朴素卸货。
func (m *Naive) Unload(stop int) ([]int, error) {
	if stop <= 0 {
		return nil, van.ErrInvalid
	}
	if stop <= m.arrived {
		return nil, van.ErrProcessed
	}
	minStop := 0
	for _, c := range m.board {
		if minStop == 0 || c.Stop < minStop {
			minStop = c.Stop
		}
	}
	if minStop != 0 && minStop < stop {
		return nil, van.ErrOrderError
	}
	ids := []int{}
	for id, c := range m.board {
		if c.Stop == stop {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		delete(m.board, id)
		delete(m.zoneOf, id)
	}
	m.arrived = stop
	sort.Ints(ids)
	return ids, nil
}

// Remaining 朴素剩余容量。
func (m *Naive) Remaining() []van.Remaining {
	out := make([]van.Remaining, len(m.specs))
	for i := range m.specs {
		out[i] = van.Remaining{Weight: m.specs[i].WeightLimit, Volume: m.specs[i].VolumeLimit}
	}
	for _, c := range m.board {
		z := m.zoneOf[c.ID] - 1
		out[z].Weight -= c.Weight
		out[z].Volume -= c.Volume
	}
	return out
}

// Location 朴素定位。
func (m *Naive) Location(id int) (int, error) {
	if z, ok := m.zoneOf[id]; ok {
		return z, nil
	}
	return 0, van.ErrNotFound
}

// ArrivedStop 朴素进度。
func (m *Naive) ArrivedStop() int { return m.arrived }

// IsBatchError 便于测试断言。
func IsBatchError(err error) (int, error, bool) {
	var be *BatchError
	if errors.As(err, &be) {
		return be.Index, be.Err, true
	}
	return 0, err, false
}
