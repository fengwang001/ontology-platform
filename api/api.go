// Package api 是对外接口层：New/Broadcast/Deliver/VC/SelfCheck。依赖 causal。
package api

import (
	"errors"
	"fmt"

	"ontology/causal"
	"ontology/vc"
)

// 对外暴露同一组哨兵错误。
var (
	ErrNodeOutOfRange = causal.ErrNodeOutOfRange
	ErrUnknownMessage = causal.ErrUnknownMessage
	ErrSelfDelivery   = causal.ErrSelfDelivery
)

// Msg 即 causal.Msg。
type Msg = causal.Msg

// System 是 n 节点因果广播系统的对外句柄。
type System struct{ sys *causal.System }

// New 创建 n 节点系统。
func New(n int) *System { return &System{sys: causal.New(n)} }

// Broadcast 见 causal.System.Broadcast。
func (s *System) Broadcast(from int) (Msg, error) { return s.sys.Broadcast(from) }

// Deliver 见 causal.System.Deliver。
func (s *System) Deliver(node int, m Msg) (bool, error) { return s.sys.Deliver(node, m) }

// VC 返回节点 node 当前向量时钟的副本。
func (s *System) VC(node int) ([]int, error) {
	v, err := s.sys.VC(node)
	return []int(v), err
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (s *System) SelfCheck() error {
	for _, chk := range []func() error{checkNaive, checkCausalOrder, checkNoSkip, checkFailureNoTrace} {
		if err := chk(); err != nil {
			return err
		}
	}
	return nil
}

// 不变量 1：VC[p][q] 等于 p 已投递消息中发送者为 q 的条数（朴素重算）。
func checkNaive() error {
	s := New(3)
	var msgs []Msg
	for i := 0; i < 4; i++ { // 交错广播，制造跨节点依赖
		m, err := s.Broadcast(i % 3)
		if err != nil {
			return err
		}
		msgs = append(msgs, m)
	}
	count := [3][3]int{} // count[节点][发送者]
	for _, m := range msgs {
		for p := 0; p < 3; p++ {
			d := p == m.From // 自己广播视作已投递
			if !d {
				var err error
				if d, err = s.Deliver(p, m); err != nil {
					return err
				}
			}
			if d {
				count[p][m.From]++
			}
		}
	}
	for p := 0; p < 3; p++ {
		got, _ := s.VC(p)
		for q := 0; q < 3; q++ {
			if got[q] != count[p][q] {
				return fmt.Errorf("不变量1: VC[%d][%d]=%d 朴素重算=%d", p, q, got[q], count[p][q])
			}
		}
	}
	return nil
}

// 不变量 2：因果序——依赖未投递时，后继消息必须阻塞。
func checkCausalOrder() error {
	s := New(2)
	m1, _ := s.Broadcast(0)
	m2, _ := s.Broadcast(0) // m2 的 ts 已包含 m1
	if d, _ := s.Deliver(1, m2); d {
		return errors.New("不变量2: m1 未投递时 m2 被投递")
	}
	if d, _ := s.Deliver(1, m1); !d {
		return errors.New("不变量2: m1 应可投递")
	}
	if d, _ := s.Deliver(1, m2); !d {
		return errors.New("不变量2: m1 投递后 m2 应可投递")
	}
	return nil
}

// 不变量 3：同源不跳号——q 的第 2 条不能在第 1 条之前投递，分量逐条递增。
func checkNoSkip() error {
	s := New(2)
	m1, _ := s.Broadcast(0)
	m2, _ := s.Broadcast(0)
	if d, _ := s.Deliver(1, m2); d {
		return errors.New("不变量3: 跳过第 1 条投递了第 2 条")
	}
	for i, m := range []Msg{m1, m2} {
		if d, _ := s.Deliver(1, m); !d {
			return fmt.Errorf("不变量3: 第 %d 条应可投递", i+1)
		}
		v, _ := s.VC(1)
		if v[0] != i+1 {
			return fmt.Errorf("不变量3: 投递第 %d 条后 VC[1][0]=%d", i+1, v[0])
		}
	}
	return nil
}

// 不变量 4：失败不留痕——三类互不相同的可判定错误，状态不变、可继续用。
func checkFailureNoTrace() error {
	s := New(2)
	m1, _ := s.Broadcast(0)
	before0, _ := s.VC(0)
	before1, _ := s.VC(1)
	if _, err := s.Broadcast(5); !errors.Is(err, ErrNodeOutOfRange) {
		return errors.New("不变量4: 越界广播应报 ErrNodeOutOfRange")
	}
	if _, err := s.Deliver(1, Msg{From: 0, TS: vc.Vector{9, 0}}); !errors.Is(err, ErrUnknownMessage) {
		return errors.New("不变量4: 未知消息应报 ErrUnknownMessage")
	}
	if _, err := s.Deliver(0, m1); !errors.Is(err, ErrSelfDelivery) {
		return errors.New("不变量4: 自投递应报 ErrSelfDelivery")
	}
	if ErrNodeOutOfRange == ErrUnknownMessage || ErrUnknownMessage == ErrSelfDelivery || ErrNodeOutOfRange == ErrSelfDelivery {
		return errors.New("不变量4: 三类错误必须互不相同")
	}
	same := func(b []int, p int) bool { a, _ := s.VC(p); return vc.Vector(b).Equal(a) }
	if !same(before0, 0) || !same(before1, 1) {
		return errors.New("不变量4: 被拒操作改变了状态")
	}
	if d, err := s.Deliver(1, m1); err != nil || !d {
		return errors.New("不变量4: 被拒后系统不可继续正常使用")
	}
	return nil
}
