// Package cardinality 提供支持加入与撤回的基数估计器。
//
// 小基数时使用显式集合精确计数（稀疏模式）；超过阈值后一次性
// 转为近似草图（稠密模式）且永不回落。草图按确定哈希把每个键
// 落到固定寄存器与秩上，加键递增对应计数、撤键递减；撤键使某个
// 寄存器当前最大秩被删时，该寄存器回落到剩余最大秩而非清零。
package cardinality

import "sync"

// Mode 表示估计器当前所处的计数模式。
type Mode int

const (
	// ModeSparse 稀疏模式：显式集合精确计数。
	ModeSparse Mode = iota
	// ModeDense 稠密模式：近似草图估计，进入后永不回落。
	ModeDense
)

func (m Mode) String() string {
	switch m {
	case ModeSparse:
		return "sparse"
	case ModeDense:
		return "dense"
	default:
		return "unknown"
	}
}

// Estimator 是支持加入与撤回的基数估计器，可并发使用。
type Estimator struct {
	mu        sync.RWMutex
	precision uint8
	threshold int
	mode      Mode
	// present 始终记录当前存在的键，用于精确计数、去重、
	// 拒绝撤回不存在的键以及自检时的批量重算。
	present   map[string]struct{}
	registers []uint8
	// rankCnt[i][r] 记录寄存器 i 上秩为 r 的键的个数，
	// 使撤键后寄存器能回落到剩余最大秩而非清零。
	rankCnt [][]int
}

// New 创建估计器。precision 决定寄存器数量 2^precision，
// threshold 为稀疏模式转稠密模式的基数阈值。
func New(precision uint8, threshold int) (*Estimator, error) {
	if precision < MinPrecision || precision > MaxPrecision {
		return nil, ErrInvalidPrecision
	}
	if threshold < 1 {
		return nil, ErrInvalidThreshold
	}
	return &Estimator{
		precision: precision,
		threshold: threshold,
		mode:      ModeSparse,
		present:   make(map[string]struct{}),
	}, nil
}

// Add 加入一个键。空键被拒绝且不改变任何状态。
func (e *Estimator) Add(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.present[key]; ok {
		return nil
	}
	if e.mode == ModeSparse && len(e.present)+1 > e.threshold {
		e.convertToDenseLocked()
	}
	e.present[key] = struct{}{}
	if e.mode == ModeDense {
		e.addDenseLocked(key)
	}
	return nil
}

// Remove 撤回一个键。撤回不存在的键被拒绝且不改变任何状态。
func (e *Estimator) Remove(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.present[key]; !ok {
		return ErrKeyNotFound
	}
	delete(e.present, key)
	if e.mode == ModeDense {
		e.removeDenseLocked(key)
	}
	return nil
}

// Estimate 返回当前基数估计值。
func (e *Estimator) Estimate() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.mode == ModeSparse {
		return uint64(len(e.present))
	}
	return estimate(e.registers)
}

// Mode 返回当前计数模式。
func (e *Estimator) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// Registers 返回寄存器秩快照（用于测试与核对）。
func (e *Estimator) Registers() []uint8 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.mode == ModeSparse {
		return nil
	}
	out := make([]uint8, len(e.registers))
	copy(out, e.registers)
	return out
}

// Verify 自检：用当前键集合批量重算寄存器并与现值比对。
func (e *Estimator) Verify() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.mode == ModeSparse {
		return nil
	}
	recomputed := make([]uint8, len(e.registers))
	for key := range e.present {
		reg, rank := locate(e.precision, hashKey(key))
		if rank > recomputed[reg] {
			recomputed[reg] = rank
		}
	}
	for i := range recomputed {
		if recomputed[i] != e.registers[i] {
			return ErrVerifyMismatch
		}
	}
	return nil
}

// convertToDenseLocked 一次性把显式集合重放进近似草图，此后永不回落。
func (e *Estimator) convertToDenseLocked() {
	m := 1 << e.precision
	e.registers = make([]uint8, m)
	e.rankCnt = make([][]int, m)
	for i := range e.rankCnt {
		e.rankCnt[i] = make([]int, maxRank(e.precision)+1)
	}
	e.mode = ModeDense
	for key := range e.present {
		e.addDenseLocked(key)
	}
}

// addDenseLocked 递增键对应寄存器上对应秩的计数，并抬升寄存器秩。
func (e *Estimator) addDenseLocked(key string) {
	reg, rank := locate(e.precision, hashKey(key))
	e.rankCnt[reg][rank]++
	if rank > e.registers[reg] {
		e.registers[reg] = rank
	}
}

// removeDenseLocked 递减键对应寄存器上对应秩的计数；
// 当前最大秩被删尽时，寄存器回落到剩余最大秩而非清零。
func (e *Estimator) removeDenseLocked(key string) {
	reg, rank := locate(e.precision, hashKey(key))
	e.rankCnt[reg][rank]--
	if e.registers[reg] != rank || e.rankCnt[reg][rank] > 0 {
		return
	}
	fallback := uint8(0)
	for r := int(rank) - 1; r >= 1; r-- {
		if e.rankCnt[reg][r] > 0 {
			fallback = uint8(r)
			break
		}
	}
	e.registers[reg] = fallback
}
