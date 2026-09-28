package causalbuf

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func msg(sender int, vec []int, payload any) Message {
	return Message{Sender: sender, Vector: append([]int(nil), vec...), Payload: payload}
}

func keyOf(m Message) bufKey {
	return bufKey{m.Sender, m.Vector[m.Sender]}
}

// 乱序到达 + 级联交付：B1 先到；A2、B1-on-A1 等消息被缓冲；A1 到达后全部级联交付。
func TestOutOfOrderAndCascade(t *testing.T) {
	b, err := New(3, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// B1=[0,1,0] 因果上不依赖任何人，立即交付。
	r, err := b.Receive(msg(1, []int{0, 1, 0}, "B1"))
	if err != nil || r.Outcome != OutcomeDelivered || len(r.Delivered) != 1 {
		t.Fatalf("B1: result=%+v err=%v", r, err)
	}

	// A2=[2,1,0]：自身序号 2 超前本地 0，缓冲等待 A1。
	r, err = b.Receive(msg(0, []int{2, 1, 0}, "A2"))
	if err != nil || r.Outcome != OutcomeBuffered {
		t.Fatalf("A2: result=%+v err=%v", r, err)
	}
	if b.BufferedCount() != 1 {
		t.Fatalf("buffered count = %d, want 1", b.BufferedCount())
	}

	// A1=[1,0,0] 到达：交付 A1，并级联交付 A2。
	r, err = b.Receive(msg(0, []int{1, 0, 0}, "A1"))
	if err != nil {
		t.Fatalf("A1: %v", err)
	}
	got := payloads(r.Delivered)
	want := []any{"A1", "A2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cascade order = %v, want %v", got, want)
	}
	if got, want := b.Vector(), []int{2, 1, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("local vc = %v, want %v", got, want)
	}
	if b.BufferedCount() != 0 {
		t.Fatalf("pending should be drained, got %d", b.BufferedCount())
	}

	// B2=[1,2,0] 因果依赖 A1/B1，均已交付，立即交付。
	r, _ = b.Receive(msg(1, []int{1, 2, 0}, "B2"))
	if r.Outcome != OutcomeDelivered || len(r.Delivered) != 1 {
		t.Fatalf("B2: result=%+v", r)
	}
}

// 级联裁决顺序：多个发送方的消息在同一轮变可交付时，按发送方编号最小者依次交付。
func TestCascadeOrderBySmallestSender(t *testing.T) {
	b, _ := New(3, 10)

	// B1、C1 都因果依赖 A1，先缓冲。
	if r, err := b.Receive(msg(1, []int{1, 1, 0}, "B1")); err != nil || r.Outcome != OutcomeBuffered {
		t.Fatalf("B1 buffer: %+v %v", r, err)
	}
	if r, err := b.Receive(msg(2, []int{1, 0, 1}, "C1")); err != nil || r.Outcome != OutcomeBuffered {
		t.Fatalf("C1 buffer: %+v %v", r, err)
	}

	// A1 到达后同一轮 B1(sender=1) 与 C1(sender=2) 同时可交付，
	// 必须按发送方最小者：A1, B1, C1。
	r, err := b.Receive(msg(0, []int{1, 0, 0}, "A1"))
	if err != nil {
		t.Fatalf("A1: %v", err)
	}
	got := payloads(r.Delivered)
	want := []any{"A1", "B1", "C1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cascade order = %v, want %v", got, want)
	}
}

// 因果阻塞：消息携带的因果前驱未交付时不能仅因自身序号相接而交付。
func TestCausalPredecessorBlocks(t *testing.T) {
	b, _ := New(2, 10)

	// B1=[1,1,0] 要求 A1 已交付，但 A1 尚未到达 → 必须缓冲，不能提前交付。
	r, err := b.Receive(msg(1, []int{1, 1}, "B1-needs-A1"))
	if err != nil || r.Outcome != OutcomeBuffered {
		t.Fatalf("B1 should block on A1: result=%+v err=%v", r, err)
	}
	if len(b.Delivered()) != 0 {
		t.Fatalf("nothing should be delivered yet, got %v", b.Delivered())
	}

	// A1 到达，级联交付 B1。
	r, _ = b.Receive(msg(0, []int{1, 0}, "A1"))
	if got := payloads(r.Delivered); !reflect.DeepEqual(got, []any{"A1", "B1-needs-A1"}) {
		t.Fatalf("cascade = %v", got)
	}
}

// 重复消息：已交付重复与缓冲中重复都丢弃并计数，且不是错误。
func TestDuplicates(t *testing.T) {
	b, _ := New(2, 10)

	a1 := msg(0, []int{1, 0}, "A1")
	if _, err := b.Receive(a1); err != nil {
		t.Fatalf("A1: %v", err)
	}

	// 已交付消息重复到达。
	r, err := b.Receive(a1)
	if err != nil {
		t.Fatalf("duplicate must not be an error, got %v", err)
	}
	if r.Outcome != OutcomeDuplicate || r.DupCount != 1 {
		t.Fatalf("dup A1: result=%+v", r)
	}

	// 同身份 (sender, seq) 但负载/向量细节不同，仍按重复处理。
	r, err = b.Receive(msg(0, []int{1, 0}, "A1-rewritten"))
	if err != nil || r.Outcome != OutcomeDuplicate || r.DupCount != 2 {
		t.Fatalf("same key different payload: result=%+v err=%v", r, err)
	}
	if len(b.Delivered()) != 1 {
		t.Fatalf("duplicate must not be appended, delivered=%v", b.Delivered())
	}

	// 缓冲中消息重复到达。
	a3 := msg(0, []int{3, 0}, "A3")
	if r, _ := b.Receive(a3); r.Outcome != OutcomeBuffered {
		t.Fatalf("A3 should buffer: %+v", r)
	}
	r, _ = b.Receive(a3)
	if r.Outcome != OutcomeDuplicate || r.DupCount != 3 {
		t.Fatalf("buffered dup: %+v", r)
	}
	if b.BufferedCount() != 1 {
		t.Fatalf("buffered dup must not add another entry, count=%d", b.BufferedCount())
	}

	// 缺口补齐后 A3 级联交付；此前的重复不影响正常交付。
	if _, err := b.Receive(msg(0, []int{2, 0}, "A2")); err != nil {
		t.Fatalf("A2: %v", err)
	}
	if got := payloads(b.Delivered()); !reflect.DeepEqual(got, []any{"A1", "A2", "A3"}) {
		t.Fatalf("final order = %v", got)
	}
}

// 缓冲已满：暂不能交付的消息被拒绝，且拒绝不改变任何状态。
func TestBufferFull(t *testing.T) {
	b, _ := New(2, 1)

	// A2 占满唯一缓冲位。
	if r, _ := b.Receive(msg(0, []int{2, 0}, "A2")); r.Outcome != OutcomeBuffered {
		t.Fatalf("A2: %+v", r)
	}

	snap := b.snapshotState()
	_, err := b.Receive(msg(1, []int{0, 2}, "B2"))
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("want ErrBufferFull, got %v", err)
	}
	if got := b.snapshotState(); !reflect.DeepEqual(got, snap) {
		t.Fatalf("state changed after buffer-full rejection:\n before=%+v\n after =%+v", snap, got)
	}

	// 已满足交付条件的消息不受容量限制，照常交付并级联腾出缓冲。
	r, err := b.Receive(msg(0, []int{1, 0}, "A1"))
	if err != nil {
		t.Fatalf("A1: %v", err)
	}
	if got := payloads(r.Delivered); !reflect.DeepEqual(got, []any{"A1", "A2"}) {
		t.Fatalf("cascade = %v", got)
	}
}

// 各类非法输入：构造参数、发送方越界、向量非法，错误原因必须可区分，状态不变。
func TestInvalidInputs(t *testing.T) {
	if _, err := New(0, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(0,5) err=%v", err)
	}
	if _, err := New(3, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(3,0) err=%v", err)
	}
	if _, err := New(-1, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(-1,-1) err=%v", err)
	}

	b, _ := New(2, 5)
	snap := b.snapshotState()

	cases := []struct {
		name string
		m    Message
		want error
	}{
		{"sender negative", Message{Sender: -1, Vector: []int{0, 0}}, ErrSenderOutOfRange},
		{"sender equals peers", Message{Sender: 2, Vector: []int{0, 0}}, ErrSenderOutOfRange},
		{"vector nil", Message{Sender: 0, Vector: nil}, ErrInvalidVector},
		{"vector too short", Message{Sender: 0, Vector: []int{1}}, ErrInvalidVector},
		{"vector too long", Message{Sender: 0, Vector: []int{1, 0, 0}}, ErrInvalidVector},
		{"vector negative", Message{Sender: 0, Vector: []int{1, -1}}, ErrInvalidVector},
		{"own component zero", Message{Sender: 0, Vector: []int{0, 0}}, ErrInvalidVector},
		{"own component negative", Message{Sender: 1, Vector: []int{0, -1}}, ErrInvalidVector},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := b.Receive(tc.m)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(r, Result{}) {
				t.Fatalf("rejected call returned non-zero result: %+v", r)
			}
			if got := b.snapshotState(); !reflect.DeepEqual(got, snap) {
				t.Fatalf("state changed after rejection:\n before=%+v\n after =%+v", snap, got)
			}
		})
	}

	// 发送方越界优先于向量内容校验。
	_, err := b.Receive(Message{Sender: 9, Vector: []int{-7}})
	if !errors.Is(err, ErrSenderOutOfRange) {
		t.Fatalf("sender check must come first, got %v", err)
	}
}

// 并发：因果闭合的消息集合乱序并发到达，最终全部恰好交付一次，且交付序是合法因果线性化。
func TestConcurrentClosedSet(t *testing.T) {
	// 闭合集合：每条消息向量中的每个序号都能在集合内找到对应消息。
	all := []Message{
		msg(0, []int{1, 0, 0}, "A1"),
		msg(1, []int{1, 1, 0}, "B1"),
		msg(2, []int{1, 1, 1}, "C1"),
		msg(0, []int{2, 1, 1}, "A2"),
		msg(1, []int{2, 2, 1}, "B2"),
		msg(2, []int{2, 2, 2}, "C2"),
	}

	for iter := 0; iter < 50; iter++ {
		b, _ := New(3, len(all)+2)
		shuffled := shuffleMessages(all, int64(iter))

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, len(shuffled))
		for _, m := range shuffled {
			wg.Add(1)
			go func(m Message) {
				defer wg.Done()
				<-start
				if _, err := b.Receive(m); err != nil {
					errs <- err
				}
			}(m)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("iter %d: concurrent Receive: %v", iter, err)
		}

		delivered := b.Delivered()
		if len(delivered) != len(all) {
			t.Fatalf("iter %d: delivered %d, want %d", iter, len(delivered), len(all))
		}
		if b.BufferedCount() != 0 {
			t.Fatalf("iter %d: pending not empty: %d", iter, b.BufferedCount())
		}
		if b.DupCount() != 0 {
			t.Fatalf("iter %d: unexpected duplicates: %d", iter, b.DupCount())
		}
		assertUniqueKeys(t, delivered)
		assertCausalOrder(t, delivered)
	}
}

// 并发重复：同一条消息被多个 goroutine 同时投递，恰好交付一次，其余全部计为重复。
func TestConcurrentDuplicate(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		b, _ := New(2, 5)
		const n = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]Result, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], _ = b.Receive(msg(0, []int{1, 0}, "A1"))
			}(i)
		}
		close(start)
		wg.Wait()

		delivered, dups := 0, 0
		for _, r := range results {
			switch r.Outcome {
			case OutcomeDelivered:
				delivered++
			case OutcomeDuplicate:
				dups++
			default:
				t.Fatalf("unexpected outcome %v", r.Outcome)
			}
		}
		if delivered != 1 || dups != n-1 {
			t.Fatalf("iter %d: delivered=%d dups=%d", iter, delivered, dups)
		}
		if len(b.Delivered()) != 1 || b.DupCount() != int64(n-1) {
			t.Fatalf("iter %d: delivered=%v dupCount=%d", iter, b.Delivered(), b.DupCount())
		}
	}
}

// 确定性：同一输入序列在全新缓冲上反复计算，逐次结果与最终交付序列完全相同。
func TestDeterministicReplay(t *testing.T) {
	sequence := []Message{
		msg(2, []int{1, 1, 1}, "C1"),
		msg(0, []int{2, 1, 0}, "A2"),
		msg(1, []int{1, 1, 0}, "B1"),
		msg(2, []int{1, 1, 1}, "C1-dup"),
		msg(0, []int{1, 0, 0}, "A1"),
		msg(0, []int{2, 1, 0}, "A2-dup"),
	}

	run := func() ([]Result, []Message) {
		b, _ := New(3, 10)
		rs := make([]Result, len(sequence))
		for i, m := range sequence {
			r, err := b.Receive(m)
			if err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			rs[i] = r
		}
		return rs, b.Delivered()
	}

	wantResults, wantDelivered := run()
	for iter := 0; iter < 5; iter++ {
		gotResults, gotDelivered := run()
		// Result 含切片，逐个比较。
		if len(gotResults) != len(wantResults) {
			t.Fatalf("result length mismatch")
		}
		for i := range wantResults {
			if gotResults[i].Outcome != wantResults[i].Outcome ||
				gotResults[i].DupCount != wantResults[i].DupCount ||
				!reflect.DeepEqual(payloads(gotResults[i].Delivered), payloads(wantResults[i].Delivered)) {
				t.Fatalf("iter %d step %d result differs:\n got=%+v\nwant=%+v",
					iter, i, gotResults[i], wantResults[i])
			}
		}
		if !reflect.DeepEqual(payloads(gotDelivered), payloads(wantDelivered)) {
			t.Fatalf("iter %d final delivery differs:\n got=%v\nwant=%v",
				iter, payloads(gotDelivered), payloads(wantDelivered))
		}
	}
}

// 日志：配置日志器后打印输入、判定依据与交付结果。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	b, err := New(2, 1, withTestLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := b.Receive(msg(0, []int{2, 0}, "A2")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(msg(0, []int{1, 0}, "A1")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(msg(0, []int{1, 0}, "A1-again")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(Message{Sender: 5, Vector: []int{1}}); err == nil {
		t.Fatal("expected rejection")
	}

	log := buf.String()
	for _, want := range []string{
		"RECV buffered sender=0 seq=2",  // 输入 + 判定
		"waiting own gap",               // 判定依据
		"RECV delivered sender=0 seq=1", // 交付结果
		"cascade delivered",             // 级联交付
		"RECV duplicate sender=0 seq=1", // 重复判定
		"sender out of range",           // 拒绝原因
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\nfull log:\n%s", want, log)
		}
	}
	t.Logf("captured log:\n%s", log)
}

// Vector 返回的是快照副本，外部修改不影响内部状态。
func TestVectorSnapshot(t *testing.T) {
	b, _ := New(2, 5)
	if _, err := b.Receive(msg(0, []int{1, 0}, "A1")); err != nil {
		t.Fatal(err)
	}
	v := b.Vector()
	v[0] = 999
	if got := b.Vector(); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("internal vector mutated through snapshot: %v", got)
	}
}

// ---- helpers ----

type testState struct {
	vc        []int
	pending   int
	delivered int
	dupCount  int64
}

func (b *Buffer) snapshotState() testState {
	b.mu.Lock()
	defer b.mu.Unlock()
	vc := append([]int(nil), b.vc...)
	return testState{vc: vc, pending: len(b.pending), delivered: len(b.delivered), dupCount: b.dupCount}
}

func payloads(ms []Message) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m.Payload
	}
	return out
}

func shuffleMessages(in []Message, seed int64) []Message {
	out := append([]Message(nil), in...)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func assertUniqueKeys(t *testing.T, ms []Message) {
	t.Helper()
	seen := make(map[bufKey]bool, len(ms))
	for _, m := range ms {
		k := keyOf(m)
		if seen[k] {
			t.Fatalf("message %v delivered more than once", k)
		}
		seen[k] = true
	}
}

// happensBefore 报告向量时钟 a 是否严格先于 b。
func happensBefore(a, b []int) bool {
	strict := false
	for i := range a {
		if a[i] > b[i] {
			return false
		}
		if a[i] < b[i] {
			strict = true
		}
	}
	return strict
}

// assertCausalOrder 验证交付序列中不存在逆序的 happens-before 对。
func assertCausalOrder(t *testing.T, ms []Message) {
	t.Helper()
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			if happensBefore(ms[j].Vector, ms[i].Vector) {
				t.Fatalf("causal violation at positions %d,%d: %v delivered before %v",
					i, j, ms[j].Payload, ms[i].Payload)
			}
		}
	}
}

type testLogger struct{ w *bytes.Buffer }

func (l testLogger) Printf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}

func withTestLogger(buf *bytes.Buffer) Option {
	return WithLogger(testLogger{w: buf})
}
