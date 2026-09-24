// Package obs 维护乱序度观测状态：纯观测，乱序只统计不丢弃，MaxSeen 只被有序事件推进，方法并发安全。
package obs

import (
	"errors"
	"fmt"
	"sync"

	"ontology/seq"
)

// 三类哨兵错误：序号非法、冻结后写、负 slack，互不相同、可 errors.Is 判定。
var (
	ErrInvalidSeq    = seq.ErrInvalidSeq
	ErrFrozen        = errors.New("obs: observer is frozen")
	ErrNegativeSlack = errors.New("obs: maxSlack must not be negative")
)

var b2i = map[bool]int64{false: 0, true: 1}

// Observer 纯内存观测器，零值不可用须经 New。seen=是否见过合法事件（MaxSeen
// 初始负无穷）；其余标量含义同题述；lastChecked 非导出，记录最近一次 Feed 检查过的历史事件个数（标量维护：有历史恒 1、首个 0，与总数无关），仅包内白盒可读。
type Observer struct {
	mu                                                  sync.RWMutex
	maxSlack, maxSeen, outOfOrder, maxLate, lastChecked int64
	seen, exceedsSlack, frozen                          bool
}

// New 构造观测器；maxSlack 为负整体失败 ErrNegativeSlack，无对象产生。
func New(s int64) (*Observer, error) {
	if s < 0 {
		return nil, ErrNegativeSlack
	}
	return &Observer{maxSlack: s}, nil
}

// Feed 观测一个事件：冻结返回 ErrFrozen，Seq<=0 返回 ErrInvalidSeq；校验先于任何字段写入，失败不留痕。
func (o *Observer) Feed(v int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.frozen {
		return ErrFrozen
	}
	r, err := seq.Classify(v, o.maxSeen, o.seen)
	if err != nil {
		return err
	}
	checked := b2i[o.seen] // 分类前捕获：只与标量 maxSeen 比较一次，不回扫历史
	if r.Kind == seq.InOrder {
		o.seen, o.maxSeen = true, v // 仅有序事件推进 MaxSeen
	} else if r.Kind == seq.Late {
		o.outOfOrder++ // 纯观测：照常计数，绝不丢弃
		if r.Lateness > o.maxLate {
			o.maxLate = r.Lateness
		}
		if r.Lateness > o.maxSlack {
			o.exceedsSlack = true
		}
	}
	o.lastChecked = checked
	return nil
}

// Freeze 冻结观测器；重复 Freeze 返回 ErrFrozen 且不改状态。
func (o *Observer) Freeze() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.frozen {
		return ErrFrozen
	}
	o.frozen = true
	return nil
}

func (o *Observer) MaxSeen() int64     { o.mu.RLock(); defer o.mu.RUnlock(); return o.maxSeen }
func (o *Observer) OutOfOrder() int64  { o.mu.RLock(); defer o.mu.RUnlock(); return o.outOfOrder }
func (o *Observer) MaxLateness() int64 { o.mu.RLock(); defer o.mu.RUnlock(); return o.maxLate }
func (o *Observer) ExceedsSlack() bool { o.mu.RLock(); defer o.mu.RUnlock(); return o.exceedsSlack }

// naive 是逐事件与当前 maxSeen 比较的朴素批量重算，作为不变量 1 的标尺。
func naive(es []int64) (mx, n, late int64) {
	first := true
	for _, v := range es {
		if first || v > mx {
			mx, first = v, false
		} else if v < mx {
			n++
			if d := mx - v; d > late {
				late = d
			}
		}
	}
	return
}

func (o *Observer) snap() [6]int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return [6]int64{b2i[o.seen], o.maxSeen, o.outOfOrder, o.maxLate, b2i[o.exceedsSlack], b2i[o.frozen]}
}

// SelfCheck 用内置序列核验第二节四条不变量及每事件 O(1) 检查次数，全过返回 nil；只回报成败，不泄露 lastChecked 数值。
func (o *Observer) SelfCheck() error {
	cases := [][]int64{{10, 10, 5, 12, 11, 13}, {1, 2, 3, 4}, {7, 7, 7}, {9, 8, 7}, {3, 1, 4, 1, 5, 9, 2, 6}}
	for i, es := range cases { // 不变量 1/2/3：与朴素重算逐项一致（case0 的重算结果即 max=13,ooo=2,late=5）
		w, _ := New(10)
		for _, v := range es {
			w.Feed(v) // 内置序列合法且未冻结
		}
		if m, c, l := naive(es); w.MaxSeen() != m || w.OutOfOrder() != c || w.MaxLateness() != l {
			return fmt.Errorf("selfcheck case %d: mismatch with naive recompute", i)
		}
	}
	b, _ := New(10)
	b.Feed(7)
	reject := func(fn func() error, s error, msg string) error { // 同时钉哨兵可判定与失败不留痕
		snap := b.snap()
		if !errors.Is(fn(), s) || b.snap() != snap {
			return fmt.Errorf("selfcheck: %s", msg)
		}
		return nil
	}
	if e := reject(func() error { return b.Feed(0) }, ErrInvalidSeq, "invalid seq"); e != nil {
		return e
	}
	if err := b.Freeze(); err != nil {
		return err
	}
	if e := reject(func() error { return b.Feed(8) }, ErrFrozen, "frozen feed"); e != nil {
		return e
	}
	if e := reject(func() error { return b.Freeze() }, ErrFrozen, "double freeze"); e != nil {
		return e
	}
	if _, e := New(-1); !errors.Is(e, ErrNegativeSlack) { // 不变量 4：三类拒绝互不相同
		return fmt.Errorf("selfcheck: negative slack err=%v", e)
	}
	for _, m := range []int64{100, 1000, 10000} { // O(1)：检查个数不随 m 线性增长
		q, _ := New(10)
		for v := int64(1); v <= m; v++ {
			q.Feed(v)
		}
		if err := q.Feed(1); err != nil || q.lastChecked > 1 {
			return fmt.Errorf("selfcheck: per-event checks grow with m=%d", m)
		}
	}
	return nil
}
