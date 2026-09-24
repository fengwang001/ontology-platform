// Package slide 对外提供滑动窗口最值：Feed 逐元素喂入、
// Maxes 一次算完整条序列、SelfCheck 自检。依赖 mono。
package slide

import (
	"errors"

	"ontology/mono"
)

var (
	// ErrBadWidth 表示窗口宽度为 0 或负数。
	ErrBadWidth = errors.New("slide: window width must be positive")
	// ErrWindowTooWide 表示窗口宽度大于序列长度。
	ErrWindowTooWide = errors.New("slide: window width exceeds sequence length")
)

// Slider 是滑窗器：逐个喂入元素，O(1) 给出当前窗口最大值。
type Slider struct {
	w    int
	q    mono.Queue
	next int
}

// New 创建窗宽为 w 的滑窗器；w<=0 时返回 ErrBadWidth。
func New(w int) (*Slider, error) {
	if w <= 0 {
		return nil, ErrBadWidth
	}
	return &Slider{w: w}, nil
}

// Feed 喂入一个元素并返回当前窗口最大值；窗口未满时返回已到达元素的最大值。
func (s *Slider) Feed(v int) int {
	i := s.next
	s.next++
	_ = s.q.Push(i, v) // next 严格递增，Push 不会失败
	s.q.Evict(i - s.w + 1)
	m, _ := s.q.Max()
	return m
}

// SelfCheck 自检：队列单调、队首下标在当前窗口内、长度不超过 w。
func (s *Slider) SelfCheck() error {
	hi := s.next - 1
	lo := hi - s.w + 1
	if lo < 0 {
		lo = 0
	}
	return s.q.Verify(lo, hi, s.w)
}

// Maxes 一次算出所有窗口最大值：第 j 个输出是 seq[j:j+w] 的最大值。
// w 非法时不产生任何结果。
func Maxes(seq []int, w int) ([]int, error) {
	if w <= 0 {
		return nil, ErrBadWidth
	}
	if w > len(seq) {
		return nil, ErrWindowTooWide
	}
	s, err := New(w)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(seq)-w+1)
	for i, v := range seq {
		m := s.Feed(v)
		if i >= w-1 {
			out = append(out, m)
		}
	}
	return out, nil
}
