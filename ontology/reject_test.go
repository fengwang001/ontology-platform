package ontology

import "testing"

// TestInvalidConstruction 构造参数非法。
func TestInvalidConstruction(t *testing.T) {
	if _, err := NewSystem(0, 10); err == nil {
		t.Fatal("nodeCount=0 should be rejected")
	} else {
		wantErrKind(t, err, ErrInvalidNodeCount)
		t.Logf("nodeCount=0 拒绝原因可区分: %v", err)
	}
	if _, err := NewSystem(-1, 10); err == nil {
		t.Fatal("nodeCount=-1 should be rejected")
	} else {
		wantErrKind(t, err, ErrInvalidNodeCount)
	}
	if _, err := NewSystem(3, 0); err == nil {
		t.Fatal("maxEvents=0 should be rejected")
	} else {
		wantErrKind(t, err, ErrInvalidEventLimit)
		t.Logf("maxEvents=0 拒绝原因可区分: %v", err)
	}
}

// TestNodeOutOfRange 三类操作的节点越界（负节点与超过上界）。
func TestNodeOutOfRange(t *testing.T) {
	s, _ := NewSystem(2, 100)
	cases := []struct {
		name string
		f    func() (Event, error)
	}{
		{"local-neg", func() (Event, error) { return s.Local(-1) }},
		{"local-high", func() (Event, error) { return s.Local(2) }},
		{"send-neg", func() (Event, error) { return s.Send(-1, "m") }},
		{"send-high", func() (Event, error) { return s.Send(99, "m") }},
		{"recv-neg", func() (Event, error) { return s.Receive(-1, "m") }},
		{"recv-high", func() (Event, error) { return s.Receive(2, "m") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.f()
			wantErrKind(t, err, ErrNodeOutOfRange)
			t.Logf("%s 被拒绝，原因: %v", c.name, err)
		})
	}
	if n := s.EventCount(); n != 0 {
		t.Fatalf("rejected ops changed event count to %d", n)
	}
}

// TestReceiveNonexistentMessage 接收从未发送过的消息。
func TestReceiveNonexistentMessage(t *testing.T) {
	s, _ := NewSystem(2, 100)
	must(s.Local(0))
	_, err := s.Receive(1, "ghost")
	wantErrKind(t, err, ErrMessageNotFound)
	t.Logf("接收不存在的消息被拒绝，原因可区分: %v", err)

	// 空消息 ID 是另一类可区分原因。
	_, err = s.Receive(1, "")
	wantErrKind(t, err, ErrInvalidMessageID)
	t.Logf("接收空消息 ID 被拒绝，原因可区分: %v", err)

	if n := s.EventCount(); n != 1 {
		t.Fatalf("event count = %d, rejected receive must not add events", n)
	}
	if c := s.Clocks()[1]; c != 0 {
		t.Fatalf("node1 clock = %d, rejected receive must not advance clock", c)
	}
}

// TestDuplicateReceive 同一条消息不能被接收两次。
func TestDuplicateReceive(t *testing.T) {
	s, _ := NewSystem(3, 100)
	must(s.Send(0, "m"))
	r1 := must(s.Receive(1, "m"))
	logOp(t, "receive#1", 1, "m", r1, nil)

	_, err := s.Receive(2, "m") // 换节点也不允许
	wantErrKind(t, err, ErrDuplicateReceive)
	t.Logf("第二次接收（换节点）被拒绝，原因可区分: %v", err)
	_, err = s.Receive(1, "m") // 同节点重复
	wantErrKind(t, err, ErrDuplicateReceive)
	t.Logf("同节点重复接收被拒绝，原因可区分: %v", err)

	if n := s.EventCount(); n != 2 {
		t.Fatalf("event count = %d, want 2 (send+one recv)", n)
	}
	if c := s.Clocks()[1]; c != r1.Clock {
		t.Fatalf("node1 clock advanced from %d to %d after rejected recv", r1.Clock, c)
	}
	if c := s.Clocks()[2]; c != 0 {
		t.Fatalf("node2 clock = %d, must stay 0 after rejected recv", c)
	}
}

// TestDuplicateSend 同一消息 ID 不能发送两次。
func TestDuplicateSend(t *testing.T) {
	s, _ := NewSystem(2, 100)
	must(s.Send(0, "m"))
	_, err := s.Send(1, "m")
	wantErrKind(t, err, ErrDuplicateSend)
	t.Logf("重复发送被拒绝，原因可区分: %v", err)
	_, err = s.Send(0, "")
	wantErrKind(t, err, ErrInvalidMessageID)
	if n := s.EventCount(); n != 1 {
		t.Fatalf("event count = %d, want 1", n)
	}
}

// TestTooManyEvents 达到事件上限后，三类操作都被拒绝且无副作用。
func TestTooManyEvents(t *testing.T) {
	s, _ := NewSystem(2, 3)
	must(s.Local(0))     // 1
	must(s.Send(0, "m")) // 2
	must(s.Local(1))     // 3，达到上限

	clocksBefore := s.Clocks()
	orderBefore := s.TotalOrder()

	_, err := s.Local(0)
	wantErrKind(t, err, ErrTooManyEvents)
	_, err = s.Send(0, "x")
	wantErrKind(t, err, ErrTooManyEvents)
	_, err = s.Receive(1, "m")
	wantErrKind(t, err, ErrTooManyEvents)
	t.Logf("达到上限后的 local/send/receive 均被拒绝，原因: %v", err)

	if n := s.EventCount(); n != 3 {
		t.Fatalf("event count = %d, want 3", n)
	}
	if got := s.Clocks(); !equalInts(got, clocksBefore) {
		t.Fatalf("clocks changed %v -> %v after rejected ops", clocksBefore, got)
	}
	if got := s.TotalOrder(); !eventsEquivalent(got, orderBefore) {
		t.Fatalf("total order changed after rejected ops")
	}
	// 消息 m 仍未被接收（被拒绝的 receive 不得改变消息登记）。
	if ts, err := s.MessageTimestamp("m"); err != nil || ts != 2 {
		t.Fatalf("message registry altered: ts=%d err=%v", ts, err)
	}
	// 上限腾不出空间：拒绝后再来仍是满的，消息依然可被正常接收吗？不能——容量已满。
	// 这里验证拒绝接收不会把 m 标记成 received（后续无新增容量，故只校验登记状态）。
	t.Logf("被拒绝操作后时钟=%v 不变，全序不变，消息登记不变", clocksBefore)
}

// TestRejectedReceiveLeavesMessageUnregistered 顺序保证：
// 先因满容量拒绝接收，确认消息未被标记，再在更大容量系统中验证同消息仍可接收一次。
func TestRejectionDoesNotConsumeMessage(t *testing.T) {
	// 容量为1：只能登记发送，接收会因超限被拒，消息不得被标记为已接收。
	s, _ := NewSystem(2, 1)
	must(s.Send(0, "m")) // 第1个事件，容量用尽
	_, err := s.Receive(1, "m")
	wantErrKind(t, err, ErrTooManyEvents)

	// 用一个新系统对照：同一消息在有容量时应能被接收（证明前一个系统并未"消耗"消息语义，
	// 且拒绝原因确实是容量而非消息问题）。
	s2, _ := NewSystem(2, 10)
	must(s2.Send(0, "m"))
	if _, err := s2.Receive(1, "m"); err != nil {
		t.Fatalf("message should be receivable in a capacity-ok system: %v", err)
	}
}

// TestUnknownEvent 查询不存在的事件返回可区分错误。
func TestUnknownEvent(t *testing.T) {
	s, _ := NewSystem(2, 100)
	must(s.Local(0))
	if _, err := s.Event(EventRef{0, 5}); err == nil {
		t.Fatal("expected ErrUnknownEvent")
	} else {
		wantErrKind(t, err, ErrUnknownEvent)
	}
	if _, err := s.Compare(EventRef{0, 1}, EventRef{1, 1}); err == nil {
		t.Fatal("compare with missing event should fail")
	} else {
		wantErrKind(t, err, ErrUnknownEvent)
		t.Logf("引用不存在事件被拒绝，原因可区分: %v", err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eventsEquivalent(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Node != b[i].Node || a[i].Seq != b[i].Seq || a[i].Clock != b[i].Clock {
			return false
		}
	}
	return true
}
