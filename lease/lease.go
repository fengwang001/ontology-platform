// Package lease 定义号段租约：起始、长度、到期时间及其判定。
package lease

import (
	"errors"
	"time"
)

// ErrInvalidLength 表示段长为零或负。
var ErrInvalidLength = errors.New("lease: 段长必须为正")

// Lease 是号段 [Start, Start+Length) 的使用权，Expiry 之后作废。
type Lease struct {
	Start  uint64
	Length uint64
	Expiry time.Time
}

// New 构造租约；length <= 0 时返回 ErrInvalidLength。
func New(start uint64, length int64, expiry time.Time) (Lease, error) {
	if length <= 0 {
		return Lease{}, ErrInvalidLength
	}
	return Lease{Start: start, Length: uint64(length), Expiry: expiry}, nil
}

// End 返回号段末尾（开区间端点）。
func (l Lease) End() uint64 { return l.Start + l.Length }

// Expired 判定 now 时刻租约是否已到期（到期即作废）。
func (l Lease) Expired(now time.Time) bool { return !now.Before(l.Expiry) }

// Remaining 返回从 next 起段内尚未分发的号数。
func (l Lease) Remaining(next uint64) uint64 {
	if next >= l.End() {
		return 0
	}
	return l.End() - next
}
