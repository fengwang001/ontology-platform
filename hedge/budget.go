package hedge

import (
	"math"
	"sync"
)

// Budget 全局对冲预算，多次调用共享。
// 任意时刻已发出对冲总数 <= fixed + floor(ratio * 已接受调用总数)。
type Budget struct {
	mu       sync.Mutex
	fixed    int
	ratio    float64
	accepted int
	hedges   int
	violated bool
}

func NewBudget(fixed int, ratio float64) *Budget {
	return &Budget{fixed: fixed, ratio: ratio}
}

// accept 记录一次通过校验、被接受的调用，会提高预算上限。
func (b *Budget) accept() {
	b.mu.Lock()
	b.accepted++
	b.checkLocked()
	b.mu.Unlock()
}

// tryHedge 尝试消耗一次对冲额度，成功返回 true。
func (b *Budget) tryHedge() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hedges >= b.allowanceLocked() {
		return false
	}
	b.hedges++
	b.checkLocked()
	return true
}

func (b *Budget) allowanceLocked() int {
	return b.fixed + int(math.Floor(b.ratio*float64(b.accepted)))
}

func (b *Budget) checkLocked() {
	if b.hedges > b.allowanceLocked() {
		b.violated = true
	}
}

// Snapshot 返回 (已接受调用数, 已发出对冲数, 当前额度, 是否曾违反预算)。
func (b *Budget) Snapshot() (accepted, hedges, allowance int, violated bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.accepted, b.hedges, b.allowanceLocked(), b.violated
}
