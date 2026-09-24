// Package api 是事件乱序度观测器的对外接口，依赖 obs。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/obs"
)

// 三类可判定哨兵错误，直接复用 obs 的定义，彼此互不相同。
var (
	ErrInvalidSeq   = obs.ErrInvalidSeq
	ErrFrozen       = obs.ErrFrozen
	ErrInvalidSlack = obs.ErrInvalidSlack
)

// Observer 对外暴露观测器；零值不可用，请用 New 构造。
type Observer struct{ o *obs.Observer }

// New 创建观测器；maxSlack 为负时返回 ErrInvalidSlack，不产生实例。
func New(maxSlack int64) (*Observer, error) {
	o, err := obs.New(maxSlack)
	if err != nil {
		return nil, err
	}
	return &Observer{o: o}, nil
}

func (o *Observer) Feed(seq int64) error { return o.o.Feed(seq) }
func (o *Observer) MaxSeen() int64       { return o.o.MaxSeen() }
func (o *Observer) OutOfOrder() int64    { return o.o.OutOfOrder() }
func (o *Observer) MaxLateness() int64   { return o.o.MaxLateness() }
func (o *Observer) ExceedsSlack() bool   { return o.o.ExceedsSlack() }
func (o *Observer) Freeze() error        { return o.o.Freeze() }

// batch 是朴素批量重算：逐事件与当前最大值比较，返回四项期望值。
func batch(seqs []int64, slack int64) (maxSeen, outN, maxLate int64, exceeds bool) {
	seen := false
	for _, s := range seqs {
		if !seen {
			seen = true
			maxSeen = s
			continue
		}
		switch {
		case s > maxSeen:
			maxSeen = s
		case s < maxSeen:
			late := maxSeen - s
			outN++
			if late > maxLate {
				maxLate = late
			}
			if late > slack {
				exceeds = true
			}
		}
	}
	return maxSeen, outN, maxLate, exceeds
}

// SelfCheck 用一组内置事件序列核验第二节的四条不变量；全部成立返回 nil。
// 它只构造一次性实例，不改变接收者 o 的任何状态。
func (o *Observer) SelfCheck() error {
	// 不变量 1/2/3：内置序列 + 循环生成的随机到达顺序，逐项对比朴素重算。
	sets := [][]int64{
		{10, 10, 5, 12, 11, 13},
		{1, 2, 3},
		{5, 4, 3, 2, 1},
		{3, 1, 4, 1, 5, 9, 2, 6},
	}
	rng := rand.New(rand.NewSource(1))
	for g := 0; g < 20; g++ {
		perm := rng.Perm(1 + rng.Intn(50))
		s := make([]int64, len(perm))
		for i, v := range perm {
			s[i] = int64(v) + 1
		}
		sets = append(sets, s)
	}
	for _, slack := range []int64{0, 3, 10, 100} {
		for _, seqs := range sets {
			c, err := New(slack)
			if err != nil {
				return err
			}
			for _, s := range seqs {
				if err := c.Feed(s); err != nil {
					return err
				}
			}
			mx, on, ml, ex := batch(seqs, slack)
			if c.MaxSeen() != mx || c.OutOfOrder() != on || c.MaxLateness() != ml || c.ExceedsSlack() != ex {
				return fmt.Errorf("batch mismatch slack=%d seqs=%v: got (%d,%d,%d,%v) want (%d,%d,%d,%v)",
					slack, seqs, c.MaxSeen(), c.OutOfOrder(), c.MaxLateness(), c.ExceedsSlack(), mx, on, ml, ex)
			}
		}
	}
	// 不变量 3 专项：late=5 超 slack 仍计数；随后 late=1 不得覆盖历史最大。
	c, _ := New(3)
	for _, s := range []int64{10, 5, 12, 11} {
		if err := c.Feed(s); err != nil {
			return err
		}
	}
	if c.OutOfOrder() != 2 || c.MaxLateness() != 5 || !c.ExceedsSlack() {
		return fmt.Errorf("no-drop/historical got (%d,%d,%v), want (2,5,true)", c.OutOfOrder(), c.MaxLateness(), c.ExceedsSlack())
	}
	// 不变量 4：三类拒绝互不相同；拒绝前后快照一致；被拒前已冻结仍冻结。
	if _, err := New(-1); !errors.Is(err, ErrInvalidSlack) {
		return fmt.Errorf("New(-1)=%v, want ErrInvalidSlack", err)
	}
	d, _ := New(10)
	for _, s := range []int64{10, 5} {
		d.Feed(s)
	}
	snap := func() string {
		return fmt.Sprintf("%d,%d,%d,%v", d.MaxSeen(), d.OutOfOrder(), d.MaxLateness(), d.ExceedsSlack())
	}
	before := snap()
	for _, s := range []int64{0, -1} {
		if err := d.Feed(s); !errors.Is(err, ErrInvalidSeq) {
			return fmt.Errorf("Feed(%d)=%v, want ErrInvalidSeq", s, err)
		}
	}
	if snap() != before {
		return fmt.Errorf("illegal Feed left trace: %s != %s", snap(), before)
	}
	if err := d.Freeze(); err != nil {
		return err
	}
	if err := d.Feed(11); !errors.Is(err, ErrFrozen) {
		return fmt.Errorf("frozen Feed=%v, want ErrFrozen", err)
	}
	if snap() != before {
		return errors.New("frozen Feed left trace")
	}
	if err := d.Feed(1); !errors.Is(err, ErrFrozen) {
		return fmt.Errorf("frozen state lost: %v", err)
	}
	if errors.Is(ErrInvalidSeq, ErrFrozen) || errors.Is(ErrInvalidSeq, ErrInvalidSlack) || errors.Is(ErrFrozen, ErrInvalidSlack) {
		return errors.New("sentinel errors are not distinct")
	}
	return nil
}
