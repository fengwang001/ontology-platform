package ontology

import "testing"

// TestCausalSameNode 同一节点上先发生的事件因果先于后发生的事件。
func TestCausalSameNode(t *testing.T) {
	s, _ := NewSystem(2, 100)
	a := must(s.Local(0))
	b := must(s.Local(0))
	c := must(s.Local(0))
	t.Logf("同节点事件: (%d,%d)t=%d -> (%d,%d)t=%d -> (%d,%d)t=%d",
		a.Node, a.Seq, a.Clock, b.Node, b.Seq, b.Clock, c.Node, c.Seq, c.Clock)

	ra := EventRef{0, a.Seq}
	rb := EventRef{0, b.Seq}
	rc := EventRef{0, c.Seq}

	r, err := s.Compare(ra, rb)
	if err != nil || r != Before {
		t.Fatalf("compare(a,b) = %s,%v want before", r, err)
	}
	logRelation(t, ra, rb, r, a.Vector, b.Vector)

	r, _ = s.Compare(rb, ra)
	if r != After {
		t.Fatalf("compare(b,a) = %s, want after", r)
	}

	// 传递：a -> c（即使不相邻）。
	ok, err := s.HappensBefore(ra, rc)
	if err != nil || !ok {
		t.Fatalf("a happens-before c = %v,%v want true", ok, err)
	}
	t.Logf("传递闭包: a->b 且 b->c，故 a->c = true（依据向量分量逐项 <=）")

	r, _ = s.Compare(ra, ra)
	if r != Equal {
		t.Fatalf("compare(a,a) = %s, want equal", r)
	}
}

// TestCausalMessageEdge 发送事件因果先于其接收事件，且接收之后
// 接收节点上的事件也因果晚于发送（消息边 + 线程边的传递闭包）。
func TestCausalMessageEdge(t *testing.T) {
	s, _ := NewSystem(3, 100)
	before := must(s.Local(2))      // 与本次消息无关的并发事件
	send := must(s.Send(0, "m"))    // n0 发送
	between := must(s.Local(0))     // 发送之后同节点的事件
	recv := must(s.Receive(1, "m")) // n1 接收
	after := must(s.Local(1))       // 接收之后同节点的事件

	rsend := EventRef{0, send.Seq}
	rrecv := EventRef{1, recv.Seq}
	rbetween := EventRef{0, between.Seq}
	rafter := EventRef{1, after.Seq}
	rbefore := EventRef{2, before.Seq}

	r, err := s.Compare(rsend, rrecv)
	if err != nil || r != Before {
		t.Fatalf("send->recv = %s,%v want before", r, err)
	}
	logRelation(t, rsend, rrecv, r, send.Vector, recv.Vector)
	t.Logf("判定依据: 接收事件向量逐分量合并了发送向量 %v -> %v", send.Vector, recv.Vector)

	// 传递闭包：send -> recv -> after
	ok, _ := s.HappensBefore(rsend, rafter)
	if !ok {
		t.Fatalf("send -> after(receiver later event) want true")
	}
	// between（发送之后在发送节点）也先于接收，因为同节点 send->between 与... 实际是 between 晚于 send。
	// send -> between（同节点），send -> recv（消息），但 between 与 recv 无直接关系；
	// between 在 send 之后，故 recv 不必然晚于 between。验证它们并发：
	r, _ = s.Compare(rbetween, rrecv)
	if r != Concurrent {
		t.Fatalf("between(same node after send) vs recv = %s, want concurrent", r)
	}
	t.Logf("发送节点 send 之后的本地事件 between 与 接收事件 recv 并发（无因果路径）")

	// 无关节点事件与消息双方并发。
	if r, _ := s.Compare(rbefore, rsend); r != Concurrent {
		t.Fatalf("unrelated node2 event vs send = %s, want concurrent", r)
	}
	if ok, _ := s.HappensBefore(rbefore, rrecv); ok {
		t.Fatalf("unrelated event must not happen-before recv")
	}
	concurrent, err := s.AreConcurrent(rbefore, rrecv)
	if err != nil || !concurrent {
		t.Fatalf("AreConcurrent(before,recv) = %v,%v want true", concurrent, err)
	}
	logRelation(t, rbefore, rrecv, Concurrent, before.Vector, recv.Vector)
}

// TestCausalTransitiveChain 多跳消息构成的因果链，起点因果先于终点。
func TestCausalTransitiveChain(t *testing.T) {
	s, _ := NewSystem(3, 100)
	first := must(s.Local(0))
	must(s.Send(0, "m1"))
	must(s.Receive(1, "m1"))
	must(s.Send(1, "m2"))
	must(s.Receive(2, "m2"))
	last := must(s.Local(2))

	ok, err := s.HappensBefore(EventRef{0, first.Seq}, EventRef{2, last.Seq})
	if err != nil || !ok {
		t.Fatalf("first on n0 -> last on n2 = %v,%v want true", ok, err)
	}
	t.Logf("多跳链 n0:local -> m1 -> n1 -> m2 -> n2:local，起点先于终点=true")

	// 反向不成立，且不是并发（明确的 after）。
	r, _ := s.Compare(EventRef{2, last.Seq}, EventRef{0, first.Seq})
	if r != After {
		t.Fatalf("last vs first = %s, want after", r)
	}
}

// TestCausalConsistentFull 构造含多消息、多节点的场景，断言系统自检通过，
// 且全序中每条因果边方向正确。
func TestCausalConsistentFull(t *testing.T) {
	s, _ := NewSystem(3, 100)
	must(s.Local(0))
	s1 := must(s.Send(0, "a"))
	must(s.Local(2)) // 并发事件
	must(s.Receive(1, "a"))
	must(s.Send(1, "b"))
	must(s.Local(0)) // 与 b 并发
	must(s.Receive(2, "b"))
	must(s.Local(1))

	if detail, ok := s.IsCausalConsistent(); !ok {
		t.Fatalf("system not causally consistent: %s", detail)
	}
	ordered := s.TotalOrder()
	t.Logf("全序（因果一致扩展）: %s", formatOrder(ordered))

	// 穷举所有事件对：凡 before，其在全序中的位置必须更靠前。
	pos := map[EventRef]int{}
	for i, e := range ordered {
		pos[EventRef{e.Node, e.Seq}] = i
	}
	es := s.Events()
	for _, x := range es {
		for _, y := range es {
			rx := EventRef{x.Node, x.Seq}
			ry := EventRef{y.Node, y.Seq}
			r, _ := s.Compare(rx, ry)
			if r == Before && pos[rx] >= pos[ry] {
				t.Fatalf("causal edge violated in total order: (%d,%d) before (%d,%d)",
					x.Node, x.Seq, y.Node, y.Seq)
			}
		}
	}
	t.Logf("全部因果边在全序中方向正确（依据 Lamport 时间戳保持 happens-before）")
	_ = s1
}
