// Package api 是 Lamport 时钟与事件全序的对外接口，仅依赖 hist。
package api

import (
	"errors"
	"sort"

	"ontology/hist"
)

// System 是一个 n 节点分布式系统的进程内模型。
type System struct{ h *hist.History }

// New 创建 n 个节点、事件总数上限 maxEvents 的系统；n/maxEvents 非正按节点非法拒绝。
func New(n, maxEvents int) (*System, error) {
	h, err := hist.New(n, maxEvents)
	if err != nil {
		return nil, err
	}
	return &System{h: h}, nil
}
func (s *System) Local(node int) error                  { return s.h.Local(node) }
func (s *System) Send(from, to int) (int, int64, error) { return s.h.Send(from, to) }
func (s *System) Recv(msgID int) (int64, error)         { return s.h.Recv(msgID) }
func (s *System) Order() []hist.Event                   { return s.h.Order() }
func (s *System) HappensBefore(a, b int) bool           { return s.h.HappensBefore(a, b) }

// 自检步骤（即 NOTES 九事件）：kind 0=local(a) 1=send(a→b) 2=节点a 接收消息 b。
var selfSteps = [...]struct{ kind, a, b int }{
	{0, 3, 0}, {1, 3, 1}, {0, 2, 0}, {2, 1, 1}, {1, 1, 2},
	{1, 2, 3}, {2, 2, 2}, {2, 3, 3}, {0, 1, 0},
}

// SelfCheck 对内置操作序列核验第二节四条不变量；任一条不成立返回可判定错误。
func (s *System) SelfCheck() error {
	h, err := hist.New(3, 50)
	if err != nil {
		return err
	}
	for _, st := range selfSteps {
		switch st.kind {
		case 0:
			err = h.Local(st.a)
		case 1:
			_, _, err = h.Send(st.a, st.b)
		case 2:
			_, err = h.Recv(st.b)
		}
		if err != nil {
			return err
		}
	}
	got := h.Order()
	if len(got) != 9 {
		return errors.New("selfcheck: expected 9 events")
	}
	byID := make([]hist.Event, 10)
	for _, e := range got {
		byID[e.ID] = e
	}
	// 不变量 1：与把全部事件按 (TS,Node) 整体排序的批量结果逐项相同。
	batch := append([]hist.Event(nil), got...)
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].TS != batch[j].TS {
			return batch[i].TS < batch[j].TS
		}
		return batch[i].Node < batch[j].Node
	})
	for i := range got {
		if got[i] != batch[i] {
			return errors.New("selfcheck: invariant 1 (batch recompute) violated")
		}
	}
	// 不变量 2：a→b ⇒ ts(a)<ts(b)，用 HB 传递闭包逐对核验。
	for a := 1; a <= 9; a++ {
		for b := 1; b <= 9; b++ {
			if h.HappensBefore(a, b) && !(byID[a].TS < byID[b].TS) {
				return errors.New("selfcheck: invariant 2 (clock condition) violated")
			}
		}
	}
	// 不变量 3：同节点时间戳严格递增；任意两事件 (TS,Node) 互不相同。
	seen := map[[2]int]bool{}
	last := map[int]int{}
	for _, e := range got {
		k := [2]int{e.TS, e.Node}
		if seen[k] {
			return errors.New("selfcheck: invariant 3 (duplicate order key) violated")
		}
		seen[k] = true
		if t, ok := last[e.Node]; ok && !(t < e.TS) {
			return errors.New("selfcheck: invariant 3 (per-node strictly increasing) violated")
		}
		last[e.Node] = e.TS
	}
	return selfCheckRejection(h, byID[2].Msg) // e2 是 m1 的发送事件
}

// selfCheckRejection 核验不变量 4：四类拒绝互不相同、不留痕，之后仍可正常使用。
func selfCheckRejection(h *hist.History, m1 int) error {
	n := len(h.Order())
	cases := []struct {
		try  func() error
		want error
	}{
		{func() error { return h.Local(9) }, hist.ErrInvalidNode},
		{func() error { _, _, e := h.Send(0, 1); return e }, hist.ErrInvalidNode},
		{func() error { _, e := h.Recv(999); return e }, hist.ErrMsgNotFound},
		{func() error { _, e := h.Recv(m1); return e }, hist.ErrMsgRecvTwice},
	}
	for _, c := range cases {
		if e := c.try(); !errors.Is(e, c.want) {
			return errors.New("selfcheck: rejection returned wrong error")
		}
		if len(h.Order()) != n {
			return errors.New("selfcheck: invariant 4 (rejection left a trace) violated")
		}
	}
	if e := h.Local(1); e != nil { // 拒绝后实例仍可正常使用
		return errors.New("selfcheck: system unusable after rejections")
	}
	g, _ := hist.New(1, 1)
	if e := g.Local(1); e != nil {
		return e
	}
	if e := g.Local(1); !errors.Is(e, hist.ErrEventLimit) {
		return errors.New("selfcheck: event-limit error wrong")
	}
	if len(g.Order()) != 1 {
		return errors.New("selfcheck: invariant 4 (limit left a trace) violated")
	}
	return nil
}
