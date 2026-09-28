package ontology

import "testing"

// TestLocalAndSendClock 本地事件与发送事件都使时钟加一。
func TestLocalAndSendClock(t *testing.T) {
	s, err := NewSystem(2, 100)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}

	e1 := must(s.Local(0))
	logOp(t, "local", 0, "", e1, nil)
	if e1.Clock != 1 {
		t.Fatalf("first local event clock = %d, want 1", e1.Clock)
	}

	e2 := must(s.Send(0, "m1"))
	logOp(t, "send", 0, "m1", e2, nil)
	if e2.Clock != 2 {
		t.Fatalf("send event clock = %d, want 2", e2.Clock)
	}

	if got := s.Clocks()[0]; got != 2 {
		t.Fatalf("node0 clock = %d, want 2", got)
	}
	if got := s.EventCount(); got != 2 {
		t.Fatalf("event count = %d, want 2", got)
	}
}

// TestReceiveTakesMax 覆盖接收时 max(本地时钟, 消息时间戳)+1 的两个边界方向。
func TestReceiveTakesMax(t *testing.T) {
	t.Run("消息时间戳领先_采用消息时间戳", func(t *testing.T) {
		// 节点0推进到时钟3后发送 m（携带时间戳3）；节点1尚无事件（时钟0）。
		s, _ := NewSystem(2, 100)
		must(s.Local(0))
		must(s.Local(0))
		send := must(s.Send(0, "m"))
		if send.Clock != 3 {
			t.Fatalf("send clock = %d, want 3", send.Clock)
		}
		recv := must(s.Receive(1, "m"))
		logOp(t, "receive", 1, "m", recv, nil)
		// max(0, 3)+1 = 4
		if recv.Clock != 4 {
			t.Fatalf("recv clock = %d, want max(0,3)+1=4", recv.Clock)
		}
	})

	t.Run("本地时钟领先_采用本地时钟", func(t *testing.T) {
		// 节点1已推进到时钟5，再接收到时间戳为1的消息。
		s, _ := NewSystem(2, 100)
		must(s.Send(0, "m")) // 节点0时钟1，m 携带1
		must(s.Local(1))     // 1
		must(s.Local(1))     // 2
		must(s.Local(1))     // 3
		must(s.Local(1))     // 4
		must(s.Local(1))     // 5
		recv := must(s.Receive(1, "m"))
		logOp(t, "receive", 1, "m", recv, nil)
		// max(5, 1)+1 = 6
		if recv.Clock != 6 {
			t.Fatalf("recv clock = %d, want max(5,1)+1=6", recv.Clock)
		}
	})

	t.Run("恰好相等_仍严格加一", func(t *testing.T) {
		// 两边时钟都为2时接收：max(2,2)+1=3。
		s, _ := NewSystem(2, 100)
		must(s.Local(0))             // n0:1
		send := must(s.Send(0, "m")) // n0:2, m 携带2
		_ = send
		must(s.Local(1)) // n1:1
		must(s.Local(1)) // n1:2
		recv := must(s.Receive(1, "m"))
		logOp(t, "receive", 1, "m", recv, nil)
		if recv.Clock != 3 {
			t.Fatalf("recv clock = %d, want max(2,2)+1=3", recv.Clock)
		}
	})

	t.Run("链式消息_时间戳逐跳传播", func(t *testing.T) {
		// 0 -> 1 -> 2，验证时间戳沿消息链传播。
		s, _ := NewSystem(3, 100)
		must(s.Local(0))              // n0:1
		must(s.Send(0, "a"))          // n0:2
		r1 := must(s.Receive(1, "a")) // n1:3
		must(s.Send(1, "b"))          // n1:4
		r2 := must(s.Receive(2, "b")) // n2:5
		logOp(t, "receive", 1, "a", r1, nil)
		logOp(t, "receive", 2, "b", r2, nil)
		if r1.Clock != 3 || r2.Clock != 5 {
			t.Fatalf("chain clocks = %d,%d, want 3,5", r1.Clock, r2.Clock)
		}
	})
}

// TestTotalOrderTieBreak 时间戳并列时按节点编号升序，且全序与接受先后无关。
func TestTotalOrderTieBreak(t *testing.T) {
	s, _ := NewSystem(3, 100)
	// 故意先在高编号节点制造事件，再在低编号节点制造并列时间戳。
	e2 := must(s.Local(2)) // (clock=1,node=2)，先接受
	e1 := must(s.Local(1)) // (clock=1,node=1)，后接受
	e0 := must(s.Local(0)) // (clock=1,node=0)，最后接受
	t.Logf("输入接受顺序: n2(%d), n1(%d), n0(%d)", e2.Clock, e1.Clock, e0.Clock)

	ordered := s.TotalOrder()
	t.Logf("全序结果: %s", formatOrder(ordered))
	if len(ordered) != 3 {
		t.Fatalf("order len = %d, want 3", len(ordered))
	}
	wantNodes := []int{0, 1, 2}
	for i, e := range ordered {
		if e.Node != wantNodes[i] || e.Clock != 1 {
			t.Fatalf("position %d = (clock=%d,node=%d), want (1,%d)",
				i, e.Clock, e.Node, wantNodes[i])
		}
	}
}

// TestTotalOrderIndependentOfArrival 同一组事件以不同接受顺序构造，
// 重排后的全序必须逐位相同。
func TestTotalOrderIndependentOfArrival(t *testing.T) {
	build := func(recvFirst bool) []Event {
		s, _ := NewSystem(2, 100)
		if recvFirst {
			must(s.Local(1))
		}
		must(s.Local(0))
		if !recvFirst {
			must(s.Local(1))
		}
		must(s.Local(0))
		return s.TotalOrder()
	}
	a := build(false)
	b := build(true)
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Node != b[i].Node || a[i].Seq != b[i].Seq ||
			a[i].Clock != b[i].Clock || a[i].Kind != b[i].Kind ||
			a[i].MessageID != b[i].MessageID {
			t.Fatalf("position %d differs: %+v vs %+v\nA: %s\nB: %s",
				i, a[i], b[i], formatOrder(a), formatOrder(b))
		}
	}
	t.Logf("两种接受顺序的全序一致: %s", formatOrder(a))
}

// TestSeqContinuity 每节点事件编号从1开始连续不间断。
func TestSeqContinuity(t *testing.T) {
	s, _ := NewSystem(2, 100)
	for i := 1; i <= 3; i++ {
		e := must(s.Local(0))
		if e.Seq != i {
			t.Fatalf("node0 seq = %d, want %d", e.Seq, i)
		}
	}
	for i := 1; i <= 2; i++ {
		e := must(s.Local(1))
		if e.Seq != i {
			t.Fatalf("node1 seq = %d, want %d", e.Seq, i)
		}
	}
}
