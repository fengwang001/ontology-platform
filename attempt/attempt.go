// Package attempt 记录执行轮次与重跑账。
package attempt

import "errors"

// ErrExhausted 表示已用轮次达到上限，重跑超限。
var ErrExhausted = errors.New("attempt: rerun limit exceeded")

// Record 为一次重跑的账目：进入的轮次与被置回 Pending 的作业下标。
type Record struct {
	Round int
	Jobs  []int
}

// Ledger 为轮次台账。Start 时 Begin 进入第 1 轮，每次重跑 Commit 一轮。
type Ledger struct {
	max   int
	round int
	recs  []Record
}

// New 创建台账，max 为最多轮次。
func New(max int) *Ledger {
	return &Ledger{max: max}
}

// Max 返回最多轮次。
func (l *Ledger) Max() int { return l.max }

// Round 返回当前轮次（未 Begin 时为 0）。
func (l *Ledger) Round() int { return l.round }

// Begin 进入第 1 轮。
func (l *Ledger) Begin() { l.round = 1 }

// Exhausted 报告已用轮次是否达到上限。
func (l *Ledger) Exhausted() bool { return l.round >= l.max }

// Commit 记录一次重跑：轮次加 1 并记账。超限返回 ErrExhausted 且不改状态。
func (l *Ledger) Commit(jobs []int) error {
	if l.Exhausted() {
		return ErrExhausted
	}
	l.round++
	l.recs = append(l.recs, Record{Round: l.round, Jobs: append([]int(nil), jobs...)})
	return nil
}

// Records 返回重跑账副本。
func (l *Ledger) Records() []Record {
	return append([]Record(nil), l.recs...)
}
