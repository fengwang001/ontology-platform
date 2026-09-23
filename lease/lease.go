// Package lease 定义号段租约：起始、长度、到期时间。
package lease

import (
	"errors"
	"fmt"
	"time"
)

// 可判定错误。
var (
	ErrInvalidLength = errors.New("lease: segment length must be positive")
	ErrOverflow      = errors.New("lease: segment end overflows uint64")
)

// Lease 表示租用的号段 [Start, Start+Len)，Expiry 之后作废。
type Lease struct {
	Start  uint64
	Len    uint64
	Expiry time.Time
}

// New 构造租约并校验边界：长度必须为正，末端不得溢出 uint64。
func New(start uint64, length int64, expiry time.Time) (Lease, error) {
	if length <= 0 {
		return Lease{}, fmt.Errorf("%w: %d", ErrInvalidLength, length)
	}
	l := uint64(length)
	if start > ^uint64(0)-l {
		return Lease{}, fmt.Errorf("%w: %d + %d", ErrOverflow, start, l)
	}
	return Lease{Start: start, Len: l, Expiry: expiry}, nil
}

// End 返回号段开区间末端（即下一个可租起点）。
func (l Lease) End() uint64 { return l.Start + l.Len }

// ValidAt 报告 t 时刻租约是否仍有效。
func (l Lease) ValidAt(t time.Time) bool { return t.Before(l.Expiry) }
