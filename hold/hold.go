// Package hold 检验点首过良率门禁与挂起。
package hold

import (
	"errors"
	"fmt"
	"sync"
)

// ErrInvalidParam 参数非法：Y 不在 1..100 或 Nmin 不在 1..1e9。
var ErrInvalidParam = errors.New("hold: invalid parameter")

const (
	// MaxYield 首过良率阈值上限（百分数）。
	MaxYield = 100
	// MaxMinSample 最小样本量上限。
	MaxMinSample = 1_000_000_000
)

type stat struct {
	fGood  int64
	fTotal int64
}

// Gate 单张工单的首过良率门禁。
// 首过统计按检验点各自独立累计，挂起是工单级状态。
// Gate 自身并发安全，可独立使用。
type Gate struct {
	mu        sync.Mutex
	yield     int64
	minSample int64
	stats     map[int]*stat
	suspended bool
}

// NewGate 创建门禁：yield 为 1..100 的首过良率阈值（百分数），
// minSample 为 1..1e9 的最小样本量。
func NewGate(yield, minSample int64) (*Gate, error) {
	if yield < 1 || yield > MaxYield || minSample < 1 || minSample > MaxMinSample {
		return nil, fmt.Errorf("%w: yield=%d minSample=%d", ErrInvalidParam, yield, minSample)
	}
	return &Gate{yield: yield, minSample: minSample, stats: make(map[int]*stat)}, nil
}

// Record 落账一次检验点 op 的首过报工（仅 k=0 层），
// 落账之后判定：fTotal≥minSample 且 fGood*100<yield*fTotal 则挂起。
// 恰等阈值不挂起。返回本次是否触发了挂起；触发挂起的本次报工本身有效。
func (g *Gate) Record(op int, good, total int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.stats[op]
	if s == nil {
		s = &stat{}
		g.stats[op] = s
	}
	s.fGood += good
	s.fTotal += total
	if s.fTotal >= g.minSample && s.fGood*100 < g.yield*s.fTotal {
		g.suspended = true
		return true
	}
	return false
}

// Suspended 报告工单是否处于挂起。
func (g *Gate) Suspended() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.suspended
}

// Resume 解除挂起，并清零所有检验点的首过统计以重新累计。
func (g *Gate) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.suspended = false
	g.stats = make(map[int]*stat)
}

// Stats 返回检验点 op 的首过统计（审计与测试用）。
func (g *Gate) Stats(op int) (fGood, fTotal int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.stats[op]; s != nil {
		return s.fGood, s.fTotal
	}
	return 0, 0
}
