// Package slot 管理 OTA 活动的在途名额：任一时刻在途设备数不超过容量。
package slot

import "errors"

// ErrInvalid 表示容量超出 1 到 10^4 的范围。
var ErrInvalid = errors.New("slot: capacity out of range")

const maxCapacity = 10_000

// Slot 是在途名额计数器，非并发安全，由上层串行化。
type Slot struct {
	capacity int
	used     int
}

// New 构造容量为 capacity 的名额计数器。
func New(capacity int) (*Slot, error) {
	if capacity < 1 || capacity > maxCapacity {
		return nil, ErrInvalid
	}
	return &Slot{capacity: capacity}, nil
}

// Acquire 占用一个名额，名额不足时返回 false。
func (s *Slot) Acquire() bool {
	if s.used >= s.capacity {
		return false
	}
	s.used++
	return true
}

// Release 释放一个名额。
func (s *Slot) Release() {
	if s.used > 0 {
		s.used--
	}
}

// Free 返回空闲名额数。
func (s *Slot) Free() int { return s.capacity - s.used }

// Used 返回已占用名额数。
func (s *Slot) Used() int { return s.used }
