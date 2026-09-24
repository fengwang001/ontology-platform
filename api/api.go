// Package api 是因果交付缓冲对外的并发安全入口，仅依赖 cbuf（进而依赖 vc）。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"ontology/cbuf"
	"ontology/vc"
)

// 四类可判定且互不相同的哨兵错误。
var (
	ErrParam  = errors.New("api: invalid parameters") // New 参数非法
	ErrSender = vc.ErrSender                          // 发送方编号越界
	ErrVector = vc.ErrVector                          // 向量非法
	ErrFull   = cbuf.ErrFull                          // 缓冲已满
)

// Msg 是带发送方向量时钟的消息（vc.Msg 的别名）。
type Msg = vc.Msg

// Receiver 可被多 goroutine 并发使用，所有方法均持同一把锁。
type Receiver struct {
	mu sync.Mutex
	cb *cbuf.Buffer
}

// New 构造接收端；n 必须为正、maxBuf 不可为负（0 表示不允许任何缓冲）。
func New(n, maxBuf int) (*Receiver, error) {
	if n <= 0 || maxBuf < 0 {
		return nil, ErrParam
	}
	return &Receiver{cb: cbuf.New(n, maxBuf)}, nil
}

// Receive 处理一次到达，返回本次触发交付的消息（按交付先后）；重复不报错、返回空。
func (r *Receiver) Receive(m Msg) ([]Msg, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cb.Receive(m)
}

// Delivered/Buffered/Local 返回各自状态副本；Dups 返回重复计数。
func (r *Receiver) Delivered() []Msg { r.mu.Lock(); defer r.mu.Unlock(); return r.cb.Delivered() }
func (r *Receiver) Buffered() []Msg  { r.mu.Lock(); defer r.mu.Unlock(); return r.cb.Buffered() }
func (r *Receiver) Local() []int64   { r.mu.Lock(); defer r.mu.Unlock(); return r.cb.Local() }
func (r *Receiver) Dups() int64      { r.mu.Lock(); defer r.mu.Unlock(); return r.cb.Dups() }

// causalOK 核验不变量 2：每条恰好一次、同源序号 1..k 连续、后交付者不得因果在先。
func causalOK(seq []Msg) error {
	seen, maxBy := map[[2]int]bool{}, map[int]int64{}
	for _, m := range seq {
		k := [2]int{m.From, int(m.V[m.From])}
		if seen[k] {
			return errors.New("a message delivered more than once")
		}
		seen[k], maxBy[m.From] = true, max(maxBy[m.From], m.V[m.From])
	}
	for j, mx := range maxBy {
		for v := int64(1); v <= mx; v++ {
			if !seen[[2]int{j, int(v)}] {
				return fmt.Errorf("sender %d seq %d gap", j, v)
			}
		}
	}
	for i := range seq {
		for k := i + 1; k < len(seq); k++ {
			if vc.Before(seq[k].V, seq[i].V) {
				return fmt.Errorf("causal order violated at %d", i)
			}
		}
	}
	return nil
}

// SelfCheck 对第三节内置序列核验不变量 1/2/3 与第四节复杂度界，通过返回 nil。
// 不变量 4（失败不留痕）由测试 TestRejectLeavesNoTrace 钉住。
func (r *Receiver) SelfCheck() error {
	mk := func(f int, v ...int64) Msg { return Msg{From: f, V: v} }
	cases := []struct {
		name    string
		n, b    int
		arr     []Msg
		deliver []Msg
		local   []int64
		dups    int64
	}{
		{"seven", 3, 8,
			[]Msg{mk(1, 2, 2, 0), mk(2, 1, 1, 1), mk(1, 1, 1, 0), mk(0, 2, 0, 0), mk(2, 2, 2, 2), mk(0, 1, 0, 0), mk(1, 1, 1, 0)},
			[]Msg{mk(0, 1, 0, 0), mk(0, 2, 0, 0), mk(1, 1, 1, 0), mk(1, 2, 2, 0), mk(2, 1, 1, 1), mk(2, 2, 2, 2)},
			[]int64{2, 2, 2}, 1},
		{"closed2", 2, 4,
			[]Msg{mk(1, 2, 2), mk(0, 2, 1), mk(1, 1, 1), mk(0, 1, 0), mk(0, 1, 0)},
			[]Msg{mk(0, 1, 0), mk(1, 1, 1), mk(0, 2, 1), mk(1, 2, 2)},
			[]int64{2, 2}, 1},
	}
	for _, c := range cases {
		g, err := New(c.n, c.b) // 不变量 3：闭合集合到齐 → 缓冲空、各交付一次
		if err != nil {
			return err
		}
		for _, m := range c.arr {
			if _, err = g.Receive(m); err != nil {
				return fmt.Errorf("%s: %w", c.name, err)
			}
		}
		d := g.Delivered()
		if !reflect.DeepEqual(d, c.deliver) { // 不变量 1：期望序即朴素参照不动点结果
			return fmt.Errorf("%s: delivered mismatch %v", c.name, d)
		}
		if !slices.Equal(g.Local(), c.local) || len(g.Buffered()) != 0 || g.Dups() != c.dups { // 不变量 3
			return fmt.Errorf("%s: final state mismatch", c.name)
		}
		if err = causalOK(d); err != nil { // 不变量 2
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	for _, c := range []struct {
		n, b int
		m    Msg
		e    error
	}{
		{2, 1, Msg{From: 5, V: []int64{1, 0}}, ErrSender},
		{2, 1, Msg{From: 0, V: []int64{1}}, ErrVector},
		{2, 0, Msg{From: 0, V: []int64{2, 0}}, ErrFull},
	} { // 不变量 4：每类拒绝可判定且拒绝前后快照不变
		h, _ := New(c.n, c.b)
		before := fmt.Sprint(h.Local(), h.Buffered(), h.Delivered(), h.Dups())
		if _, err := h.Receive(c.m); !errors.Is(err, c.e) {
			return err
		}
		if fmt.Sprint(h.Local(), h.Buffered(), h.Delivered(), h.Dups()) != before {
			return errors.New("invariant 4: rejection left a trace")
		}
	}
	if _, err := New(0, 1); !errors.Is(err, ErrParam) { // 不变量 4：参数非法
		return err
	}
	if !cbuf.ComplexityBoundOK() { // 第四节：缓冲检查条数不随 m 线性增长
		return errors.New("complexity bound violated")
	}
	return nil
}
