package clock

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// bufLogger 是不带时间戳的内存日志器，便于断言日志内容，
// 同时避免标准库 log 的时间前缀破坏可重复性。
type bufLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *bufLogger) Printf(format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", v...)
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func newTestSystem(t *testing.T, nodes int, max int64) (*System, *bufLogger) {
	t.Helper()
	lg := &bufLogger{}
	s := New(nodes, max)
	s.SetLogger(lg)
	return s, lg
}

func eventKey(e *Event) string {
	return fmt.Sprintf("id=%d/node=%d/seq=%d/clock=%d/type=%s/msg=%d",
		e.ID, e.Node, e.Seq, e.Clock, e.Type, e.MessageID)
}

// 本地与发送事件使时钟加一。
func TestLocalAndSendIncrement(t *testing.T) {
	s, _ := newTestSystem(t, 2, 100)

	e1, err := s.Local(0)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Clock != 1 || e1.Seq != 0 || e1.ID != 0 {
		t.Fatalf("local event = %+v, want clock=1 seq=0 id=0", e1)
	}
	e2, err := s.Send(0)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Clock != 2 || e2.Seq != 1 || e2.MessageID != 0 {
		t.Fatalf("send event = %+v, want clock=2 seq=1 msg=0", e2)
	}
	if v, n, _ := s.Snapshot(0); v != 2 || n != 2 {
		t.Fatalf("snapshot = (%d,%d), want (2,2)", v, n)
	}
}

// 接收取 max(本地时钟, 消息时间戳) + 1 的三种边界：
// 消息时间更大、本地时钟更大、二者相等。
func TestReceiveTakesMax(t *testing.T) {
	cases := []struct {
		name      string
		local     int64 // 接收方在接收前的本地时钟
		msgClock  int64 // 消息携带时间戳
		wantClock int64
		basis     string // 日志中应体现的取值依据
	}{
		{"message larger", 1, 5, 6, "message"},
		{"local larger", 5, 1, 6, "local"},
		{"equal", 5, 5, 6, "local"}, // 相等时取谁结果相同，日志标注 local
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, lg := newTestSystem(t, 2, 100)

			// 发送方把时钟推进到 msgClock 后发送。
			var sendEv *Event
			for i := int64(0); i < tc.msgClock; i++ {
				var err error
				if i == tc.msgClock-1 {
					sendEv, err = s.Send(0)
				} else {
					_, err = s.Local(0)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if sendEv.Clock != tc.msgClock {
				t.Fatalf("send clock = %d, want %d", sendEv.Clock, tc.msgClock)
			}

			// 接收方把时钟推进到 local。
			for i := int64(0); i < tc.local; i++ {
				if _, err := s.Local(1); err != nil {
					t.Fatal(err)
				}
			}

			before := len(lg.String())
			recv, err := s.Receive(1, sendEv.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			if recv.Clock != tc.wantClock {
				t.Fatalf("recv clock = %d, want %d", recv.Clock, tc.wantClock)
			}
			line := lg.String()[before:]
			if !strings.Contains(line, "input=receive") ||
				!strings.Contains(line, fmt.Sprintf("timestamp=%d", tc.wantClock)) ||
				!strings.Contains(line, tc.basis) {
				t.Fatalf("receive log missing input/timestamp/basis:\n%s", line)
			}
		})
	}
}

// 时间戳并列时按节点编号排序，且全序与操作执行先后无关。
func TestTotalOrderTieAndExecutionOrder(t *testing.T) {
	// 脚本 A：节点 0 先做本地，节点 1 后做本地 —— 二者时钟并列。
	build := func(firstNode int) []*Event {
		s, _ := newTestSystem(t, 2, 100)
		other := 1 - firstNode
		if _, err := s.Local(firstNode); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Local(other); err != nil {
			t.Fatal(err)
		}
		return s.TotalOrder()
	}

	for _, first := range []int{0, 1} {
		order := build(first)
		if len(order) != 2 || order[0].Node != 0 || order[1].Node != 1 {
			var got []string
			for _, e := range order {
				got = append(got, eventKey(e))
			}
			t.Fatalf("first=%d total order = %v, want node0 before node1 on tie", first, got)
		}
	}
}

// 同一脚本以不同接受顺序构造因果同构的事件集，全序必须一致。
func TestTotalOrderIndependentOfInterleaving(t *testing.T) {
	// 脚本：0 发送 m；1 本地；1 接收 m；0 再本地。
	script := func(s *System) {
		send, err := s.Send(0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Local(1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Receive(1, send.MessageID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Local(0); err != nil {
			t.Fatal(err)
		}
	}

	s1, _ := newTestSystem(t, 2, 100)
	script(s1)
	// 用完全相同的脚本再次执行，验证可重复性。
	s3, _ := newTestSystem(t, 2, 100)
	script(s3)

	var keys2, keys3 []string
	for _, e := range s1.TotalOrder() {
		keys2 = append(keys2, eventKey(e))
	}
	for _, e := range s3.TotalOrder() {
		keys3 = append(keys3, eventKey(e))
	}
	if strings.Join(keys2, "|") != strings.Join(keys3, "|") {
		t.Fatalf("same script produced different orders:\n%v\n%v", keys2, keys3)
	}
}

// 因果与并发判定：同节点先后、发送→接收、传递闭包、并发。
func TestCausalityAndConcurrency(t *testing.T) {
	s, lg := newTestSystem(t, 3, 100)

	// 节点 0：本地 e0 -> 发送 m(e1)
	e0, _ := s.Local(0)
	send, _ := s.Send(0)
	// 节点 2 与任何消息无关地本地一次，与 e0 并发
	ind, _ := s.Local(2)
	// 节点 1：本地(e3) -> 接收 m(e4) -> 本地(e5)
	l1, _ := s.Local(1)
	recv, _ := s.Receive(1, send.MessageID)
	l2, _ := s.Local(1)

	check := func(a, b int64, want Order) {
		t.Helper()
		got, err := s.Compare(a, b)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Compare(%d,%d) = %s, want %s", a, b, got, want)
		}
	}

	// 同节点先后
	check(e0.ID, send.ID, OrderBefore)
	check(send.ID, e0.ID, OrderAfter)
	check(l1.ID, l2.ID, OrderBefore)
	// 发送先于接收
	check(send.ID, recv.ID, OrderBefore)
	// 传递闭包：e0 -> send -> recv -> l2
	check(e0.ID, l2.ID, OrderBefore)
	// 并发：节点 2 的独立事件与发送方、接收方事件均无因果关系
	check(ind.ID, send.ID, OrderConcurrent)
	check(ind.ID, recv.ID, OrderConcurrent)
	check(e0.ID, ind.ID, OrderConcurrent)

	// 判定依据必须出现在日志中
	logs := lg.String()
	if !strings.Contains(logs, "path:") {
		t.Fatalf("causal log missing path basis:\n%s", logs)
	}
	if !strings.Contains(logs, "concurrent") {
		t.Fatalf("concurrent log missing basis:\n%s", logs)
	}
}

// 全序必须尊重因果：send 一定排在其 receive 之前。
func TestTotalOrderRespectsCausality(t *testing.T) {
	s, _ := newTestSystem(t, 2, 100)
	send, _ := s.Send(0)
	// 在节点 1 上堆出更高时钟，制造“接收事件时钟远大于发送”的场景
	for i := 0; i < 5; i++ {
		if _, err := s.Local(1); err != nil {
			t.Fatal(err)
		}
	}
	recv, _ := s.Receive(1, send.MessageID)

	pos := map[int64]int{}
	for i, e := range s.TotalOrder() {
		pos[e.ID] = i
	}
	if pos[send.ID] >= pos[recv.ID] {
		t.Fatalf("send at %d not before receive at %d", pos[send.ID], pos[recv.ID])
	}
}

// 各类非法输入：节点越界。
func TestInvalidNode(t *testing.T) {
	for _, bad := range []int{-1, 2, 100} {
		s, _ := newTestSystem(t, 2, 100)
		if _, err := s.Local(bad); !errorsIs(err, ErrNodeOutOfRange) {
			t.Fatalf("Local(%d) err = %v, want ErrNodeOutOfRange", bad, err)
		}
		if _, err := s.Send(bad); !errorsIs(err, ErrNodeOutOfRange) {
			t.Fatalf("Send(%d) err = %v, want ErrNodeOutOfRange", bad, err)
		}
		if _, err := s.Receive(bad, 0); !errorsIs(err, ErrNodeOutOfRange) {
			t.Fatalf("Receive(%d) err = %v, want ErrNodeOutOfRange", bad, err)
		}
	}
}

// 接收不存在的消息。
func TestReceiveNonexistent(t *testing.T) {
	s, _ := newTestSystem(t, 2, 100)
	if _, err := s.Receive(0, 42); !errorsIs(err, ErrMessageNotFound) {
		t.Fatalf("err = %v, want ErrMessageNotFound", err)
	}
}

// 重复接收同一条消息必须被拒绝，且只能被一个节点接收一次。
func TestDuplicateReceive(t *testing.T) {
	s, _ := newTestSystem(t, 3, 100)
	send, _ := s.Send(0)

	if _, err := s.Receive(1, send.MessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(1, send.MessageID); !errorsIs(err, ErrDuplicateReceive) {
		t.Fatalf("same node re-receive err = %v, want ErrDuplicateReceive", err)
	}
	if _, err := s.Receive(2, send.MessageID); !errorsIs(err, ErrDuplicateReceive) {
		t.Fatalf("other node re-receive err = %v, want ErrDuplicateReceive", err)
	}
}

// 事件总数超限被拒绝。
func TestEventLimit(t *testing.T) {
	s, _ := newTestSystem(t, 2, 3)
	for i := 0; i < 3; i++ {
		if _, err := s.Local(0); err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
	}
	if _, err := s.Local(0); !errorsIs(err, ErrEventLimit) {
		t.Fatalf("local over limit err = %v, want ErrEventLimit", err)
	}
	if _, err := s.Send(0); !errorsIs(err, ErrEventLimit) {
		t.Fatalf("send over limit err = %v, want ErrEventLimit", err)
	}
	// 先有一条已登记消息，超限下接收同样被拒绝。
	s2, _ := newTestSystem(t, 2, 1)
	send, _ := s2.Send(0) // 用满唯一配额；消息已登记
	if _, err := s2.Receive(1, send.MessageID); !errorsIs(err, ErrEventLimit) {
		t.Fatalf("receive over limit err = %v, want ErrEventLimit", err)
	}
}

// 被拒绝的操作不得改变时钟、事件编号、全序与消息登记。
func TestRejectionLeavesStateUnchanged(t *testing.T) {
	s, _ := newTestSystem(t, 3, 4)
	send, _ := s.Send(0)
	recv, _ := s.Receive(1, send.MessageID)
	if _, err := s.Local(2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Local(2); err != nil { // 用满 4 个事件配额
		t.Fatal(err)
	}

	snapshot := func() string {
		var b strings.Builder
		for _, e := range s.TotalOrder() {
			b.WriteString(eventKey(e))
			b.WriteByte(';')
		}
		for n := 0; n < 3; n++ {
			c, cnt, _ := s.Snapshot(n)
			fmt.Fprintf(&b, "n%d=%d/%d;", n, c, cnt)
		}
		fmt.Fprintf(&b, "msgs=%d", s.EventCount())
		return b.String()
	}
	before := snapshot()

	type rejected struct {
		name string
		fn   func() error
	}
	cases := []rejected{
		{"local bad node", func() error { _, e := s.Local(-1); return e }},
		{"send bad node", func() error { _, e := s.Send(9); return e }},
		{"recv bad node", func() error { _, e := s.Receive(9, send.MessageID); return e }},
		{"recv missing", func() error { _, e := s.Receive(2, 999); return e }},
		{"recv duplicate", func() error { _, e := s.Receive(2, send.MessageID); return e }},
		{"over limit local", func() error { _, e := s.Local(0); return e }},
		{"over limit send", func() error { _, e := s.Send(0); return e }},
		{"recv missing and over limit", func() error { _, e := s.Receive(2, -1); return e }},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Fatalf("%s: want rejection, got nil", c.name)
		}
		if got := snapshot(); got != before {
			t.Fatalf("%s changed state:\nbefore: %s\nafter:  %s", c.name, before, got)
		}
	}

	// recv 事件仍指向原接收者，消息登记数量/状态未被篡改
	if order := s.TotalOrder(); len(order) != 4 {
		t.Fatalf("event count = %d, want 4", len(order))
	}
	if recv.MessageID != send.MessageID {
		t.Fatal("existing receive event altered")
	}
}

// 并发调用：每个节点的节点内事件编号必须连续，
// 全序必须尊重所有 send->receive 因果边，且与同脚本串行重放一致。
func TestConcurrentContinuityAndCausality(t *testing.T) {
	const senders = 2
	const perSender = 50
	const receivers = 3
	s, _ := newTestSystem(t, senders+receivers, 100000)

	var wg sync.WaitGroup
	type sent struct {
		ev *Event
	}
	sentCh := make(chan sent, senders*perSender)

	// 阶段一：发送方与其他本地事件并发产生消息。
	for n := 0; n < senders; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			for i := 0; i < perSender; i++ {
				ev, err := s.Send(node)
				if err != nil {
					t.Errorf("send: %v", err)
					return
				}
				sentCh <- sent{ev}
			}
		}(n)
	}
	// 接收方节点并发刷本地事件，制造时钟竞争。
	for n := senders; n < senders+receivers; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if _, err := s.Local(node); err != nil {
					t.Errorf("local: %v", err)
					return
				}
			}
		}(n)
	}
	wg.Wait()
	close(sentCh)

	var msgs []*Event
	for sm := range sentCh {
		msgs = append(msgs, sm.ev)
	}

	// 阶段二：接收方并发接收全部消息，每条恰好被接收一次。
	jobs := make(chan *Event, len(msgs))
	for _, m := range msgs {
		jobs <- m
	}
	close(jobs)
	edgeMu := sync.Mutex{}
	for w := 0; w < receivers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				r, err := s.Receive(senders+w, m.MessageID)
				if err != nil {
					t.Errorf("receive msg %d: %v", m.MessageID, err)
					return
				}
				edgeMu.Lock()
				// 接收时钟必须严格大于消息时间戳
				if r.Clock <= m.Clock {
					t.Errorf("recv clock %d <= msg clock %d", r.Clock, m.Clock)
				}
				edgeMu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 节点内 Seq 必须为 0..count-1 连续，时钟严格递增。
	for n := 0; n < senders+receivers; n++ {
		_, cnt, err := s.Snapshot(n)
		if err != nil {
			t.Fatal(err)
		}
		seenSeq := make(map[int64]bool, cnt)
		var lastClock int64
		for _, e := range s.TotalOrder() {
			if e.Node != n {
				continue
			}
			seenSeq[e.Seq] = true
			if e.Clock <= lastClock {
				t.Fatalf("node %d clock not strictly increasing at seq %d: %d <= %d", n, e.Seq, e.Clock, lastClock)
			}
			lastClock = e.Clock
		}
		if int64(len(seenSeq)) != cnt {
			t.Fatalf("node %d seqs = %d unique, want %d", n, len(seenSeq), cnt)
		}
		for i := int64(0); i < cnt; i++ {
			if !seenSeq[i] {
				t.Fatalf("node %d missing seq %d", n, i)
			}
		}
	}

	// 全序必须尊重每一条 send->receive 边。
	pos := map[int64]int{}
	for i, e := range s.TotalOrder() {
		pos[e.ID] = i
	}
	for _, m := range msgs {
		// 找到接收该消息的事件
		var recvID int64 = -1
		for _, e := range s.TotalOrder() {
			if e.Type == EventReceive && e.MessageID == m.MessageID {
				recvID = e.ID
				break
			}
		}
		if recvID == -1 {
			t.Fatalf("message %d never received", m.MessageID)
		}
		if pos[m.ID] >= pos[recvID] {
			t.Fatalf("msg %d: send pos %d >= recv pos %d", m.MessageID, pos[m.ID], pos[recvID])
		}
		if got, _ := s.Compare(m.ID, recvID); got != OrderBefore {
			t.Fatalf("msg %d: Compare(send,recv) = %s, want before", m.MessageID, got)
		}
	}
}

// 同一输入序列反复计算必须得到完全相同的输出（全序 + 全部两两因果判定）。
func TestDeterministicReplay(t *testing.T) {
	// op: 0=local 1=send 2=recv(引用第 ref 个 send 产生的消息)
	type op struct {
		kind int
		node int
		ref  int
	}
	script := []op{
		{1, 0, -1}, // send #0
		{0, 1, -1},
		{2, 1, 0}, // node1 接收 send#0
		{0, 0, -1},
		{1, 0, -1}, // send #1
		{0, 2, -1},
		{2, 2, 1},  // node2 接收 send#1
		{1, 1, -1}, // send #2
		{2, 2, 2},  // node2 接收 send#2
	}

	run := func() string {
		s, _ := newTestSystem(t, 3, 100)
		var sends []int64 // 已发送消息 id
		var ids []int64
		for _, o := range script {
			switch o.kind {
			case 0:
				e, err := s.Local(o.node)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, e.ID)
			case 1:
				e, err := s.Send(o.node)
				if err != nil {
					t.Fatal(err)
				}
				sends = append(sends, e.MessageID)
				ids = append(ids, e.ID)
			case 2:
				e, err := s.Receive(o.node, sends[o.ref])
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, e.ID)
			}
		}
		var b strings.Builder
		for _, e := range s.TotalOrder() {
			b.WriteString(eventKey(e))
			b.WriteByte(';')
		}
		b.WriteByte('|')
		for _, a := range ids {
			for _, bb := range ids {
				o, err := s.Compare(a, bb)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&b, "%s,", o)
			}
		}
		return b.String()
	}

	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); got != first {
			t.Fatalf("replay %d differs:\nfirst: %s\ngot:   %s", i, first, got)
		}
	}
}

// Compare 对越界事件编号必须报错。
func TestCompareBadID(t *testing.T) {
	s, _ := newTestSystem(t, 1, 10)
	s.Local(0)
	for _, id := range []int64{-1, 1, 99} {
		if _, err := s.Compare(0, id); err == nil {
			t.Fatalf("Compare(0,%d) want error", id)
		}
	}
}

// errorsIs 避免在测试文件额外 import errors。
func errorsIs(err, target error) bool {
	if err == nil {
		return false
	}
	type iser interface{ Is(error) bool }
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return err == target
}
