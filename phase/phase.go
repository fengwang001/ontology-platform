// Package phase 实现连续交易与波动性中断阶段机。
package phase

import "ontology/band"

// Phase 是单标的的中断阶段机（不含价格判断）。
type Phase struct {
	halted    bool
	he        int64
	extends   int
	halts     int
	haltLimit int
	maxExtend int
	duration  int64
	lastCall  int64
}

// New 构造阶段机。
func New(duration int64, maxExtend, haltLimit int, lastCall int64) *Phase {
	return &Phase{haltLimit: haltLimit, maxExtend: maxExtend, duration: duration, lastCall: lastCall}
}

// Begin 进入中断，结束时刻 he=min(now+duration,lastCall)，中断计数加一。
func (p *Phase) Begin(now int64) int64 {
	p.halted = true
	p.halts++
	p.extends = 0
	p.he = now + p.duration
	if p.lastCall < p.he {
		p.he = p.lastCall
	}
	return p.he
}

// Halted 报告是否处于中断。
func (p *Phase) Halted() bool { return p.halted }

// HaltCount 报告当日已触发的中断次数。
func (p *Phase) HaltCount() int { return p.halts }

// End 报告当前中断的结束时刻。
func (p *Phase) End() int64 { return p.he }

// Extensions 报告本次中断已延长次数。
func (p *Phase) Extensions() int { return p.extends }

// CanExtend 报告在恢复价相对静态带 de（基点）超带时本次中断是否还能延长。
// 条件全部满足：恢复价超 De 带、延长次数未达 X、当前 he 尚未到尾盘起点。
func (p *Phase) CanExtend(rs, price, de int64) bool {
	return p.halted && band.Over(price, rs, de) && p.extends < p.maxExtend && p.he < p.lastCall
}

// Extend 延长一次中断，返回新的结束时刻。
func (p *Phase) Extend() int64 {
	p.extends++
	p.he += p.duration
	if p.lastCall < p.he {
		p.he = p.lastCall
	}
	return p.he
}

// Recover 恢复连续交易。
func (p *Phase) Recover() {
	p.halted = false
	p.he = 0
	p.extends = 0
}
