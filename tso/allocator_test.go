package tso

import (
	"bytes"
	"errors"
	"log"
	"sync"
	"testing"
)

// scriptClock 按调用次序返回预设毫秒；未预设时返回最后一个值。
func scriptClock(values ...int64) Clock {
	i := 0
	return func() int64 {
		var v int64
		if i < len(values) {
			v = values[i]
		} else {
			v = values[len(values)-1]
		}
		i++
		return v
	}
}

func testLogger(buf *bytes.Buffer) *log.Logger {
	return log.New(buf, "[test-tso] ", log.LstdFlags|log.Lmicroseconds)
}

func mustAllocate(t *testing.T, a *Allocator, n int64) []Timestamp {
	t.Helper()
	ts, err := a.Allocate(n)
	if err != nil {
		t.Fatalf("Allocate(%d) 意外失败: %v", n, err)
	}
	return ts
}

func assertIncreasingUnique(t *testing.T, all []Timestamp) {
	t.Helper()
	seen := map[Timestamp]bool{}
	for i, ts := range all {
		if seen[ts] {
			t.Fatalf("时间戳重复: %s", ts)
		}
		seen[ts] = true
		if i > 0 && all[i-1].Cmp(ts) >= 0 {
			t.Fatalf("顺序不严格递增: %s !< %s", all[i-1], ts)
		}
	}
}

// 场景 1：新主时钟落后旧主 5 秒，首个时间戳仍大于旧主已发全部值。
func TestTakeoverClockBehindFiveSeconds(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	oldClock := scriptClock(100_000, 100_010, 100_020)
	old, err := New(store, Options{L: 4, W: 10}, oldClock, nil, testLogger(buf))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	oldIssued := mustAllocate(t, old, 2)
	oldIssued = append(oldIssued, mustAllocate(t, old, 2)...)
	oldMax := oldIssued[len(oldIssued)-1]

	newClock := scriptClock(95_000, 95_000)
	newLeader, err := New(store, Options{L: 4, W: 10}, newClock, nil, testLogger(buf))
	if err != nil {
		t.Fatal(err)
	}
	if err := newLeader.TakeOver(2); err != nil {
		t.Fatal(err)
	}
	first := mustAllocate(t, newLeader, 1)[0]
	if first.Cmp(oldMax) <= 0 {
		t.Fatalf("新主首个时间戳 %s 未大于旧主最大 %s", first, oldMax)
	}
	if first.Physical != 100_030 {
		t.Fatalf("期望起点 phys=100030(=storedHigh), 实际 %d", first.Physical)
	}
	t.Logf("旧主最大=%s, 新主首个=%s, 判定: 新主 phys=max(clock,high)=110010 > 旧主全部\n%s",
		oldMax, first, buf.String())
}

// 场景 2：逻辑计数用尽跨毫秒。
func TestLogicalExhaustionCrossesMillisecond(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	clock := scriptClock(1000, 1000, 1000)
	a, _ := New(store, Options{L: 4, W: 100}, clock, nil, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	batch1 := mustAllocate(t, a, 3)
	batch2 := mustAllocate(t, a, 3)
	if batch1[0] != (Timestamp{1000, 0}) || batch1[2] != (Timestamp{1000, 2}) {
		t.Fatalf("batch1 错误: %v", batch1)
	}
	if batch2[0] != (Timestamp{1001, 0}) || batch2[2] != (Timestamp{1001, 2}) {
		t.Fatalf("batch2 应跨毫秒 (1001,0..2), 实际 %v", batch2)
	}
	t.Logf("逻辑用尽跨毫秒: %v -> %v, 依据: L-logical<n 则 phys++ 并清零\n%s",
		batch1, batch2, buf.String())
}

// 场景 3：批量整体移到下一毫秒。
func TestBatchMovesWholeToNextMillisecond(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	clock := scriptClock(2000, 2000, 2000)
	a, _ := New(store, Options{L: 4, W: 100}, clock, nil, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	_ = mustAllocate(t, a, 3)
	batch := mustAllocate(t, a, 2)
	if batch[0].Physical != 2001 || batch[0].Logical != 0 || batch[1].Logical != 1 {
		t.Fatalf("批量应整体落在 2001, 实际 %v", batch)
	}
	t.Logf("批量整体移动: %v, 依据: 同一毫秒连续, 剩余不足整体 phys++\n%s", batch, buf.String())
}

// 场景 4a：续写注入故障但未越界 -> 照常发放。
func TestExtendFaultNonOverflowSucceeds(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	clock := scriptClock(3000, 3009)
	fault := ExtendFault(func(term, high int64) bool { return true })
	a, _ := New(store, Options{L: 8, W: 10}, clock, fault, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	before := store.Get()
	batch, err := a.Allocate(2)
	if err != nil {
		t.Fatalf("续写故障但未越界应照常发放, got %v", err)
	}
	if batch[0] != (Timestamp{3009, 0}) {
		t.Fatalf("期望 (3009,0), 实际 %s", batch[0])
	}
	if after := store.Get(); before != after {
		t.Fatalf("失败的持久化不得改变存储: before=%s after=%s", before, after)
	}
	t.Logf("续写故障不越界照常发放: %v, 存储保持 %s\n%s", batch, before, buf.String())
}

// 场景 4b：续写注入故障且越界 -> 本次失败，不消耗、不改存储。
func TestExtendFaultOverflowFails(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	clock := scriptClock(4000, 4010)
	fault := ExtendFault(func(term, high int64) bool { return true })
	a, _ := New(store, Options{L: 8, W: 10}, clock, fault, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	before := store.Get()
	if _, err := a.Allocate(1); !errors.Is(err, ErrExceedsBound) {
		t.Fatalf("期望 ErrExceedsBound, got %v", err)
	}
	if after := store.Get(); before != after {
		t.Fatalf("失败请求不得改变存储: before=%s after=%s", before, after)
	}
	a.fault = nil
	ts := mustAllocate(t, a, 1)
	if ts[0] != (Timestamp{4010, 0}) {
		t.Fatalf("故障恢复后期望 (4010,0), 实际 %s", ts[0])
	}
	t.Logf("续写故障越界失败且不消耗, 恢复后首签=%s\n%s", ts[0], buf.String())
}

// 场景 4c：续写时任期已被超过 -> 立即降为从节点。
func TestExtendSupersededStepsDown(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	clock := scriptClock(5000, 5009)
	a, _ := New(store, Options{L: 8, W: 10}, clock, nil, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	other, _ := New(store, Options{L: 8, W: 10}, scriptClock(1), nil,
		testLogger(&bytes.Buffer{}))
	if err := other.TakeOver(2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate(1); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("期望降为从节点 ErrNotLeader, got %v", err)
	}
	if a.IsLeader() {
		t.Fatal("旧主必须已降为从节点")
	}
	if a.Term() != 2 {
		t.Fatalf("旧主应获知新任期 2, 实际 %d", a.Term())
	}
	t.Logf("续写被超任期拒绝后立即降级, 获知任期=%d\n%s", a.Term(), buf.String())
}

// 场景 5：时钟回拨不产生更小的物理部分。
func TestClockRollback(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	clock := scriptClock(6000, 5000)
	a, _ := New(store, Options{L: 4, W: 100}, clock, nil, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	first := mustAllocate(t, a, 1)
	second := mustAllocate(t, a, 1)
	if first[0].Physical != 6000 {
		t.Fatalf("首个 phys 期望 6000, 实际 %d", first[0].Physical)
	}
	if second[0] != (Timestamp{6000, 1}) {
		t.Fatalf("时钟回拨后应沿用 (6000,1), 实际 %v", second)
	}
	t.Logf("时钟回拨被吸收: %s -> %s, 依据: phys=max(clock,lastPhys)\n%s",
		first[0], second[0], buf.String())
}

// 场景 6：并发请求全局唯一且按发放顺序严格递增。
func TestConcurrentAllocateUniqueAndOrdered(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	var t0 int64 = 7_000_000
	clock := Clock(func() int64 { return t0 })
	a, _ := New(store, Options{L: 1000, W: 10_000_000}, clock, nil, testLogger(buf))
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const perG = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []Timestamp
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				ts := mustAllocate(t, a, 1)
				mu.Lock()
				all = append(all, ts...)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if int64(len(all)) != goroutines*perG {
		t.Fatalf("数量错误: %d", len(all))
	}
	seen := map[Timestamp]bool{}
	for _, ts := range all {
		if seen[ts] {
			t.Fatalf("时间戳重复: %s", ts)
		}
		seen[ts] = true
		if ts.Physical != t0 {
			t.Fatalf("固定时钟下不应跨毫秒: %s", ts)
		}
	}
	total := int64(goroutines * perG)
	if total > 1000 {
		t.Fatal("测试前提：总量不超过 L，才能落在同一毫秒")
	}
	for l := int64(0); l < total; l++ {
		if !seen[Timestamp{t0, l}] {
			t.Fatalf("缺少时间戳 (%d,%d)：发放序列不连续", t0, l)
		}
	}
	t.Logf("并发 %d x %d 全部唯一且恰好覆盖 (%d,0..%d)；单主锁内发放天然严格递增",
		goroutines, perG, t0, total-1)
}

// 参数与状态类拒绝，原因可区分。
func TestRejectionReasons(t *testing.T) {
	store := NewMemoryStore()
	buf := &bytes.Buffer{}
	logger := testLogger(buf)

	if _, err := New(store, Options{L: 0, W: 10}, nil, nil, logger); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("L 非正应 ErrInvalidConfig, got %v", err)
	}
	if _, err := New(store, Options{L: 4, W: -1}, nil, nil, logger); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("W 非正应 ErrInvalidConfig, got %v", err)
	}

	a, _ := New(store, Options{L: 4, W: 10}, scriptClock(8000), nil, logger)
	if _, err := a.Allocate(1); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("从节点请求应 ErrNotLeader, got %v", err)
	}
	if err := a.TakeOver(1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate(0); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("n=0 应 ErrInvalidCount, got %v", err)
	}
	if _, err := a.Allocate(5); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("n>L 应 ErrInvalidCount, got %v", err)
	}
	before := store.Get()
	if _, err := a.Allocate(5); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("重复判定错误")
	}
	if after := store.Get(); before != after {
		t.Fatalf("被拒绝的请求不得改变存储")
	}
	// 以不大于存量的任期接任。
	if err := a.TakeOver(1); !errors.Is(err, ErrTermNotHigher) {
		t.Fatalf("同任期接任应 ErrTermNotHigher, got %v", err)
	}
	if err := a.TakeOver(0); !errors.Is(err, ErrTermNotHigher) {
		t.Fatalf("更小任期接任应 ErrTermNotHigher, got %v", err)
	}
	if !a.IsLeader() || a.Term() != 1 {
		t.Fatalf("失败的接任不得改变节点身份与任期")
	}
	// 更大任期接任成功。
	if err := a.TakeOver(2); err != nil {
		t.Fatalf("更大任期接任应成功, got %v", err)
	}
	t.Logf("拒绝原因均可区分且无副作用:\n%s", buf.String())
}

// 相同的时钟、故障注入与请求序列重放结果相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() []Timestamp {
		store := NewMemoryStore()
		buf := &bytes.Buffer{}
		clock := scriptClock(9000, 9000, 9009, 9009)
		calls := 0
		fault := ExtendFault(func(term, high int64) bool {
			calls++
			return calls == 2 // 仅第二次续写（第二个 Allocate）失败
		})
		a, _ := New(store, Options{L: 8, W: 10}, clock, fault, testLogger(buf))
		if err := a.TakeOver(1); err != nil {
			t.Fatal(err)
		}
		r1 := mustAllocate(t, a, 1)
		r2 := mustAllocate(t, a, 1)
		return append(r1, r2...)
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatal("重放长度不一致")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放不确定: %v != %v", first, second)
		}
	}
	t.Logf("重放结果一致: %v", first)
}
