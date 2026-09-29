package hlc

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// stepLogger 记录并打印每一步的输入、产生的时间戳与判定依据。
type stepLogger struct {
	t    *testing.T
	sb   strings.Builder
	step int
}

func newStepLogger(t *testing.T) *stepLogger {
	t.Helper()
	l := &stepLogger{t: t}
	l.line("init", "混合逻辑时钟场景开始", nil, nil)
	return l
}

func (l *stepLogger) line(op, input string, ts *Timestamp, err error) {
	l.step++
	text := fmt.Sprintf("step %02d | %-8s | input: %-46s", l.step, op, input)
	if ts != nil {
		text += fmt.Sprintf(" | ts=%s", ts)
	}
	if err == nil {
		text += " | verdict=accept（l=max(l,pt)，同毫秒计数加一）"
	} else {
		text += " | verdict=reject: " + err.Error() + "（失败不留痕）"
	}
	l.t.Log(text)
	l.sb.WriteString(text + "\n")
}

func rejectReason(err error) RejectReason {
	if err == nil {
		return ReasonOK
	}
	if re, ok := err.(*RejectError); ok {
		return re.Reason
	}
	return -1
}

// naiveVectorClock 是朴素因果参照：本地/发送自增本分量，接收先并入消息向量再自增。
type naiveVectorClock struct {
	nodes []string
	idx   map[string]int
	state []map[string]int64
	msgs  map[int64][]int64
}

func newNaive(nodes []string) *naiveVectorClock {
	idx := map[string]int{}
	state := make([]map[string]int64, len(nodes))
	for i, n := range nodes {
		idx[n] = i
		state[i] = map[string]int64{}
	}
	return &naiveVectorClock{nodes: nodes, idx: idx, state: state, msgs: map[int64][]int64{}}
}

func (v *naiveVectorClock) tick(n string) []int64 {
	cur := v.state[v.idx[n]]
	cur[n]++
	return v.snapshot(cur)
}

func (v *naiveVectorClock) send(n string, id int64) []int64 {
	vec := v.tick(n)
	v.msgs[id] = vec
	return vec
}

func (v *naiveVectorClock) receive(n string, id int64) []int64 {
	cur := v.state[v.idx[n]]
	for i, name := range v.nodes {
		if m := v.msgs[id][i]; m > cur[name] {
			cur[name] = m
		}
	}
	cur[n]++
	return v.snapshot(cur)
}

func (v *naiveVectorClock) snapshot(cur map[string]int64) []int64 {
	vec := make([]int64, len(v.nodes))
	for i, name := range v.nodes {
		vec[i] = cur[name]
	}
	return vec
}

func vcHappensBefore(a, b []int64) bool {
	less, greater := false, false
	for i := range a {
		switch {
		case a[i] < b[i]:
			less = true
		case a[i] > b[i]:
			greater = true
		}
	}
	return less && !greater
}

// refEvent 把一次 HLC 操作与朴素向量时钟参照结果绑定，用于校验因果一致性。
type refEvent struct {
	ts  Timestamp
	vec []int64
	msg int64
}

// assertCausalConsistency 用朴素向量时钟作为因果参照：
// 凡参照中 e1 因果先于 e2，HLC 时间戳也必须严格更小。
// 跨节点并发事件允许时间戳相等（需要节点 ID 才能破平，不属于本实现范围）。
func assertCausalConsistency(t *testing.T, events []refEvent) {
	t.Helper()
	for i := range events {
		for j := range events {
			if i == j {
				continue
			}
			ei, ej := events[i], events[j]
			switch {
			case vcHappensBefore(ei.vec, ej.vec):
				if !ei.ts.Less(ej.ts) {
					t.Fatalf("因果不一致: vec %v -> %v 但 HLC %s !< %s",
						ei.vec, ej.vec, ei.ts, ej.ts)
				}
			case vcHappensBefore(ej.vec, ei.vec):
				if !ej.ts.Less(ei.ts) {
					t.Fatalf("因果不一致: vec %v -> %v 但 HLC %s !< %s",
						ej.vec, ei.vec, ej.ts, ei.ts)
				}
			}
		}
	}
}

func TestLocalSendReceiveAndRollback(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	clk := New(Config{MaxDrift: 1000, MaxCounter: 8}, nodes...)
	vc := newNaive(nodes)
	log := newStepLogger(t)
	var refs []refEvent

	ts1, err := clk.LocalTick("a", 10)
	log.line("local", "node=a pt=10（首事件）", &ts1, err)
	refs = append(refs, refEvent{ts1, vc.tick("a"), 0})
	if err != nil || ts1 != (Timestamp{10, 0}) {
		t.Fatalf("local first: ts=%v err=%v", ts1, err)
	}

	// 物理时钟回拨：l 保持 10，计数加一，时间戳仍严格递增。
	ts2, err := clk.LocalTick("a", 9)
	log.line("local", "node=a pt=9（物理时钟回拨）", &ts2, err)
	refs = append(refs, refEvent{ts2, vc.tick("a"), 0})
	if err != nil || ts2 != (Timestamp{10, 1}) || !ts1.Less(ts2) {
		t.Fatalf("rollback local: ts=%v err=%v", ts2, err)
	}

	ts3, err := clk.LocalTick("a", 10)
	log.line("local", "node=a pt=10（同一毫秒）", &ts3, err)
	refs = append(refs, refEvent{ts3, vc.tick("a"), 0})
	if err != nil || ts3 != (Timestamp{10, 2}) {
		t.Fatalf("same-ms local: ts=%v err=%v", ts3, err)
	}

	msg, err := clk.Send("a", "b", 8)
	log.line("send", fmt.Sprintf("from=a to=b pt=8（回拨）-> msg#%d", msg.ID), &msg.SendAt, err)
	refs = append(refs, refEvent{msg.SendAt, vc.send("a", msg.ID), msg.ID})
	if err != nil || msg.SendAt != (Timestamp{10, 3}) {
		t.Fatalf("send: msg=%+v err=%v", msg, err)
	}

	tsb, err := clk.LocalTick("b", 12)
	log.line("local", "node=b pt=12", &tsb, err)
	refs = append(refs, refEvent{tsb, vc.tick("b"), 0})

	// 本地 l 领先：b=12.0，消息=10.3，接收取本地分支 c+1。
	r1, err := clk.Receive("b", msg.ID, 11)
	log.line("receive", fmt.Sprintf("node=b msg#%d pt=11（本地l领先）", msg.ID), &r1, err)
	refs = append(refs, refEvent{r1, vc.receive("b", msg.ID), msg.ID})
	if err != nil || r1 != (Timestamp{12, 1}) || !msg.SendAt.Less(r1) {
		t.Fatalf("receive local-leading: ts=%v err=%v", r1, err)
	}

	// 远程 l 领先：c=0.0，消息携带 20.x；即便接收读数回拨到 15 也严格大于发送。
	msg2, err := clk.Send("a", "c", 20)
	log.line("send", fmt.Sprintf("from=a to=c pt=20 -> msg#%d", msg2.ID), &msg2.SendAt, err)
	refs = append(refs, refEvent{msg2.SendAt, vc.send("a", msg2.ID), msg2.ID})
	r2, err := clk.Receive("c", msg2.ID, 15)
	log.line("receive", fmt.Sprintf("node=c msg#%d pt=15（远程l领先）", msg2.ID), &r2, err)
	refs = append(refs, refEvent{r2, vc.receive("c", msg2.ID), msg2.ID})
	if err != nil || r2 != (Timestamp{20, 1}) || !msg2.SendAt.Less(r2) {
		t.Fatalf("receive remote-leading: ts=%v err=%v", r2, err)
	}

	// 物理领先分支。
	msg3, err := clk.Send("a", "b", 21)
	log.line("send", fmt.Sprintf("from=a to=b pt=21 -> msg#%d", msg3.ID), &msg3.SendAt, err)
	refs = append(refs, refEvent{msg3.SendAt, vc.send("a", msg3.ID), msg3.ID})
	r3, err := clk.Receive("b", msg3.ID, 30)
	log.line("receive", fmt.Sprintf("node=b msg#%d pt=30（物理领先）", msg3.ID), &r3, err)
	refs = append(refs, refEvent{r3, vc.receive("b", msg3.ID), msg3.ID})
	if err != nil || r3 != (Timestamp{30, 0}) {
		t.Fatalf("receive physical-leading: ts=%v err=%v", r3, err)
	}

	// 相等合并分支：双方 l 同为 30，c 取 max+1。
	msg4, err := clk.Send("a", "b", 30)
	log.line("send", fmt.Sprintf("from=a to=b pt=30 -> msg#%d ts=%s", msg4.ID, msg4.SendAt), &msg4.SendAt, err)
	refs = append(refs, refEvent{msg4.SendAt, vc.send("a", msg4.ID), msg4.ID})
	r4, err := clk.Receive("b", msg4.ID, 30)
	log.line("receive", fmt.Sprintf("node=b msg#%d pt=30（l相等，c=max+1）", msg4.ID), &r4, err)
	refs = append(refs, refEvent{r4, vc.receive("b", msg4.ID), msg4.ID})
	if err != nil || r4 != (Timestamp{30, 1}) || !msg4.SendAt.Less(r4) {
		t.Fatalf("receive tie: ts=%v err=%v", r4, err)
	}

	for _, n := range nodes {
		hist, herr := clk.History(n)
		if herr != nil {
			t.Fatal(herr)
		}
		for i := 1; i < len(hist); i++ {
			if !hist[i-1].Timestamp.Less(hist[i].Timestamp) {
				t.Fatalf("节点 %s 历史未严格有序: %s !< %s",
					n, hist[i-1].Timestamp, hist[i].Timestamp)
			}
		}
	}
	if pending := clk.PendingMessages(); len(pending) != 0 {
		t.Fatalf("四条消息均应已接收，仍有在途: %+v", pending)
	}

	assertCausalConsistency(t, refs)
	log.line("check", "全部事件与朴素向量时钟因果参照一致", nil, nil)
}

func TestDriftAndCounterLimits(t *testing.T) {
	log := newStepLogger(t)

	t.Run("drift_exceeded_local", func(t *testing.T) {
		clk := New(Config{MaxDrift: 5, MaxCounter: 100}, "a")
		if _, err := clk.LocalTick("a", 100); err != nil {
			t.Fatal(err)
		}
		_, err := clk.LocalTick("a", 90)
		log.line("local", "node=a pt=90, maxDrift=5（偏差10超限）", nil, err)
		if rejectReason(err) != ReasonDriftExceeded {
			t.Fatalf("want drift_exceeded, got %v", err)
		}
		if now, _ := clk.Now("a"); now != (Timestamp{100, 0}) {
			t.Fatalf("拒绝后时钟被改动: %v", now)
		}
	})

	t.Run("drift_boundary_accepted", func(t *testing.T) {
		clk := New(Config{MaxDrift: 5, MaxCounter: 100}, "a")
		if _, err := clk.LocalTick("a", 100); err != nil {
			t.Fatal(err)
		}
		ts, err := clk.LocalTick("a", 95) // 偏差恰为 5，边界内
		log.line("local", "node=a pt=95, maxDrift=5（偏差恰为5，边界内）", &ts, err)
		if err != nil || ts != (Timestamp{100, 1}) {
			t.Fatalf("boundary: ts=%v err=%v", ts, err)
		}
	})

	t.Run("drift_exceeded_receive", func(t *testing.T) {
		clk := New(Config{MaxDrift: 5, MaxCounter: 100}, "a", "b")
		msg, _ := clk.Send("a", "b", 100)
		_, err := clk.Receive("b", msg.ID, 50)
		log.line("receive", fmt.Sprintf("node=b msg#%d pt=50（远程领先50，超限）", msg.ID), nil, err)
		if rejectReason(err) != ReasonDriftExceeded {
			t.Fatalf("want drift_exceeded, got %v", err)
		}
		if got, _ := clk.LookupMessage(msg.ID); got.Delivered {
			t.Fatal("拒绝后消息被标记为已接收")
		}
	})

	t.Run("counter_overflow_boundary", func(t *testing.T) {
		clk := New(Config{MaxDrift: 1 << 40, MaxCounter: 2}, "a")
		want := []Timestamp{{10, 0}, {10, 1}, {10, 2}}
		for i, w := range want {
			ts, err := clk.LocalTick("a", 10)
			log.line("local", fmt.Sprintf("node=a pt=10 第%d个, maxCounter=2", i+1), &ts, err)
			if err != nil || ts != w {
				t.Fatalf("i=%d ts=%v err=%v want %v", i, ts, err, w)
			}
		}
		_, err := clk.LocalTick("a", 10)
		log.line("local", "node=a pt=10 第4个（c=3 计数超限）", nil, err)
		if rejectReason(err) != ReasonCounterOverflow {
			t.Fatalf("want counter_overflow, got %v", err)
		}
		if now, _ := clk.Now("a"); now != want[2] {
			t.Fatalf("拒绝后时钟被改动: %v", now)
		}
	})

	t.Run("counter_overflow_receive", func(t *testing.T) {
		clk := New(Config{MaxDrift: 1 << 40, MaxCounter: 1}, "a", "b")
		m1, _ := clk.Send("a", "b", 10)
		r1, err := clk.Receive("b", m1.ID, 10)
		log.line("receive", fmt.Sprintf("node=b msg#%d（c=1 边界内）", m1.ID), &r1, err)
		if err != nil {
			t.Fatal(err)
		}
		m2, _ := clk.Send("a", "b", 10)
		_, err = clk.Receive("b", m2.ID, 10)
		log.line("receive", fmt.Sprintf("node=b msg#%d（max(c)+1=2 超限）", m2.ID), nil, err)
		if rejectReason(err) != ReasonCounterOverflow {
			t.Fatalf("want counter_overflow, got %v", err)
		}
		if got, _ := clk.LookupMessage(m2.ID); got.Delivered {
			t.Fatal("拒绝后消息被标记为已接收")
		}
	})
}

func TestRejectionsLeaveNoTrace(t *testing.T) {
	clk := New(Config{MaxDrift: 1000, MaxCounter: 100}, "a", "b")
	msg, _ := clk.Send("a", "b", 10)
	if _, err := clk.Receive("b", msg.ID, 10); err != nil {
		t.Fatal(err)
	}
	wrongTargetMsg, err := clk.Send("a", "b", 11)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		ha, _ := clk.History("a")
		hb, _ := clk.History("b")
		na, _ := clk.Now("a")
		nb, _ := clk.Now("b")
		return fmt.Sprintf("now:a=%s,b=%s len:a=%d,b=%d pending=%d nextMsg=%d",
			na, nb, len(ha), len(hb), len(clk.PendingMessages()), clk.nextMsg)
	}
	before := snapshot()
	log := newStepLogger(t)

	type probe struct {
		name string
		call func() error
		want RejectReason
	}
	probes := []probe{
		{"local_empty_node", func() error { _, e := clk.LocalTick("  ", 10); return e }, ReasonInvalidArgument},
		{"send_empty_from", func() error { _, e := clk.Send("", "b", 10); return e }, ReasonInvalidArgument},
		{"send_empty_to", func() error { _, e := clk.Send("a", "", 10); return e }, ReasonInvalidArgument},
		{"local_unknown_node", func() error { _, e := clk.LocalTick("ghost", 10); return e }, ReasonNodeNotFound},
		{"send_unknown_from", func() error { _, e := clk.Send("ghost", "b", 10); return e }, ReasonNodeNotFound},
		{"send_unknown_to", func() error { _, e := clk.Send("a", "ghost", 10); return e }, ReasonNodeNotFound},
		{"receive_unknown_node", func() error { _, e := clk.Receive("ghost", msg.ID, 10); return e }, ReasonNodeNotFound},
		{"local_negative_pt", func() error { _, e := clk.LocalTick("a", -1); return e }, ReasonNegativePhysical},
		{"send_negative_pt", func() error { _, e := clk.Send("a", "b", -1); return e }, ReasonNegativePhysical},
		{"receive_negative_pt", func() error { _, e := clk.Receive("b", msg.ID, -1); return e }, ReasonNegativePhysical},
		{"receive_msg_zero", func() error { _, e := clk.Receive("b", 0, 10); return e }, ReasonMessageNotFound},
		{"receive_missing_msg", func() error { _, e := clk.Receive("b", 9999, 10); return e }, ReasonMessageNotFound},
		{"receive_twice", func() error { _, e := clk.Receive("b", msg.ID, 10); return e }, ReasonMessageAlreadyReceived},
		{"receive_wrong_target", func() error {
			_, e := clk.Receive("a", wrongTargetMsg.ID, 11)
			return e
		}, ReasonTargetMismatch},
	}
	for _, p := range probes {
		err := p.call()
		log.line("reject", p.name, nil, err)
		if rejectReason(err) != p.want {
			t.Fatalf("%s: want %s got %v", p.name, p.want, err)
		}
		if after := snapshot(); after != before {
			t.Fatalf("%s 被拒后状态发生变化:\nbefore=%s\nafter =%s", p.name, before, after)
		}
	}

	// 所有拒绝原因必须互不相同。
	seen := map[RejectReason]string{}
	for _, r := range []RejectReason{
		ReasonInvalidArgument, ReasonNodeNotFound, ReasonNegativePhysical,
		ReasonMessageNotFound, ReasonMessageAlreadyReceived,
		ReasonDriftExceeded, ReasonCounterOverflow, ReasonTargetMismatch,
	} {
		if prev, dup := seen[r]; dup {
			t.Fatalf("重复的拒绝原因: %s 与 %s", prev, r)
		}
		seen[r] = r.String()
		if r.String() == "unknown" {
			t.Fatalf("原因 %d 缺少名称", r)
		}
	}
}

// scriptedOp 是一条确定性操作脚本，用于复现性与并发测试。
type scriptedOp struct {
	kind    string
	node    string
	to      string
	pt      int64
	recvMsg int // 引用此前第几条 send 的消息 ID，-1 表示无
}

func buildScript() []scriptedOp {
	return []scriptedOp{
		{"local", "a", "", 100, -1},
		{"local", "b", "", 102, -1},
		{"send", "a", "b", 101, -1},
		{"send", "b", "a", 103, -1},
		{"local", "a", "", 99, -1},   // 时钟回拨
		{"receive", "a", "", 104, 2}, // 接收第 2 条 send（b->a）
		{"receive", "b", "", 104, 1}, // 接收第 1 条 send（a->b）
		{"send", "a", "c", 105, -1},
		{"local", "c", "", 105, -1},
		{"receive", "c", "", 106, 3}, // 接收第 3 条 send（a->c）
		{"send", "c", "a", 107, -1},
		{"receive", "a", "", 108, 4}, // 接收第 4 条 send（c->a）
		{"local", "b", "", 108, -1},
		{"send", "b", "c", 109, -1},
		{"receive", "c", "", 110, 5}, // 接收第 5 条 send（b->c）
	}
}

// runScript 串行执行脚本，返回每条操作的时间戳（接收/本地/发送事件）。
func runScript(t *testing.T, clk *Clock, ops []scriptedOp) []Timestamp {
	t.Helper()
	msgIDs := map[int]int64{}
	sendCount := 0
	out := make([]Timestamp, len(ops))
	for i, op := range ops {
		switch op.kind {
		case "local":
			ts, err := clk.LocalTick(op.node, op.pt)
			if err != nil {
				t.Fatalf("op %d local: %v", i, err)
			}
			out[i] = ts
		case "send":
			m, err := clk.Send(op.node, op.to, op.pt)
			if err != nil {
				t.Fatalf("op %d send: %v", i, err)
			}
			sendCount++
			msgIDs[sendCount] = m.ID
			out[i] = m.SendAt
		case "receive":
			ts, err := clk.Receive(op.node, msgIDs[op.recvMsg], op.pt)
			if err != nil {
				t.Fatalf("op %d receive: %v", i, err)
			}
			out[i] = ts
		}
	}
	return out
}

func TestDeterministicReplay(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	ops := buildScript()
	first := runScript(t, New(Config{MaxDrift: 1000, MaxCounter: 100}, nodes...), ops)
	second := runScript(t, New(Config{MaxDrift: 1000, MaxCounter: 100}, nodes...), ops)
	for i := range first {
		if !first[i].Equal(second[i]) {
			t.Fatalf("同一事件序列结果不可复现: op %d 第一次=%s 第二次=%s", i, first[i], second[i])
		}
	}
	t.Logf("串行复现一致: %d 个事件时间戳完全相同: %v", len(first), first)
}

func TestConcurrentSameTimestamps(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	ops := buildScript()
	log := newStepLogger(t)

	// 参照结果：串行执行。
	want := runScript(t, New(Config{MaxDrift: 1000, MaxCounter: 100}, nodes...), ops)

	// 并发执行：每条操作一个 goroutine，但通过依赖通道保证 receive
	// 发生在对应 send 之后，并且同一节点上的操作按脚本序获取执行权。
	// 在“事件序列相同（同节点先后序 + 消息收发因果）”这一前提下，
	// HLC 时间戳与调度无关，必须与串行参照完全一致。
	clk := New(Config{MaxDrift: 1000, MaxCounter: 100}, nodes...)
	results := make([]Timestamp, len(ops))
	var wg sync.WaitGroup
	sendCh := make(map[int]chan int64) // send 序号（从 1 起）-> 消息 ID
	sendSeqOf := map[int]int{}         // receive 的 op 下标 -> send 序号
	sendSeqAt := map[int]int{}         // send 的 op 下标 -> send 序号
	sendSeq := 0
	// 每个节点用一条“执行权通道链”保证脚本中同节点事件按序提交，
	// 不同节点之间则完全并发。
	for i, op := range ops {
		if op.kind == "send" {
			sendSeq++
			sendCh[sendSeq] = make(chan int64, 1)
			sendSeqAt[i] = sendSeq
		}
		if op.kind == "receive" {
			sendSeqOf[i] = op.recvMsg
		}
	}

	start := make(chan struct{})
	// 为每条 op 计算它在本节点通道链上的前驱与后继。
	myTurn := map[int]chan struct{}{}
	pass := map[int]chan struct{}{}
	last := map[string]chan struct{}{}
	for _, n := range nodes {
		ch := make(chan struct{}, 1)
		ch <- struct{}{}
		last[n] = ch
	}
	for i, op := range ops {
		myTurn[i] = last[op.node]
		next := make(chan struct{}, 1)
		pass[i] = next
		last[op.node] = next
	}

	for i, op := range ops {
		wg.Add(1)
		go func(i int, op scriptedOp) {
			defer wg.Done()
			<-start
			<-myTurn[i] // 等待本节点上一操作完成，保证节点内序列一致
			release := func() { pass[i] <- struct{}{} }
			if op.kind == "receive" {
				id := <-sendCh[sendSeqOf[i]] // 等待消息被发送
				ts, err := clk.Receive(op.node, id, op.pt)
				defer release()
				if err != nil {
					t.Errorf("op %d receive: %v", i, err)
					return
				}
				results[i] = ts
				return
			}
			switch op.kind {
			case "local":
				ts, err := clk.LocalTick(op.node, op.pt)
				defer release()
				if err != nil {
					t.Errorf("op %d local: %v", i, err)
					return
				}
				results[i] = ts
			case "send":
				m, err := clk.Send(op.node, op.to, op.pt)
				defer release()
				if err != nil {
					t.Errorf("op %d send: %v", i, err)
					return
				}
				results[i] = m.SendAt
				sendCh[sendSeqAt[i]] <- m.ID
			}
		}(i, op)
	}
	close(start)
	wg.Wait()

	for i := range want {
		if !results[i].Equal(want[i]) {
			t.Fatalf("并发结果与串行参照不一致: op %d (%+v) 并发=%s 串行=%s",
				i, ops[i], results[i], want[i])
		}
		log.line(ops[i].kind,
			fmt.Sprintf("op=%02d node=%s pt=%d 并发与串行一致", i, ops[i].node, ops[i].pt),
			&results[i], nil)
	}

	// 并发结束后，各节点历史仍严格有序，消息全部恰好接收一次。
	for _, n := range nodes {
		hist, _ := clk.History(n)
		for j := 1; j < len(hist); j++ {
			if !hist[j-1].Timestamp.Less(hist[j].Timestamp) {
				t.Fatalf("并发后节点 %s 历史非有序: %s !< %s",
					n, hist[j-1].Timestamp, hist[j].Timestamp)
			}
		}
	}
	if pending := clk.PendingMessages(); len(pending) != 0 {
		t.Fatalf("并发后仍有在途消息: %+v", pending)
	}
}

func TestConcurrentRejectedCallsHarmless(t *testing.T) {
	// 高并发下混合合法与非法调用：非法调用永不落痕，合法事件严格递增。
	clk := New(Config{MaxDrift: 1 << 40, MaxCounter: 1 << 20}, "a", "b")
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				switch i % 4 {
				case 0:
					_, _ = clk.LocalTick("a", 200)
				case 1:
					_, _ = clk.LocalTick("ghost", 200)
				case 2:
					_, _ = clk.LocalTick("a", -1)
				case 3:
					_, _ = clk.Send("a", "b", 200)
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()

	hist, _ := clk.History("a")
	if len(hist) != 16*25 { // 每 goroutine 25 次合法事件
		t.Fatalf("非法调用疑似落痕或合法事件丢失: %d", len(hist))
	}
	for i := 1; i < len(hist); i++ {
		if !hist[i-1].Timestamp.Less(hist[i].Timestamp) {
			t.Fatalf("高并发下时间戳未严格递增: %s -> %s",
				hist[i-1].Timestamp, hist[i].Timestamp)
		}
	}
}
