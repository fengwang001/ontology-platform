// Package api 是 Go-Back-N 发送端的对外门面（固定签名、哨兵错误、SelfCheck）；只依赖 gbn。
package api

import (
	"errors"
	"sync"

	"ontology/gbn"
)

// 三类可判定哨兵错误，彼此不同，errors.Is 可判。
var (
	ErrBadWindow  = gbn.ErrBadWindow  // W 非正
	ErrBadAck     = gbn.ErrBadAck     // ACK 为负
	ErrWindowFull = gbn.ErrWindowFull // 窗口已满仍 Send
)

// Sender 是可靠传输发送端（GBN + 累计 ACK + 超时重传），状态只在进程内存。
type Sender struct{ g *gbn.Sender }

// New 创建窗口为 W 的发送端；W<=0 返回 ErrBadWindow，不留任何状态。
func New(W int) (*Sender, error) {
	g, err := gbn.New(W)
	if err != nil {
		return nil, err
	}
	return &Sender{g: g}, nil
}

// Send 窗口未满时发送下一段并返回序号（0 起单调递增，不回绕）；满则 ErrWindowFull 且不留痕。
func (s *Sender) Send() (int, error) {
	seq, err := s.g.Send()
	if err != nil {
		return 0, err
	}
	return int(seq), nil
}

// Ack 处理累计 ACK：a 表示序号 < a 的段均已收到；a<0 拒绝，a<=base 幂等，否则 base 单调推进。
func (s *Sender) Ack(a int64) error { return s.g.Ack(a) }

// Timeout 返回 [base,next) 全部未确认段的有序切片，base/next 不变；空为 nil。
func (s *Sender) Timeout() []int64 { return s.g.Timeout() }

// Base 返回已确认下界。
func (s *Sender) Base() int64 { return s.g.Base() }

// Next 返回下一个待发送段序号。
func (s *Sender) Next() int64 { return s.g.Next() }

// Unacked 返回未确认段的有序切片副本。
func (s *Sender) Unacked() []int64 { return s.g.Unacked() }

type snapshot struct {
	base, next int64
	unacked    []int64
}

func (s *Sender) snapshot() snapshot { return snapshot{s.Base(), s.Next(), s.Unacked()} }
func eqSnap(a, b snapshot) bool {
	if a.base != b.base || a.next != b.next || len(a.unacked) != len(b.unacked) {
		return false
	}
	for i := range a.unacked {
		if a.unacked[i] != b.unacked[i] {
			return false
		}
	}
	return true
}

// SelfCheck 用内置序列核验第二节四条不变量，并附复杂度与并发只读一致性；内部自建实例、不改动接收者、不泄露计数器数值。
func (s *Sender) SelfCheck() bool {
	t, err := New(4)
	if err != nil {
		return false
	}
	s = t
	want := []snapshot{
		{0, 1, []int64{0}}, {0, 2, []int64{0, 1}}, {0, 3, []int64{0, 1, 2}}, {0, 4, []int64{0, 1, 2, 3}},
		{2, 4, []int64{2, 3}}, {2, 5, []int64{2, 3, 4}}, {2, 6, []int64{2, 3, 4, 5}},
		{2, 6, []int64{2, 3, 4, 5}}, {5, 6, []int64{5}}, {5, 6, []int64{5}},
	}
	got := make([]snapshot, 0, 10)
	step := func(f func()) { f(); got = append(got, s.snapshot()) }
	for range 4 {
		step(func() { _, _ = s.Send() })
	}
	step(func() { _ = s.Ack(2) })
	step(func() { _, _ = s.Send() })
	step(func() { _, _ = s.Send() })
	step(func() { _ = s.Ack(1) }) // 不变量3：重复/乱序幂等
	step(func() { _ = s.Ack(5) })
	step(func() { _ = s.Timeout() })
	for i := range want { // 不变量1（朴素一致）、2（next-base<=W）
		if !eqSnap(got[i], want[i]) || got[i].next-got[i].base > 4 {
			return false
		}
	}
	if r := s.Timeout(); len(r) != 1 || r[0] != 5 { // 第10步只重传 {5}
		return false
	}
	if _, err := New(0); !errors.Is(err, ErrBadWindow) { // 不变量4：非法 W
		return false
	}
	r, _ := New(4) // 独立喂满实例：负 ACK、满窗 Send 被拒且不留痕
	for range 4 {
		if _, e := r.Send(); e != nil {
			return false
		}
	}
	before := r.snapshot()
	if e := r.Ack(-1); !errors.Is(e, ErrBadAck) || !eqSnap(r.snapshot(), before) {
		return false
	}
	if _, e := r.Send(); !errors.Is(e, ErrWindowFull) || !eqSnap(r.snapshot(), before) {
		return false
	}
	return gbn.AdvanceCheckConstant() && concurrentReadOnlyConsistent()
}

// concurrentReadOnlyConsistent 多 goroutine 并发只读喂满实例，采样逐字段相同；不用 sleep。
func concurrentReadOnlyConsistent() bool {
	s, _ := New(8)
	for range 8 {
		if _, e := s.Send(); e != nil {
			return false
		}
	}
	const n = 16
	var wg sync.WaitGroup
	q := make([]snapshot, n)
	wg.Add(n)
	for i := range n {
		go func(i int) { defer wg.Done(); q[i] = s.snapshot() }(i)
	}
	wg.Wait()
	for _, v := range q {
		if v.base != 0 || v.next != 8 || len(v.unacked) != 8 {
			return false
		}
		for k, x := range v.unacked {
			if x != int64(k) {
				return false
			}
		}
	}
	return true
}
