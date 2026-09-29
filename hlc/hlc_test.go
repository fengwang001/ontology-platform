package hlc

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func newTestNetwork(t *testing.T, cfg Config, ids ...string) *Network {
	t.Helper()
	n := NewNetwork(cfg)
	for _, id := range ids {
		if err := n.AddNode(id); err != nil {
			t.Fatalf("AddNode(%q): %v", id, err)
		}
	}
	return n
}

var testConfig = Config{MaxClockOffset: 1000, MaxCounter: 1 << 20}

// TestLocalEvents 覆盖本地事件：物理前进、停滞与回拨。
func TestLocalEvents(t *testing.T) {
	n := newTestNetwork(t, testConfig, "a")

	ts1, err := n.Local("a", 100)
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	t.Logf("step1 local(a, pt=100) -> ts=%s 依据: pt>logical, logical=100 counter=0", ts1)
	if want := (Timestamp{100, 0}); ts1 != want {
		t.Fatalf("got %s want %s", ts1, want)
	}

	// 物理读数停滞：逻辑时间不推进，计数加一。
	ts2, err := n.Local("a", 100)
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	t.Logf("step2 local(a, pt=100) -> ts=%s 依据: pt==logical, counter+1", ts2)
	if want := (Timestamp{100, 1}); ts2 != want {
		t.Fatalf("got %s want %s", ts2, want)
	}

	// 物理时钟回拨：逻辑时间不回退，计数继续加一。
	ts3, err := n.Local("a", 10)
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	t.Logf("step3 local(a, pt=10) -> ts=%s 依据: pt<logical(回拨), counter+1", ts3)
	if want := (Timestamp{100, 2}); ts3 != want {
		t.Fatalf("got %s want %s", ts3, want)
	}

	// 物理读数重新超前：逻辑时间推进，计数归零。
	ts4, err := n.Local("a", 250)
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	t.Logf("step4 local(a, pt=250) -> ts=%s 依据: pt>logical, logical=250 counter=0", ts4)
	if want := (Timestamp{250, 0}); ts4 != want {
		t.Fatalf("got %s want %s", ts4, want)
	}

	if !(ts1.Compare(ts2) < 0 && ts2.Compare(ts3) < 0 && ts3.Compare(ts4) < 0) {
		t.Fatalf("timestamps not strictly increasing: %s %s %s %s", ts1, ts2, ts3, ts4)
	}
}

// TestSendReceive 覆盖发送/接收事件与因果顺序。
func TestSendReceive(t *testing.T) {
	n := newTestNetwork(t, testConfig, "a", "b")

	if _, err := n.Local("a", 50); err != nil {
		t.Fatalf("local: %v", err)
	}
	msgID, sendTS, err := n.Send("a", "b", 50)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	t.Logf("send(a->b, pt=50) -> msg=%s ts=%s 依据: pt==logical, counter+1", msgID, sendTS)

	// 接收方逻辑时间落后，消息时间占主导：计数取消息计数加一。
	recvTS, err := n.Receive("b", msgID, 10)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	t.Logf("receive(b, %s, pt=10) -> ts=%s 依据: msg.logical 占主导, counter=msg.counter+1", msgID, recvTS)
	if recvTS.Compare(sendTS) <= 0 {
		t.Fatalf("receive ts %s not greater than send ts %s", recvTS, sendTS)
	}

	// 反向消息：接收方逻辑时间与消息相等，计数取 max+1。
	msgID2, sendTS2, err := n.Send("b", "a", 60)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	recvTS2, err := n.Receive("a", msgID2, 60)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	t.Logf("send(b->a, pt=60) -> ts=%s; receive(a, pt=60) -> ts=%s 依据: 逻辑时间与消息相等取 max(counter)+1", sendTS2, recvTS2)
	if recvTS2.Compare(sendTS2) <= 0 {
		t.Fatalf("receive ts %s not greater than send ts %s", recvTS2, sendTS2)
	}
}

// TestPhysicalClockRollback 物理时钟持续回拨时时间戳仍严格单调。
func TestPhysicalClockRollback(t *testing.T) {
	n := newTestNetwork(t, testConfig, "a", "b")

	if _, err := n.Local("a", 500); err != nil {
		t.Fatalf("local: %v", err)
	}
	msgID, sendTS, err := n.Send("a", "b", 400) // 发送时物理已回拨
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	recvTS, err := n.Receive("b", msgID, 1) // 接收方物理读数极低
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	t.Logf("rollback: send ts=%s (pt=400), receive ts=%s (pt=1) 依据: 逻辑时间不回退", sendTS, recvTS)
	if recvTS.Compare(sendTS) <= 0 {
		t.Fatalf("causality violated under rollback: send=%s recv=%s", sendTS, recvTS)
	}

	prev := Timestamp{}
	hist, err := n.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, ev := range hist {
		if prev.Compare(ev.Timestamp) >= 0 {
			t.Fatalf("history not strictly increasing at %+v", ev)
		}
		prev = ev.Timestamp
	}
}

// TestClockOffsetLimit 偏差超限拒绝，边界值放行。
func TestClockOffsetLimit(t *testing.T) {
	cfg := Config{MaxClockOffset: 10, MaxCounter: 1 << 20}
	n := newTestNetwork(t, cfg, "a")

	if _, err := n.Local("a", 100); err != nil {
		t.Fatalf("local: %v", err)
	}
	// 边界：恰好超前 MaxClockOffset，允许。
	if _, err := n.Local("a", 110); err != nil {
		t.Fatalf("boundary offset should be accepted: %v", err)
	}
	t.Logf("offset boundary pt=110 (logical=110, max=10) accepted")
	// 超限：拒绝且状态不变。
	before, _ := n.Now("a")
	if _, err := n.Local("a", 121); !errors.Is(err, ErrClockOffsetExceeded) {
		t.Fatalf("got %v want ErrClockOffsetExceeded", err)
	}
	after, _ := n.Now("a")
	t.Logf("offset exceeded pt=121 rejected: %v; clock unchanged %s -> %s", ErrClockOffsetExceeded, before, after)
	if before != after {
		t.Fatalf("clock changed after rejection: %s -> %s", before, after)
	}
}

// TestCounterOverflow 计数超限拒绝且状态不变。
func TestCounterOverflow(t *testing.T) {
	cfg := Config{MaxClockOffset: 1000, MaxCounter: 2}
	n := newTestNetwork(t, cfg, "a")

	// 首个事件推进逻辑时间（counter=0），之后两次自增到上限 2。
	for i := 0; i < 3; i++ {
		if _, err := n.Local("a", 5); err != nil {
			t.Fatalf("local %d: %v", i, err)
		}
	}
	before, _ := n.Now("a")
	if _, err := n.Local("a", 5); !errors.Is(err, ErrCounterOverflow) {
		t.Fatalf("got %v want ErrCounterOverflow", err)
	}
	after, _ := n.Now("a")
	t.Logf("counter overflow at counter=%d rejected; clock unchanged %s -> %s", cfg.MaxCounter, before, after)
	if before != after {
		t.Fatalf("clock changed after rejection: %s -> %s", before, after)
	}
	// 物理读数推进后计数归零，可继续。
	if _, err := n.Local("a", 6); err != nil {
		t.Fatalf("local after physical advance: %v", err)
	}
}

// TestInvalidInputs 各类非法输入给出可区分错误，且拒绝后状态不变。
func TestInvalidInputs(t *testing.T) {
	n := newTestNetwork(t, testConfig, "a", "b")
	msgID, _, err := n.Send("a", "b", 10)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	snapshot := func() string {
		ta, _ := n.Now("a")
		tb, _ := n.Now("b")
		ha, _ := n.History("a")
		hb, _ := n.History("b")
		return fmt.Sprintf("%s|%s|%d|%d", ta, tb, len(ha), len(hb))
	}

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"empty node id", func() error { return n.AddNode("") }, ErrEmptyNodeID},
		{"duplicate node", func() error { return n.AddNode("a") }, ErrNodeExists},
		{"local unknown node", func() error { _, e := n.Local("x", 1); return e }, ErrNodeNotFound},
		{"send unknown source", func() error { _, _, e := n.Send("x", "b", 1); return e }, ErrNodeNotFound},
		{"send unknown target", func() error { _, _, e := n.Send("a", "x", 1); return e }, ErrNodeNotFound},
		{"negative physical", func() error { _, e := n.Local("a", -1); return e }, ErrNegativePhysical},
		{"receive unknown node", func() error { _, e := n.Receive("x", msgID, 1); return e }, ErrNodeNotFound},
		{"empty message id", func() error { _, e := n.Receive("b", "", 1); return e }, ErrEmptyMessageID},
		{"unknown message", func() error { _, e := n.Receive("b", "m999", 1); return e }, ErrMessageNotFound},
		{"wrong recipient", func() error { _, e := n.Receive("a", msgID, 1); return e }, ErrNotRecipient},
	}
	for _, tc := range cases {
		before := snapshot()
		err := tc.run()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		if after := snapshot(); before != after {
			t.Fatalf("%s: state changed after rejection: %s -> %s", tc.name, before, after)
		}
		t.Logf("reject %-20s -> %v (状态不变)", tc.name, err)
	}

	// 正常接收后重复接收：报已被接收，且状态不变。
	if _, err := n.Receive("b", msgID, 20); err != nil {
		t.Fatalf("receive: %v", err)
	}
	before := snapshot()
	if _, err := n.Receive("b", msgID, 20); !errors.Is(err, ErrMessageAlreadyReceived) {
		t.Fatalf("got %v want ErrMessageAlreadyReceived", err)
	}
	if after := snapshot(); before != after {
		t.Fatalf("state changed after duplicate receive rejection")
	}
	t.Logf("reject duplicate receive -> %v (状态不变)", ErrMessageAlreadyReceived)
}

// op 是因果测试脚本中的一步操作。
type op struct {
	kind     EventKind
	node     string
	peer     string // send: 目标节点
	sendIdx  int    // receive: 引用第几步 send
	physical int64
}

// recordedEvent 记录一步操作产生的 HLC 与 Lamport 参照时间。
type recordedEvent struct {
	node    string
	ts      Timestamp
	lamport uint64
}

// runScript 在全新网络上执行脚本，同步维护朴素 Lamport 因果参照，
// 返回事件序列与因果边（同节点相邻 + 发送->接收，用事件下标表示）。
func runScript(t *testing.T, script []op) ([]recordedEvent, [][2]int) {
	t.Helper()
	n := newTestNetwork(t, testConfig, "n1", "n2", "n3")

	var events []recordedEvent
	var edges [][2]int
	lastOnNode := map[string]int{}
	lamport := map[string]uint64{}
	sendMsgID := map[int]string{}  // send 步下标 -> 消息号
	sendEvent := map[int]int{}     // send 步下标 -> 发送事件下标
	msgLamport := map[int]uint64{} // send 步下标 -> 发送时的 Lamport 时间

	addEvent := func(node string, ts Timestamp) {
		lastOnNode[node] = len(events)
		events = append(events, recordedEvent{node, ts, lamport[node]})
	}

	for i, o := range script {
		switch o.kind {
		case EventLocal:
			ts, err := n.Local(o.node, o.physical)
			if err != nil {
				t.Fatalf("step %d local: %v", i, err)
			}
			lamport[o.node]++
			if prev, ok := lastOnNode[o.node]; ok {
				edges = append(edges, [2]int{prev, len(events)})
			}
			addEvent(o.node, ts)
			t.Logf("step%-2d local(%s, pt=%d) -> hlc=%s lamport=%d 依据: max(logical,pt)", i, o.node, o.physical, ts, lamport[o.node])
		case EventSend:
			id, ts, err := n.Send(o.node, o.peer, o.physical)
			if err != nil {
				t.Fatalf("step %d send: %v", i, err)
			}
			lamport[o.node]++
			sendMsgID[i] = id
			sendEvent[i] = len(events)
			msgLamport[i] = lamport[o.node]
			if prev, ok := lastOnNode[o.node]; ok {
				edges = append(edges, [2]int{prev, len(events)})
			}
			addEvent(o.node, ts)
			t.Logf("step%-2d send(%s->%s, pt=%d) -> msg=%s hlc=%s lamport=%d", i, o.node, o.peer, o.physical, id, ts, lamport[o.node])
		case EventReceive:
			s := o.sendIdx
			ts, err := n.Receive(o.node, sendMsgID[s], o.physical)
			if err != nil {
				t.Fatalf("step %d receive: %v", i, err)
			}
			if lamport[o.node] < msgLamport[s] {
				lamport[o.node] = msgLamport[s]
			}
			lamport[o.node]++
			if prev, ok := lastOnNode[o.node]; ok {
				edges = append(edges, [2]int{prev, len(events)})
			}
			edges = append(edges, [2]int{sendEvent[s], len(events)}) // 发送 -> 接收
			addEvent(o.node, ts)
			t.Logf("step%-2d receive(%s, %s, pt=%d) -> hlc=%s lamport=%d 依据: 并入消息时间", i, o.node, sendMsgID[s], o.physical, ts, lamport[o.node])
		}
	}
	return events, edges
}

var causalScript = []op{
	{EventLocal, "n1", "", 0, 10},
	{EventSend, "n1", "n2", 0, 10},
	{EventLocal, "n2", "", 0, 5}, // n2 物理时钟落后
	{EventReceive, "n2", "", 1, 6},
	{EventSend, "n2", "n3", 0, 6},
	{EventLocal, "n3", "", 0, 200}, // n3 物理时钟超前
	{EventSend, "n3", "n1", 0, 200},
	{EventReceive, "n1", "", 6, 3}, // n1 物理时钟回拨
	{EventReceive, "n3", "", 4, 150},
	{EventLocal, "n1", "", 0, 8},
}

// TestCausalConsistency 校验 HLC 时间戳与朴素 Lamport 因果参照一致：
// 每条因果边（同节点相邻、发送->接收）上两者都严格递增。
func TestCausalConsistency(t *testing.T) {
	events, edges := runScript(t, causalScript)
	for _, e := range edges {
		a, b := events[e[0]], events[e[1]]
		if a.ts.Compare(b.ts) >= 0 {
			t.Fatalf("HLC violates causality: %s@%s !< %s@%s", a.ts, a.node, b.ts, b.node)
		}
		if a.lamport >= b.lamport {
			t.Fatalf("lamport reference broken: %d !< %d", a.lamport, b.lamport)
		}
		t.Logf("causal edge %s@%s(hlc=%s L=%d) -> %s@%s(hlc=%s L=%d) 一致",
			a.node, a.ts, a.ts, a.lamport, b.node, b.ts, b.ts, b.lamport)
	}
}

// TestReproducible 同一事件序列重复执行得到完全相同的时间戳。
func TestReproducible(t *testing.T) {
	first, _ := runScript(t, causalScript)
	second, _ := runScript(t, causalScript)
	if len(first) != len(second) {
		t.Fatalf("event count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ts != second[i].ts {
			t.Fatalf("event %d not reproducible: %s vs %s", i, first[i].ts, second[i].ts)
		}
	}
	t.Logf("两次执行 %d 个事件的时间戳完全一致", len(first))
}

// TestConcurrent 多执行体并发调用：历史严格递增、发送<接收、无数据竞争。
func TestConcurrent(t *testing.T) {
	const nodes = 4
	const opsPerNode = 50
	n := newTestNetwork(t, testConfig, "n0", "n1", "n2", "n3")

	inbox := make([]chan string, nodes)
	for i := range inbox {
		inbox[i] = make(chan string, opsPerNode)
	}

	var wg sync.WaitGroup
	for i := 0; i < nodes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			self := fmt.Sprintf("n%d", i)
			peer := fmt.Sprintf("n%d", (i+1)%nodes)
			for k := 0; k < opsPerNode; k++ {
				pt := int64(1000 + k) // 各节点相同的物理读数序列
				if _, err := n.Local(self, pt); err != nil {
					t.Errorf("local: %v", err)
					return
				}
				msgID, _, err := n.Send(self, peer, pt)
				if err != nil {
					t.Errorf("send: %v", err)
					return
				}
				inbox[(i+1)%nodes] <- msgID
			}
			for k := 0; k < opsPerNode; k++ {
				msgID := <-inbox[i]
				if _, err := n.Receive(self, msgID, int64(1000+k)); err != nil {
					t.Errorf("receive: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	sendTS := map[string]Timestamp{}
	for i := 0; i < nodes; i++ {
		self := fmt.Sprintf("n%d", i)
		hist, err := n.History(self)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(hist) != 3*opsPerNode {
			t.Fatalf("%s: got %d events want %d", self, len(hist), 3*opsPerNode)
		}
		prev := Timestamp{}
		for _, ev := range hist {
			if prev.Compare(ev.Timestamp) >= 0 {
				t.Fatalf("%s: history not strictly increasing at %+v", self, ev)
			}
			prev = ev.Timestamp
			switch ev.Kind {
			case EventSend:
				sendTS[ev.MessageID] = ev.Timestamp
			case EventReceive:
				st, ok := sendTS[ev.MessageID]
				if ok && st.Compare(ev.Timestamp) >= 0 {
					t.Fatalf("receive %s (%s) not after send (%s)", ev.MessageID, ev.Timestamp, st)
				}
			}
		}
	}
	t.Logf("并发完成: %d 节点 x %d 事件, 全部历史严格递增且因果正确", nodes, 3*opsPerNode)
}
