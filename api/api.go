// Package api 对外提供广播状态模式下的规则版本化处理。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/inst"
	"ontology/rule"
)

// 可判定哨兵错误，四类互不相同（规则非法见 rule.ErrEmptyID / rule.ErrNoSuch）。
var ErrDeliver, ErrBadKey, ErrBufFull = errors.New("api: deliver out of range"), errors.New("api: negative key"), errors.New("api: buffer full")

// System 是广播状态处理器，全部方法可并发调用。
type System struct {
	mu  sync.Mutex
	log rule.Log // 全局发布日志，Len 即 G
	ins []*inst.Instance
	out []rule.Hit  // 迄今全部命中
	acc []rule.Data // 已接受的数据
}

func locked[T any](s *System, f func() T) T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return f()
}

func New(P, maxBuffered int) *System {
	s := &System{ins: make([]*inst.Instance, P)}
	for i := range s.ins {
		s.ins[i] = inst.New(i, maxBuffered)
	}
	return s
}

func (s *System) Publish(u rule.Update) error {
	return locked(s, func() error { return s.log.Publish(u) })
}

func (s *System) Deliver(i, n int) error {
	return locked(s, func() error {
		if i < 0 || i >= len(s.ins) || n <= 0 || s.ins[i].Version()+n > s.log.Len() {
			return ErrDeliver
		}
		for ; n > 0; n-- {
			s.out = append(s.out, s.ins[i].Apply(s.log.At(s.ins[i].Version()))...)
		}
		return nil
	})
}

func (s *System) Send(key, val int64) error {
	return locked(s, func() error {
		if key < 0 {
			return ErrBadKey
		}
		d := rule.Data{Key: key, Val: val, Tag: s.log.Len(), Inst: int(key % int64(len(s.ins)))}
		if hits, ok := s.ins[d.Inst].Accept(d); ok {
			s.out, s.acc = append(s.out, hits...), append(s.acc, d)
			return nil
		}
		return ErrBufFull
	})
}

func (s *System) Output() []rule.Hit {
	return locked(s, func() []rule.Hit { return append([]rule.Hit(nil), s.out...) })
}

func (s *System) Versions() (int, []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := make([]int, len(s.ins))
	for i, in := range s.ins {
		vs[i] = in.Version()
	}
	return s.log.Len(), vs
}

func (s *System) BufTags(i int) []int { return locked(s, func() []int { return s.ins[i].BufTags() }) }

func deliverAll(s *System) error {
	g, vs := s.Versions()
	for i, v := range vs {
		if v < g {
			if err := s.Deliver(i, g-v); err != nil {
				return err
			}
		}
	}
	return nil
}

func pub(u rule.Update) func(*System) error { return func(x *System) error { return x.Publish(u) } }
func snd(k, v int64) func(*System) error    { return func(x *System) error { return x.Send(k, v) } }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (s *System) SelfCheck() error {
	ops := []func(*System) error{pub(rule.Put("r1", 10)), snd(0, 15), pub(rule.Put("r2", 5)), pub(rule.Delete("r1")), snd(1, 12), snd(2, 7)}
	var outs [][]rule.Hit
	for _, eager := range []bool{false, true} { // 不变量 2/3：两种投递穿插，逐步核验
		x := New(2, 8)
		for _, f := range ops {
			if err := f(x); err != nil {
				return err
			}
			if eager {
				if err := deliverAll(x); err != nil {
					return err
				}
			}
			g, vs := x.Versions()
			for i := range vs {
				if err := x.ins[i].CheckAgainst(g); err != nil {
					return err
				}
			}
		}
		if err := deliverAll(x); err != nil {
			return err
		}
		outs = append(outs, x.Output())
		if !eager { // 不变量 1：朴素参照
			outs = append(outs, rule.Naive(x.log.Updates(), x.acc))
		}
	}
	if !rule.SameHits(outs[0], outs[1]) || !rule.SameHits(outs[0], outs[2]) {
		return errors.New("selfcheck: 不变量1/2 失败")
	}
	z := New(2, 1)
	_ = z.Publish(rule.Put("r1", 10)) // 必成功
	_ = z.Send(0, 1)                  // 占满实例 0 缓冲（v_0=0 < G=1）
	before := fmt.Sprint(z.Versions()) + fmt.Sprint(z.Output(), z.BufTags(0), z.BufTags(1))
	bads := []struct{ got, want error }{
		{z.Publish(rule.Put("", 1)), rule.ErrEmptyID},
		{z.Publish(rule.Delete("x")), rule.ErrNoSuch},
		{z.Deliver(0, 2), ErrDeliver},
		{z.Send(-1, 1), ErrBadKey},
		{z.Send(2, 1), ErrBufFull},
	}
	for _, b := range bads {
		if !errors.Is(b.got, b.want) || fmt.Sprint(z.Versions())+fmt.Sprint(z.Output(), z.BufTags(0), z.BufTags(1)) != before {
			return errors.New("selfcheck: 不变量4 失败")
		}
	}
	return z.Deliver(0, 1)
}
