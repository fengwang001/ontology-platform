// Package api 是对外的 FIFO 乱序防护缓冲区入口（进程内存、仅标准库、并发安全）。
// 依赖方向：api → dispatch → fifo。
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/dispatch"
)

// 三类互不相同的可判定哨兵错误；拒绝操作整体失败且不留痕。
var (
	ErrEmptyKey     = errors.New("api: key must not be empty")
	ErrInvalidMax   = errors.New("api: maxInFlight must be positive")
	ErrBackpressure = dispatch.ErrBackpressure
)

// ReorderBuffer 是并发安全的按 key FIFO 乱序防护缓冲区。
type ReorderBuffer struct {
	max int
	hub *dispatch.Hub
}

// New 创建每 key 缓冲上限为 maxInFlight 的缓冲区；非正返回 ErrInvalidMax。
func New(maxInFlight int) (*ReorderBuffer, error) {
	if maxInFlight <= 0 {
		return nil, ErrInvalidMax
	}
	return &ReorderBuffer{max: maxInFlight, hub: dispatch.New(maxInFlight)}, nil
}

// Feed 投递 (key, seq)，返回本次新发射的序号（含级联）。空 key 返回 ErrEmptyKey，
// 缓冲将超限返回 ErrBackpressure；二者均不改变任何状态。
func (r *ReorderBuffer) Feed(key string, seq int64) ([]int64, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	return r.hub.Feed(key, seq)
}

// Emitted 返回某 key 已发射序列的升序副本（形如 1,2,...,n，无空洞）。
func (r *ReorderBuffer) Emitted(key string) []int64 { return r.hub.Emitted(key) }

// Buffered 返回某 key 当前在途缓冲的数量。
func (r *ReorderBuffer) Buffered(key string) int { return r.hub.Buffered(key) }

// Dropped 返回所有 key 的重复丢弃总数。
func (r *ReorderBuffer) Dropped() int64 { return r.hub.Dropped() }

var eightStep = []struct { // NOTES 第三节规定的内置序列（maxInFlight=3，key=K）
	seq          int64
	emit, buffer []int64
}{
	{5, nil, []int64{5}},
	{2, nil, []int64{2, 5}},
	{4, nil, []int64{2, 4, 5}},
	{1, []int64{1, 2}, []int64{4, 5}}, // 第 4 步：发射 1 并级联放行 2
	{3, []int64{3, 4, 5}, nil},        // 第 5 步：级联放行连续前缀
	{6, []int64{6}, nil},
	{7, []int64{7}, nil},
	{2, nil, nil}, // 重复丢弃
}

// checkNaive 用独立朴素参照（map 接受集合 + next 推进）回放序列，逐拍比对
// 发射结果、错误类型、已发射前缀长度与丢弃数；数据结构与实现完全不同。
func checkNaive(max int, stream []int64) error {
	rb, _ := New(max)
	acc := map[int64]bool{}
	var next, dropped int64 = 1, 0
	for _, s := range stream {
		got, gerr := rb.Feed("P", s)
		var want []int64
		var werr error
		switch {
		case s < next: // 朴素：重复
			dropped++
		case s > next && !acc[s]: // 朴素：按在途计数判背压
			n := 0
			for v := range acc {
				if v >= next {
					n++
				}
			}
			if n >= max {
				werr = ErrBackpressure
			}
		}
		if werr == nil && s >= next {
			acc[s] = true
			for acc[next] { // 朴素：放行已接受集合的连续前缀
				want = append(want, next)
				next++
			}
		}
		if !errors.Is(gerr, werr) || !slices.Equal(got, want) ||
			rb.Dropped() != dropped || int64(len(rb.Emitted("P"))) != next-1 {
			return fmt.Errorf("seq %d: got (%v,%v) d=%d, want (%v,%v) d=%d emitted=%v",
				s, got, gerr, rb.Dropped(), want, werr, dropped, rb.Emitted("P"))
		}
	}
	return nil
}

// SelfCheck 对内置事件序列核验四条不变量，全部成立返回 nil，否则返回首个失配。
func (r *ReorderBuffer) SelfCheck() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidMax) { // 不变量 4：参数非法
		return fmt.Errorf("New(0): %v", err)
	}
	rb, _ := New(3)
	var emitted []int64
	for i, row := range eightStep { // 不变量 1、3：逐步核对发射、级联、缓冲上界
		got, err := rb.Feed("K", row.seq)
		buf := rb.hub.BufferedSnapshot("K")
		if err != nil || !slices.Equal(got, row.emit) || !slices.Equal(buf, row.buffer) ||
			rb.Buffered("K") > 3 {
			return fmt.Errorf("step %d: got (%v,%v) buf %v, want %v buf %v (len=%d)",
				i+1, got, err, buf, row.emit, row.buffer, rb.Buffered("K"))
		}
		emitted = append(emitted, got...)
	}
	if !slices.Equal(emitted, []int64{1, 2, 3, 4, 5, 6, 7}) || rb.Dropped() != 1 {
		return fmt.Errorf("final: emitted=%v dropped=%d", emitted, rb.Dropped())
	}
	for _, s := range [][]int64{ // 不变量 2：与朴素参照一致
		{5, 2, 4, 1, 3, 6, 7, 2}, {3, 7, 2, 1, 4, 9, 5, 6, 6, 1, 8},
		{1, 2, 3, 4, 5}, {9, 8, 7, 6, 5, 4, 3, 2, 1},
	} {
		if err := checkNaive(r.max, s); err != nil {
			return err
		}
	}
	snap := func() [3]int64 { // 不变量 4：被拒操作不留痕
		return [3]int64{int64(len(rb.Emitted("K"))), int64(rb.Buffered("K")), rb.Dropped()}
	}
	before := snap()
	if _, e := rb.Feed("", 1); !errors.Is(e, ErrEmptyKey) {
		return fmt.Errorf("empty key: %v", e)
	}
	full, _ := New(1)
	_, _ = full.Feed("Q", 3)
	if _, e := full.Feed("Q", 2); !errors.Is(e, ErrBackpressure) {
		return fmt.Errorf("backpressure: %v", e)
	}
	if after := snap(); after != before || full.Buffered("Q") != 1 || full.Dropped() != 0 {
		return errors.New("rejected op left a trace")
	}
	return nil
}
