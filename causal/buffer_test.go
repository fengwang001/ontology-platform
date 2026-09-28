package causal

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// msg 构造一条消息，Payload 用 "s#seq" 便于在断言中识别；
// sender 越界（用于构造非法输入用例）时 Payload 退化为 "s#?"。
func msg(sender int, vector ...int) *Message {
	payload := fmt.Sprintf("%d#?", sender)
	if sender >= 0 && sender < len(vector) {
		payload = fmt.Sprintf("%d#%d", sender, vector[sender])
	}
	return &Message{
		Sender:  sender,
		Vector:  append([]int(nil), vector...),
		Payload: payload,
	}
}

// newCapture 新建缓冲并返回一个日志捕获缓冲，测试结束时用 t.Log 打印
// 输入、交付结果与判定依据。
func newCapture(t *testing.T, n, capacity int) (*Buffer, *bytes.Buffer) {
	t.Helper()
	var logBuf bytes.Buffer
	b, err := NewWithLogger(n, capacity, &logBuf)
	if err != nil {
		t.Fatalf("NewWithLogger: %v", err)
	}
	t.Cleanup(func() {
		t.Helper()
		t.Logf("判定日志:\n%s", logBuf.String())
	})
	return b, &logBuf
}

func mustReceive(t *testing.T, b *Buffer, m *Message) ReceiveResult {
	t.Helper()
	res, err := b.Receive(m)
	if err != nil {
		t.Fatalf("Receive(%d#%d): unexpected error: %v", m.Sender, m.seq(), err)
	}
	return res
}

func expectReject(t *testing.T, b *Buffer, m *Message, want Reason) {
	t.Helper()
	res, err := b.Receive(m)
	if err == nil {
		t.Fatalf("Receive(%v): expected reject %s, got result %+v", m.Vector, want, res)
	}
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("expected *RejectError, got %T: %v", err, err)
	}
	if re.Reason != want {
		t.Fatalf("expected reason %s, got %s (%v)", want, re.Reason, err)
	}
	if len(res.Delivered) != 0 || res.Duplicate || res.Buffered {
		t.Fatalf("rejected call must return zero result, got %+v", res)
	}
}

// assertCausalOrder 校验交付序列：(发送方,序号) 唯一、每发送方序号连续，
// 且每条消息之前恰好已交付其向量所声明的各发送方消息数——即无超前、无遗漏。
func assertCausalOrder(t *testing.T, delivered []Message) {
	t.Helper()
	seen := map[[2]int]bool{}
	// deliveredFrom[i] 为截至当前已交付的发送方 i 的消息条数。
	counts := map[int]int{}
	maxSender := 0
	for _, m := range delivered {
		if m.Sender > maxSender {
			maxSender = m.Sender
		}
	}
	for pos, m := range delivered {
		key := [2]int{m.Sender, m.seq()}
		if seen[key] {
			t.Fatalf("message %d#%d delivered twice (pos %d)", m.Sender, m.seq(), pos)
		}
		seen[key] = true
		// 同一发送方必须按 1,2,3,... 连续交付。
		if counts[m.Sender]+1 != m.seq() {
			t.Fatalf("message %d#%d at pos %d breaks per-sender contiguity (had %d)",
				m.Sender, m.seq(), pos, counts[m.Sender])
		}
		// m 声明已见过 m.Vector[i] 条来自 i 的消息，它们必须全部在 m 之前。
		// 发送方自身分量的连续性已在上面校验；独立消息允许计数严格大于
		// 向量分量（交付顺序在并发消息间可以自由选择）。
		for i, v := range m.Vector {
			if i == m.Sender {
				continue
			}
			if counts[i] < v {
				t.Fatalf("message %d#%d at pos %d: vector[%d]=%d but only %d messages from %d precede it",
					m.Sender, m.seq(), pos, i, v, counts[i], i)
			}
		}
		counts[m.Sender]++
	}
}

func TestNewRejectsInvalidParams(t *testing.T) {
	if _, err := New(0, 4); err == nil {
		t.Fatal("New(0,4) should fail")
	}
	if _, err := New(2, -1); err == nil {
		t.Fatal("New(2,-1) should fail")
	}
}

func TestInOrderDelivery(t *testing.T) {
	b, _ := newCapture(t, 2, 4)

	r1 := mustReceive(t, b, msg(0, 1, 0))
	if len(r1.Delivered) != 1 || r1.Delivered[0].Payload != "0#1" {
		t.Fatalf("first message should deliver immediately, got %+v", r1)
	}
	r2 := mustReceive(t, b, msg(1, 1, 1))
	if len(r2.Delivered) != 1 || r2.Delivered[0].Payload != "1#1" {
		t.Fatalf("second message should deliver immediately, got %+v", r2)
	}
	if got := b.Vector(); fmt.Sprint(got) != "[1 1]" {
		t.Fatalf("clock = %v, want [1 1]", got)
	}
	if b.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", b.PendingCount())
	}
}

func TestOutOfOrderBufferingThenRelease(t *testing.T) {
	b, _ := newCapture(t, 2, 4)

	// 0#2 先到：0#1 缺失，只能缓冲。
	r := mustReceive(t, b, msg(0, 2, 0))
	if !r.Buffered || len(r.Delivered) != 0 {
		t.Fatalf("0#2 should buffer, got %+v", r)
	}
	if b.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", b.PendingCount())
	}
	if got := b.Vector(); fmt.Sprint(got) != "[0 0]" {
		t.Fatalf("clock must not advance on buffer, got %v", got)
	}

	// 0#1 到达后立即交付并级联放出 0#2。
	r = mustReceive(t, b, msg(0, 1, 0))
	if len(r.Delivered) != 2 {
		t.Fatalf("expected 2 deliveries (immediate+cascade), got %d", len(r.Delivered))
	}
	if r.Delivered[0].Payload != "0#1" || r.Delivered[1].Payload != "0#2" {
		t.Fatalf("delivery order = %v, want [0#1 0#2]", summarize(r.Delivered))
	}
	if b.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0 after cascade", b.PendingCount())
	}
	assertCausalOrder(t, b.Delivered())
}

func TestCascadeDeliveryMultipleHops(t *testing.T) {
	// 3 个发送方，依赖链：
	//   1#1=[1,1,0] 依赖 0#1
	//   0#2=[2,1,0] 依赖 0#1、1#1
	//   1#2=[2,2,0] 依赖 0#2
	//   2#1=[2,2,1] 依赖 1#2
	// 逆序到达后，0#1 到达应一次性级联放出全部。
	b, _ := newCapture(t, 3, 8)
	for _, m := range []*Message{
		msg(2, 2, 2, 1),
		msg(1, 2, 2, 0),
		msg(0, 2, 1, 0),
		msg(1, 1, 1, 0),
	} {
		r := mustReceive(t, b, m)
		if !r.Buffered {
			t.Fatalf("%d#%d should buffer, got %+v", m.Sender, m.seq(), r)
		}
	}
	if b.PendingCount() != 4 {
		t.Fatalf("pending = %d, want 4", b.PendingCount())
	}

	r := mustReceive(t, b, msg(0, 1, 0, 0))
	wantOrder := []string{"0#1", "1#1", "0#2", "1#2", "2#1"}
	if len(r.Delivered) != len(wantOrder) {
		t.Fatalf("cascade delivered %d, want %d (%s)", len(r.Delivered), len(wantOrder), summarize(r.Delivered))
	}
	for i, want := range wantOrder {
		if r.Delivered[i].Payload != want {
			t.Fatalf("delivery[%d] = %s, want %s; full %s", i, r.Delivered[i].Payload, want, summarize(r.Delivered))
		}
	}
	if got := b.Vector(); fmt.Sprint(got) != "[2 2 1]" {
		t.Fatalf("clock = %v, want [2 2 1]", got)
	}
	assertCausalOrder(t, b.Delivered())
}

func TestCascadePicksSmallestSenderFirst(t *testing.T) {
	// 0#1 到达后，1#1=[1,1,0] 与 2#1=[1,0,1] 同时可交付，
	// 级联规则要求每轮取发送方编号最小者：先 1#1 后 2#1。
	b, _ := newCapture(t, 3, 8)
	mustReceive(t, b, msg(1, 1, 1, 0)) // 依赖 0#1，缓冲
	mustReceive(t, b, msg(2, 1, 0, 1)) // 依赖 0#1，缓冲
	r := mustReceive(t, b, msg(0, 1, 0, 0))
	want := []string{"0#1", "1#1", "2#1"}
	if len(r.Delivered) != 3 {
		t.Fatalf("delivered %d, want 3", len(r.Delivered))
	}
	for i, w := range want {
		if r.Delivered[i].Payload != w {
			t.Fatalf("delivery[%d] = %s, want %s; order %s", i, r.Delivered[i].Payload, w, summarize(r.Delivered))
		}
	}
}

func TestDuplicateAlreadyDelivered(t *testing.T) {
	b, _ := newCapture(t, 2, 4)
	mustReceive(t, b, msg(0, 1, 0))
	mustReceive(t, b, msg(1, 1, 1))

	// 重放已交付消息：不是错误，Duplicate=true，计数 +1，交付序列不变。
	before := len(b.Delivered())
	clockBefore := fmt.Sprint(b.Vector())
	for i := 0; i < 3; i++ {
		r := mustReceive(t, b, msg(0, 1, 0))
		if !r.Duplicate || r.Buffered || len(r.Delivered) != 0 {
			t.Fatalf("replay #%d: expected duplicate, got %+v", i, r)
		}
	}
	if b.DuplicateCount() != 3 {
		t.Fatalf("dupCount = %d, want 3", b.DuplicateCount())
	}
	if len(b.Delivered()) != before {
		t.Fatal("delivered sequence changed by duplicate")
	}
	if got := fmt.Sprint(b.Vector()); got != clockBefore {
		t.Fatalf("clock changed by duplicate: %s vs %s", got, clockBefore)
	}
}

func TestDuplicateAlreadyBuffered(t *testing.T) {
	b, _ := newCapture(t, 2, 4)
	// 0#2 缓冲后再次收到同一消息：重复丢弃，缓冲中仍只有一份。
	mustReceive(t, b, msg(0, 2, 0))
	r := mustReceive(t, b, msg(0, 2, 0))
	if !r.Duplicate || r.Buffered {
		t.Fatalf("expected buffered duplicate, got %+v", r)
	}
	if b.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1 (duplicate must not be enqueued)", b.PendingCount())
	}
	if b.DuplicateCount() != 1 {
		t.Fatalf("dupCount = %d, want 1", b.DuplicateCount())
	}
	// 0#1 到达后 0#2 只交付一次。
	r = mustReceive(t, b, msg(0, 1, 0))
	if len(r.Delivered) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(r.Delivered))
	}
	assertCausalOrder(t, b.Delivered())
}

func TestInvalidInputs(t *testing.T) {
	b, _ := newCapture(t, 2, 4)
	// 先制造一些正常状态，随后所有拒绝都必须不改变它。
	mustReceive(t, b, msg(0, 1, 0))
	snapshotClock := fmt.Sprint(b.Vector())
	snapshotDelivered := len(b.Delivered())
	snapshotPending := b.PendingCount()
	snapshotDup := b.DuplicateCount()

	cases := []struct {
		name string
		m    *Message
		want Reason
	}{
		{"nil message", nil, ReasonInvalidArgument},
		{"sender negative", msg(-1, 1, 0), ReasonSenderOutOfRange},
		{"sender too large", msg(2, 0, 0, 1), ReasonSenderOutOfRange},
		{"vector too short", msg(0, 1), ReasonInvalidVector},
		{"vector too long", msg(0, 1, 0, 0), ReasonInvalidVector},
		{"negative component", msg(1, 0, -1), ReasonInvalidVector},
		{"sender component zero", msg(1, 0, 0), ReasonInvalidVector},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectReject(t, b, c.m, c.want)
		})
	}

	if fmt.Sprint(b.Vector()) != snapshotClock {
		t.Fatalf("clock changed after rejects: %v", b.Vector())
	}
	if len(b.Delivered()) != snapshotDelivered ||
		b.PendingCount() != snapshotPending ||
		b.DuplicateCount() != snapshotDup {
		t.Fatalf("state changed after rejects: delivered=%d pending=%d dup=%d",
			len(b.Delivered()), b.PendingCount(), b.DuplicateCount())
	}
}

func TestBufferFullRejectionLeavesStateUntouched(t *testing.T) {
	// capacity=1：0#2=[2,0,0] 因缺口缓冲；下一条无法立即交付的消息
	// 必须以 buffer_full 拒绝，且向量、缓冲、交付序列、重复计数不变。
	b, _ := newCapture(t, 2, 1)
	r := mustReceive(t, b, msg(0, 2, 0))
	if !r.Buffered {
		t.Fatalf("0#2 should buffer, got %+v", r)
	}

	clockBefore := fmt.Sprint(b.Vector())
	expectReject(t, b, msg(1, 0, 2), ReasonBufferFull)

	if b.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", b.PendingCount())
	}
	if got := fmt.Sprint(b.Vector()); got != clockBefore {
		t.Fatalf("clock changed: %s vs %s", got, clockBefore)
	}
	if len(b.Delivered()) != 0 || b.DuplicateCount() != 0 {
		t.Fatalf("delivered=%d dup=%d, want 0/0", len(b.Delivered()), b.DuplicateCount())
	}

	// 可立即交付的消息不受满缓冲影响：1#1=[0,1,0] 直接交付。
	r = mustReceive(t, b, msg(1, 0, 1))
	if len(r.Delivered) != 1 {
		t.Fatalf("deliverable message must bypass full buffer, got %+v", r)
	}
}

func TestDeterministicForSameSequence(t *testing.T) {
	// 同一输入序列反复计算得到完全相同的输出。
	sequence := []*Message{
		msg(2, 2, 2, 1),
		msg(0, 2, 1, 0),
		msg(1, 1, 1, 0),
		msg(1, 2, 2, 0),
		msg(0, 1, 0, 0),
	}
	run := func() []string {
		b, err := NewWithLogger(3, 8, nil)
		if err != nil {
			t.Fatal(err)
		}
		var order []string
		for _, m := range sequence {
			r, err := b.Receive(m)
			if err != nil {
				t.Fatalf("Receive: %v", err)
			}
			for _, d := range r.Delivered {
				order = append(order, d.Payload.(string))
			}
		}
		return order
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("run %d = %v, want %v", i, got, first)
		}
	}
	want := []string{"0#1", "1#1", "0#2", "1#2", "2#1"}
	if fmt.Sprint(first) != fmt.Sprint(want) {
		t.Fatalf("delivery = %v, want %v", first, want)
	}
}

func TestConcurrentCausallyClosedSet(t *testing.T) {
	// 一个因果闭合的消息集合，乱序并发到达：全部交付、每条恰好一次、
	// 因果序成立、最终向量一致。多轮重复以提高暴露竞态的概率。
	messages := []*Message{
		msg(0, 1, 0, 0),
		msg(1, 0, 1, 0),
		msg(2, 0, 0, 1),
		msg(0, 2, 1, 0), // 依赖 1#1
		msg(1, 1, 2, 0), // 依赖 0#1
		msg(2, 2, 2, 2), // 2#2，依赖 0#2、1#2、2#1
	}
	const iterations = 50
	for iter := 0; iter < iterations; iter++ {
		b, err := NewWithLogger(3, len(messages), nil)
		if err != nil {
			t.Fatal(err)
		}

		shuffled := append([]*Message(nil), messages...)
		rand.New(rand.NewSource(int64(iter))).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		// 两次栅栏保证所有 goroutine 尽可能同时开始 Receive。
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, m := range shuffled {
			wg.Add(1)
			go func(m *Message) {
				defer wg.Done()
				<-start
				if _, err := b.Receive(m); err != nil {
					t.Errorf("Receive(%d#%d): %v", m.Sender, m.seq(), err)
				}
			}(m)
		}
		close(start)
		wg.Wait()

		delivered := b.Delivered()
		if len(delivered) != len(messages) {
			t.Fatalf("iter %d: delivered %d, want %d; pending=%d",
				iter, len(delivered), len(messages), b.PendingCount())
		}
		assertCausalOrder(t, delivered)
		if got := fmt.Sprint(b.Vector()); got != "[2 2 2]" {
			t.Fatalf("iter %d: clock = %v, want [2 2 2]", iter, b.Vector())
		}
		if b.DuplicateCount() != 0 || b.PendingCount() != 0 {
			t.Fatalf("iter %d: dup=%d pending=%d, want 0/0",
				iter, b.DuplicateCount(), b.PendingCount())
		}
	}
}

func TestConcurrentDuplicatesCounted(t *testing.T) {
	// 并发重放同一条已交付消息：重复计数必须精确，且交付序列不变。
	b, err := NewWithLogger(2, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(msg(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	const dupes = 100
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < dupes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := b.Receive(msg(0, 1, 0))
			if err != nil || !r.Duplicate {
				t.Errorf("expected duplicate, got %+v err=%v", r, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if b.DuplicateCount() != dupes {
		t.Fatalf("dupCount = %d, want %d", b.DuplicateCount(), dupes)
	}
	if len(b.Delivered()) != 1 {
		t.Fatalf("delivered = %d, want 1", len(b.Delivered()))
	}
}

func TestLogContainsInputDecisionAndBasis(t *testing.T) {
	// 日志中必须能看到输入、交付结果与判定依据。
	b, logBuf := newCapture(t, 2, 4)
	if _, err := b.Receive(msg(0, 2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(msg(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	out := logBuf.String()
	for _, want := range []string{"receive", "input", "hold", "basis", "gap", "deliver", "cascade"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q\n%s", want, out)
		}
	}
}
