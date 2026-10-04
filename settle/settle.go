// Package settle 在 catalog 与 fund 之上编排基本医保、大病保险、
// 医疗救助三层分摊，以及单调时钟、换年与后进先出冲正。
package settle

import (
	"errors"
)

const maxNow = 1_000_000_000
const maxMoney = 10_000_000_000_000

var (
	ErrInvalidParam        = errors.New("settle: invalid parameter")
	ErrClockRollback       = errors.New("settle: clock rollback")
	ErrPersonNotFound      = errors.New("settle: person not found")
	ErrDuplicateSettlement = errors.New("settle: duplicate settlement id")
	ErrItemNotFound        = errors.New("settle: catalog item not found")
	ErrYearClosed          = errors.New("settle: year closed")
	ErrSettlementNotFound  = errors.New("settle: settlement not found")
	ErrAlreadyReversed     = errors.New("settle: settlement already reversed")
	ErrNotLast             = errors.New("settle: settlement is not the last open one")
)

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// segments 返回区间 [lo,hi) 落在 [0,S1)、[S1,S2)、[S2,∞) 的长度。
func segments(lo, hi, S1, S2 int64) (m1, m2, m3 int64) {
	if lo < S1 {
		m1 = min64(hi, S1) - lo
	}
	l2 := max64(lo, S1)
	r2 := min64(hi, S2)
	if l2 < r2 {
		m2 = r2 - l2
	}
	if hi > S2 {
		m3 = hi - max64(lo, S2)
	}
	return m1, m2, m3
}
