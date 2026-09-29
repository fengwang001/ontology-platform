package tso

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
)

type scriptClock struct{ t int64 }

func (c *scriptClock) get() int64 { return c.t }

func newTestAllocator(t *testing.T, L, W int64, clock func() int64, log *bytes.Buffer) (*Allocator, *MemStore) {
	t.Helper()
	store := NewMemStore()
	opts := []Option{WithClock(clock)}
	if log != nil {
		opts = append(opts, WithLogger(log))
	}
	a, err := New(L, W, store, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, store
}

func mustAllocate(t *testing.T, a *Allocator, n int64) []Timestamp {
	t.Helper()
	ts, err := a.Allocate(context.Background(), n)
	if err != nil {
		t.Fatalf("Allocate(%d): %v", n, err)
	}
	return ts
}

func TestNewLeaderClockBehindFiveSeconds(t *testing.T) {
	var log bytes.Buffer
	oldClock := &scriptClock{t: 1_000_000}
	old, store := newTestAllocator(t, 1000, 8, oldClock.get, &log)
	ctx := context.Background()
	if err := old.TakeOver(ctx, 1, oldClock.get()); err != nil {
		t.Fatalf("old TakeOver: %v", err)
	}
	var lastOld Timestamp
	for i := 0; i < 10; i++ {
		batch := mustAllocate(t, old, 1000)
		lastOld = batch[len(batch)-1]
	}
	stored := store.Current()
	t.Logf("旧主最后发出: %s, 存储: term=%d upper=%d", lastOld, stored.Term, stored.Upper)

	newClock := &scriptClock{t: oldClock.t - 5000}
	var newLog bytes.Buffer
	fresh, err := New(1000, 8, store, WithClock(newClock.get), WithLogger(&newLog))
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.TakeOver(ctx, 2, newClock.get()); err != nil {
		t.Fatalf("new TakeOver: %v", err)
	}
	first := mustAllocate(t, fresh, 1)[0]
	t.Logf("新主时钟=%d，首个时间戳=%s\n旧主日志:\n%s新主日志:\n%s",
		newClock.get(), first, log.String(), newLog.String())

	if !lastOld.Less(first) {
		t.Fatalf("新主首个 %s 不大于旧主最后 %s", first, lastOld)
	}
	if first.Physical < stored.Upper {
		t.Fatalf("新主首个物理部分 %d 应 >= 旧存量上界 %d", first.Physical, stored.Upper)
	}
}

func TestLogicalExhaustionCrossesMillisecond(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 5000}
	a, _ := newTestAllocator(t, 3, 10, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}
	b1 := mustAllocate(t, a, 3)
	want1 := []Timestamp{{5000, 0}, {5000, 1}, {5000, 2}}
	for i := range want1 {
		if b1[i] != want1[i] {
			t.Fatalf("第一批[%d]=%s，期望 %s", i, b1[i], want1[i])
		}
	}
	b2 := mustAllocate(t, a, 2)
	want2 := []Timestamp{{5001, 0}, {5001, 1}}
	for i := range want2 {
		if b2[i] != want2[i] {
			t.Fatalf("第二批[%d]=%s，期望 %s", i, b2[i], want2[i])
		}
	}
	t.Log(log.String())
}

func TestBatchMovesWhollyToNextMillisecond(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 7000}
	a, _ := newTestAllocator(t, 4, 20, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}
	first := mustAllocate(t, a, 3)
	if first[2] != (Timestamp{7000, 2}) {
		t.Fatalf("first = %v", first)
	}
	second := mustAllocate(t, a, 2)
	want := []Timestamp{{7001, 0}, {7001, 1}}
	for i := range want {
		if second[i] != want[i] {
			t.Fatalf("second[%d]=%s，期望 %s", i, second[i], want[i])
		}
	}
	t.Log(log.String())
}

func TestExtensionFailureWithinBoundStillServes(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 10_000}
	a, store := newTestAllocator(t, 100, 4, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}

	mustAllocate(t, a, 100)
	mustAllocate(t, a, 100)
	mustAllocate(t, a, 100)
	// 当前已发到 10002；下一批进入 10003（< 上界 10004），
	// 但剩余窗口 1ms 不足 W=4 的一半，按规则先续写。
	store.InjectFault(WriteFaultFail)
	stamp := mustAllocate(t, a, 1)
	if stamp[0].Physical != 10003 {
		t.Fatalf("续写失败但未越界时应照常发放于 10003，得到 %s", stamp[0])
	}
	if got := store.Current().Upper; got != 10004 {
		t.Fatalf("失败写入不得改变存储: upper=%d want 10004", got)
	}
	if !a.IsLeader() {
		t.Fatalf("普通持久化故障不应导致降级")
	}
	t.Log(log.String())
}

func TestExtensionFailureOutOfBoundFailsRequest(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 10_000}
	a, store := newTestAllocator(t, 100, 2, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}
	mustAllocate(t, a, 100) // 10000
	mustAllocate(t, a, 100) // 10001（上界 10002 的最后一个可用毫秒）

	store.InjectFault(WriteFaultFail)
	_, err := a.Allocate(ctx, 100) // 必须进入 10002
	if !errors.Is(err, ErrPersistUnavailable) {
		t.Fatalf("err = %v，期望 ErrPersistUnavailable", err)
	}
	if !a.IsLeader() {
		t.Fatalf("普通持久化故障不应导致降级")
	}
	next := mustAllocate(t, a, 100)
	if next[0] != (Timestamp{10002, 0}) {
		t.Fatalf("失败请求消耗了时间戳，下一批起点=%s", next[0])
	}
	t.Log(log.String())
}

func TestSupersededTermDemotesToFollower(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 20_000}
	a, store := newTestAllocator(t, 10, 2, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}
	mustAllocate(t, a, 10) // 20000
	mustAllocate(t, a, 10) // 20001
	store.InjectFault(WriteFaultSupersede)

	_, err := a.Allocate(ctx, 10)
	if !errors.Is(err, ErrTermSuperseded) {
		t.Fatalf("err = %v，期望 ErrTermSuperseded", err)
	}
	if a.IsLeader() {
		t.Fatalf("任期被超过后必须立即降为从节点")
	}
	if _, err := a.Allocate(ctx, 1); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("降级后请求应得 ErrNotLeader，got %v", err)
	}
	t.Log(log.String())
}

func TestClockMovesBackwards(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 30_000}
	a, _ := newTestAllocator(t, 1000, 20, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}
	b1 := mustAllocate(t, a, 2)
	clock.t = 29_000
	b2 := mustAllocate(t, a, 2)
	if !b1[len(b1)-1].Less(b2[0]) {
		t.Fatalf("回拨后未严格递增: %s -> %s", b1[len(b1)-1], b2[0])
	}
	if b2[0].Physical != b1[0].Physical {
		t.Fatalf("回拨不应倒退物理部分: b1=%v b2=%v", b1, b2)
	}
	t.Log(log.String())
}

func TestConcurrentAllocateUniqueAndOrdered(t *testing.T) {
	var log bytes.Buffer
	clock := &scriptClock{t: 40_000}
	a, _ := newTestAllocator(t, 100, 100, clock.get, &log)
	ctx := context.Background()
	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const perG = 50
	var wg sync.WaitGroup
	results := make([][]Timestamp, goroutines)
	errCh := make(chan error, goroutines)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			var err error
			results[g], err = a.Allocate(ctx, perG)
			if err != nil {
				errCh <- err
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent Allocate: %v", err)
	}

	all := make([]Timestamp, 0, goroutines*perG)
	for _, batch := range results {
		if len(batch) != perG {
			t.Fatalf("批量大小被破坏: %d", len(batch))
		}
		for i := 1; i < len(batch); i++ {
			if !batch[i-1].Less(batch[i]) {
				t.Fatalf("批内不递增: %s !< %s", batch[i-1], batch[i])
			}
		}
		all = append(all, batch...)
	}
	sorted := append([]Timestamp(nil), all...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Less(sorted[j]) })
	for i := 1; i < len(sorted); i++ {
		if !sorted[i-1].Less(sorted[i]) {
			t.Fatalf("全局出现重复或乱序: %s 与 %s", sorted[i-1], sorted[i])
		}
	}
	if sorted[0] != (Timestamp{40000, 0}) {
		t.Fatalf("序列起点异常: %s", sorted[0])
	}
	t.Logf("并发发放 %d 个时间戳，范围 %s .. %s\n%s",
		len(all), sorted[0], sorted[len(sorted)-1], log.String())
}

func TestRejectionCausesAreDistinguishable(t *testing.T) {
	ctx := context.Background()

	if _, err := New(0, 10, NewMemStore()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("L=0: %v", err)
	}
	if _, err := New(10, -1, NewMemStore()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("W=-1: %v", err)
	}

	clock := &scriptClock{t: 50_000}
	a, store := newTestAllocator(t, 5, 10, clock.get, nil)

	// 从节点优先报 ErrNotLeader（即便 n 也非法）。
	if _, err := a.Allocate(ctx, 6); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("从节点请求: %v", err)
	}
	if _, err := a.Allocate(ctx, 0); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("从节点请求 n=0: %v", err)
	}

	if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate(ctx, 0); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("n=0: %v", err)
	}
	if _, err := a.Allocate(ctx, -3); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("n=-3: %v", err)
	}
	if _, err := a.Allocate(ctx, 6); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("n>L: %v", err)
	}
	// 以不大于存量的任期接任：相等与更小都必须拒绝。
	if err := a.TakeOver(ctx, 1, clock.get()); !errors.Is(err, ErrStaleTerm) {
		t.Fatalf("相等任期接任: %v", err)
	}
	if err := a.TakeOver(ctx, 0, clock.get()); !errors.Is(err, ErrStaleTerm) {
		t.Fatalf("更小任期接任: %v", err)
	}
	if !a.IsLeader() || a.Term() != 1 {
		t.Fatalf("被拒绝的接任不得改变本地主节点状态")
	}
	if got := store.Current(); got.Term != 1 {
		t.Fatalf("被拒绝的接任不得改变存储: %+v", got)
	}
	// 非法请求不消耗：拒绝后正常请求仍从 (50000,0) 起。
	first := mustAllocate(t, a, 1)
	if first[0] != (Timestamp{50000, 0}) {
		t.Fatalf("被拒绝请求似乎消耗了时间戳: %s", first[0])
	}
}

func TestDeterministicReplay(t *testing.T) {
	// 相同的时钟、故障注入与请求序列重放，结果必须完全一致。
	run := func() []Timestamp {
		store := NewMemStore()
		clock := &scriptClock{t: 60_000}
		var log bytes.Buffer
		a, err := New(7, 3, store, WithClock(clock.get), WithLogger(&log))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := a.TakeOver(ctx, 1, clock.get()); err != nil {
			t.Fatal(err)
		}
		var out []Timestamp
		out = append(out, mustAllocate(t, a, 5)...) // 60000:0..4
		clock.t = 59_000                            // 时钟回拨
		out = append(out, mustAllocate(t, a, 2)...) // 60000:5..6
		store.InjectFault(WriteFaultFail)
		out = append(out, mustAllocate(t, a, 7)...) // 逻辑用尽移到 60001，续写失败但在界内
		out = append(out, mustAllocate(t, a, 7)...) // 60002，触发成功续写
		t.Log(log.String())
		return out
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("重放长度不同: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放在 [%d] 处不同: %s vs %s", i, first[i], second[i])
		}
	}
}
