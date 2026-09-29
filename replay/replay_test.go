package replay

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logStep 打印一次操作后的操作名、时钟、令牌、吐出序列与判定依据，
// 便于本地按逐步重放的方式核对结果。
func logStep(t *testing.T, op string, s State, emitted []int, reason string) {
	t.Helper()
	t.Logf("%-16s now=%d tokens=%d queued=%d in=%d out=%d emitted=%v | %s",
		op, s.Now, s.Tokens, s.QueueLen, s.TotalEnqueued, s.TotalDrained, emitted, reason)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero base rate", Config{BaseRate: 0, CatchUpRate: 3, Capacity: 5, MaxQueue: 10}},
		{"negative catchup", Config{BaseRate: 1, CatchUpRate: -1, Capacity: 5, MaxQueue: 10}},
		{"zero capacity", Config{BaseRate: 1, CatchUpRate: 3, Capacity: 0, MaxQueue: 10}},
		{"zero max queue", Config{BaseRate: 1, CatchUpRate: 3, Capacity: 5, MaxQueue: 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := New[int](c.cfg)
			t.Logf("%-16s cfg=%+v | %s", "New", c.cfg, err.Error())
			if r != nil || !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got r=%v err=%v", r, err)
			}
		})
	}

	r, err := New[int](Config{BaseRate: 1, CatchUpRate: 3, Capacity: 5, MaxQueue: 10})
	if err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	logStep(t, "New(ok)", s, nil, "bucket starts full at Capacity")
	if s.Tokens != 5 || s.Now != 0 {
		t.Fatalf("initial state wrong: %+v", s)
	}
}

// 追赶场景：队列有积压时按追赶速率（3/时间单位）补充令牌，且被桶容量封顶。
func TestCatchUpRefillCapped(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 3, Capacity: 5, MaxQueue: 20})

	// t=0 起桶满（5），入队 8 个事件并立即吐出 5 个，令牌耗尽、队列剩 3。
	for i := 1; i <= 8; i++ {
		if err := r.Enqueue(0, i); err != nil {
			t.Fatal(err)
		}
	}
	out, err := r.Drain(0)
	if err != nil || fmt.Sprint(out) != "[1 2 3 4 5]" {
		t.Fatalf("initial drain wrong: %v %v", out, err)
	}
	logStep(t, "Drain@0", r.Snapshot(), out, "full bucket bursts 5; backlog 3 -> catchup rate next")

	// 队列仍有积压，推进 1 个时间单位补 3 个令牌（追赶速率），吐出 3 个。
	out, err = r.Drain(1)
	if err != nil || fmt.Sprint(out) != "[6 7 8]" {
		t.Fatalf("catchup drain wrong: %v %v", out, err)
	}
	s := r.Snapshot()
	logStep(t, "Drain@1", s, out, "backlog => +3 tokens at CatchUpRate=3; queue empty, tokens=0")
	if s.Tokens != 0 || s.QueueLen != 0 {
		t.Fatalf("state after catchup drain wrong: %+v", s)
	}

	// 队列已空后再入队 8 个；推进 1 个时间单位时队列有积压，仍补 3 令牌。
	for i := 9; i <= 16; i++ {
		if err := r.Enqueue(1, i); err != nil {
			t.Fatal(err)
		}
	}
	out, err = r.Drain(2)
	if err != nil || fmt.Sprint(out) != "[9 10 11]" {
		t.Fatalf("catchup refill wrong: %v %v", out, err)
	}
	logStep(t, "Drain@2", r.Snapshot(), out, "backlog before advance => +3 tokens at CatchUpRate=3")

	// 同一时刻重复吐出：时间未推进，不补令牌。
	out, err = r.Drain(2)
	if err != nil || len(out) != 0 {
		t.Fatalf("same-time drain must emit nothing: %v %v", out, err)
	}
	logStep(t, "Drain@2", r.Snapshot(), out, "zero elapsed time => no refill")

	// 追赶补充受桶容量封顶：即使推进 100 个时间单位，最多也只补到 5 个令牌。
	out, err = r.Drain(102)
	if err != nil || fmt.Sprint(out) != "[12 13 14 15 16]" {
		t.Fatalf("cap refill wrong: %v %v", out, err)
	}
	s = r.Snapshot()
	logStep(t, "Drain@102", s, out, "refill capped at Capacity=5 despite large elapsed time")
	if s.Tokens != 0 || s.QueueLen != 0 {
		t.Fatalf("state after capped drain wrong: %+v", s)
	}
	if err := r.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 突发场景：桶容量把大批量事件拆平——首批只吐出 Capacity 个，之后按追赶速率平滑吐出。
func TestBurstFlattenedByCapacity(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 2, Capacity: 4, MaxQueue: 20})
	for i := 1; i <= 10; i++ {
		if err := r.Enqueue(0, i); err != nil {
			t.Fatal(err)
		}
	}

	want := []struct {
		at      int64
		emitted string
	}{
		{0, "[1 2 3 4]"}, // 初始桶容量 = 4，突发最多 4
		{1, "[5 6]"},     // 积压，追赶速率 2
		{2, "[7 8]"},
		{3, "[9 10]"},
	}
	for _, w := range want {
		out, err := r.Drain(w.at)
		if err != nil || fmt.Sprint(out) != w.emitted {
			t.Fatalf("Drain@%d = %v (err=%v), want %s", w.at, out, err, w.emitted)
		}
		logStep(t, fmt.Sprintf("Drain@%d", w.at), r.Snapshot(), out,
			"burst flattened: first batch limited by capacity, later by catchup rate")
	}
	if s := r.Snapshot(); s.Tokens != 0 || s.QueueLen != 0 {
		t.Fatalf("final state wrong: %+v", s)
	}
}

// 队列空时按基准速率（而非追赶速率）补充。
func TestBaseRateWhenQueueIdle(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 5, Capacity: 5, MaxQueue: 20})

	for i := 1; i <= 5; i++ {
		if err := r.Enqueue(0, i); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := r.Drain(0)
	logStep(t, "Drain@0", r.Snapshot(), out, "queue drained empty, tokens=0")

	// 队列空闲期间推进 3 个时间单位，再入队：应只按 BaseRate 补 3（不是 CatchUpRate 的 15）。
	for i := 6; i <= 10; i++ {
		if err := r.Enqueue(3, i); err != nil {
			t.Fatal(err)
		}
	}
	out, err := r.Drain(3)
	if err != nil || fmt.Sprint(out) != "[6 7 8]" {
		t.Fatalf("idle refill should use base rate: %v %v", out, err)
	}
	logStep(t, "Drain@3", r.Snapshot(), out, "idle before enqueue => +3 at BaseRate=1; 2 events remain")

	out, err = r.Drain(5)
	if err != nil || fmt.Sprint(out) != "[9 10]" {
		t.Fatalf("backlog refill wrong: %v %v", out, err)
	}
	logStep(t, "Drain@5", r.Snapshot(), out,
		"backlog => refill up to Capacity=5 at CatchUpRate, spend 2; 3 tokens remain")
}

// 队列超限：整体拒绝且不改变任何状态（包括时钟）。
func TestQueueFullRejectedAtomically(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 2, Capacity: 2, MaxQueue: 3})
	for i := 1; i <= 3; i++ {
		if err := r.Enqueue(0, i); err != nil {
			t.Fatal(err)
		}
	}
	before := r.Snapshot()

	err := r.Enqueue(5, 99) // 试图同时推进时钟并入队
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
	after := r.Snapshot()
	logStep(t, "Enqueue@5(full)", after, nil, "rejected: queue at MaxQueue; clock and tokens unchanged")
	if after != before {
		t.Fatalf("failed enqueue mutated state:\nbefore=%+v\nafter =%+v", before, after)
	}

	out, _ := r.Drain(0)
	logStep(t, "Drain@0", r.Snapshot(), out, "free 2 slots")
	if err := r.Enqueue(1, 4); err != nil {
		t.Fatalf("enqueue after drain should succeed: %v", err)
	}
	s := r.Snapshot()
	logStep(t, "Enqueue@1", s, nil, "accepted; advances to t=1 and refills while backlog is present")
	if s.Now != 1 {
		t.Fatalf("clock should advance on successful enqueue: %+v", s)
	}
}

// 时钟回退：整体拒绝，状态不变。
func TestClockBackwardsRejected(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 2, Capacity: 5, MaxQueue: 10})
	if err := r.Enqueue(10, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Drain(10); err != nil {
		t.Fatal(err)
	}
	before := r.Snapshot()

	if err := r.Enqueue(9, 2); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("enqueue backwards: %v", err)
	}
	if _, err := r.Drain(9); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("drain backwards: %v", err)
	}
	after := r.Snapshot()
	logStep(t, "ops@9(backwards)", after, nil, "both rejected; state identical to before the failures")
	if after != before {
		t.Fatalf("backwards call mutated state:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// FIFO 保序：任意速率下吐出顺序必须与入队顺序一致。
func TestFIFOOrderingAcrossTicks(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 3, Capacity: 4, MaxQueue: 100})
	const n = 20
	for i := 1; i <= n; i++ {
		if err := r.Enqueue(0, i); err != nil {
			t.Fatal(err)
		}
	}
	var got []int
	for tick := int64(0); len(got) < n; tick++ {
		out, err := r.Drain(tick)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, out...)
		if len(out) > 0 {
			logStep(t, fmt.Sprintf("Drain@%d", tick), r.Snapshot(), out, "emitted order must match enqueue order")
		}
	}
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("order broken at %d: got %v", i, got)
		}
	}
}

// 并发自检：多个 goroutine 同时只读 Snapshot/SelfCheck。没有写者时所有读者
// 拿到的快照必须逐字段相同；每张快照都满足 累计入队 == 累计吐出 + 队列长度。
func TestConcurrentReadsConsistent(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 2, CatchUpRate: 4, Capacity: 6, MaxQueue: 100})
	for i := 0; i < 50; i++ {
		if err := r.Enqueue(int64(i), i); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			if _, err := r.Drain(int64(i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	baseline := r.Snapshot()

	const readers = 16
	const rounds = 1000
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	wg.Add(readers)
	for g := 0; g < readers; g++ {
		go func() {
			defer wg.Done()
			for k := 0; k < rounds; k++ {
				s := r.Snapshot()
				if s != baseline {
					setErr(fmt.Errorf("concurrent readers disagree: got %+v want %+v", s, baseline))
					return
				}
				if s.TotalEnqueued != s.TotalDrained+int64(s.QueueLen) {
					setErr(fmt.Errorf("invariant broken: %+v", s))
					return
				}
				if s.Tokens < 0 || s.Tokens > 6 {
					setErr(fmt.Errorf("tokens out of range: %+v", s))
					return
				}
				if err := r.SelfCheck(); err != nil {
					setErr(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	logStep(t, "ConcurrentSnapshot", baseline, nil,
		"16 goroutines x 1000 reads: identical field-by-field snapshots; in==out+queued for every read")
}

// StepReplay 是文档中「逐步重放三元组」核对方法的可执行版本：
// 每个三元组为 (操作, 逻辑时间, 入队事件, 期望吐出序列, 期望判定依据)，
// 逐步执行并把 (时钟, 令牌, 吐出序列) 与预算结果比对，保证回放确定可复现。
type stepReplay struct {
	op       string // "enqueue" 或 "drain"
	now      int64
	event    int   // 仅 enqueue 使用
	wantEmit []int // 操作后本步吐出的序列（enqueue 为空）
	wantTok  int64 // 操作后期望令牌数
	reason   string
}

func TestStepwiseReplayTriplets(t *testing.T) {
	r, _ := New[int](Config{BaseRate: 1, CatchUpRate: 3, Capacity: 4, MaxQueue: 10})
	steps := []stepReplay{
		{"enqueue", 0, 1, nil, 4, "initial full bucket"},
		{"enqueue", 0, 2, nil, 4, "no elapsed time, tokens unchanged"},
		{"enqueue", 0, 3, nil, 4, "FIFO enqueue"},
		{"enqueue", 0, 4, nil, 4, "FIFO enqueue"},
		{"enqueue", 0, 5, nil, 4, "FIFO enqueue"},
		{"enqueue", 0, 6, nil, 4, "FIFO enqueue"},
		{"drain", 0, 0, []int{1, 2, 3, 4}, 0, "burst limited by Capacity=4; 2 backlogged"},
		{"drain", 1, 0, []int{5, 6}, 1, "backlog => +3 at CatchUpRate, spend 2, 1 saved"},
		{"enqueue", 2, 7, nil, 2, "advance with empty queue: 1 saved +1 at BaseRate"},
		{"enqueue", 2, 8, nil, 2, "no elapsed time, tokens unchanged"},
		{"drain", 2, 0, []int{7, 8}, 0, "2 tokens spend on 2 backlogged events"},
		{"drain", 3, 0, nil, 1, "empty queue => +1 at BaseRate, nothing to emit"},
	}

	for i, st := range steps {
		var got []int
		switch st.op {
		case "enqueue":
			if err := r.Enqueue(st.now, st.event); err != nil {
				t.Fatalf("step %d enqueue: %v", i, err)
			}
		case "drain":
			out, err := r.Drain(st.now)
			if err != nil {
				t.Fatalf("step %d drain: %v", i, err)
			}
			got = out
		default:
			t.Fatalf("unknown op: %s", st.op)
		}
		s := r.Snapshot()
		logStep(t, fmt.Sprintf("%s@%d#%d", st.op, st.now, i), s, got, st.reason)
		if fmt.Sprint(got) != fmt.Sprint(st.wantEmit) {
			t.Fatalf("step %d emitted=%v want=%v", i, got, st.wantEmit)
		}
		if s.Tokens != st.wantTok {
			t.Fatalf("step %d tokens=%d want=%d", i, s.Tokens, st.wantTok)
		}
		if err := r.SelfCheck(); err != nil {
			t.Fatalf("step %d self-check: %v", i, err)
		}
	}
	final := r.Snapshot()
	if final.QueueLen != 0 || final.TotalEnqueued != final.TotalDrained {
		t.Fatalf("replay not fully drained: %+v", final)
	}
	logStep(t, "Final", final, nil, "in==out, queue empty; deterministic replay verified step by step")
}
