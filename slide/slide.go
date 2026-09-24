// Package slide 基于 mono 提供滑动窗口最大值的对外接口。
package slide

import (
	"errors"

	"ontology/mono"
)

var (
	// ErrNonPositiveWidth 表示窗口宽度为 0 或负数。
	ErrNonPositiveWidth = errors.New("slide: window width must be positive")
	// ErrWidthExceedsLength 表示窗口宽度大于序列长度。
	ErrWidthExceedsLength = errors.New("slide: window width exceeds sequence length")
)

// Slider 维护一条整数序列上宽度为 w 的滑动窗口最大值。
type Slider struct {
	w    int
	next int
	mq   *mono.Queue
}

// New 创建滑窗器；w <= 0 时拒绝并返回 ErrNonPositiveWidth。
func New(w int) (*Slider, error) {
	if w <= 0 {
		return nil, ErrNonPositiveWidth
	}
	return &Slider{w: w, mq: mono.New()}, nil
}

// Feed 喂入一个元素，返回当前窗口的最大值；窗口未满时返回已有元素的最大值。
func (s *Slider) Feed(v int) int {
	_ = s.mq.Push(s.next, v)
	s.mq.Expire(s.next - s.w + 1)
	s.next++
	_, value, _ := s.mq.Max()
	return value
}

// Maxes 对整条序列一次算出所有窗口最大值；w 非法或大于序列长度时返回
// 对应哨兵错误且不产生任何结果。
func Maxes(seq []int, w int) ([]int, error) {
	if w <= 0 {
		return nil, ErrNonPositiveWidth
	}
	if w > len(seq) {
		return nil, ErrWidthExceedsLength
	}
	s, _ := New(w)
	out := make([]int, 0, len(seq)-w+1)
	for i, v := range seq {
		m := s.Feed(v)
		if i >= w-1 {
			out = append(out, m)
		}
	}
	return out, nil
}

// SelfCheck 核验内部单调队列：候选值非严格递减、队首下标在当前窗口内、
// 队长不超过 w、入出队总次数不超过 2*已推入元素数。
func (s *Slider) SelfCheck() error {
	if !s.mq.Healthy(s.next-s.w, s.w) {
		return errors.New("slide: monotonic queue invariant violated")
	}
	return nil
}
