package allocator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// 100 个并发调用：发放的 (版本,序号) 两两不同，且恰好覆盖前 100 个序号。
func TestHundredConcurrentAllocationsNoDuplicate(t *testing.T) {
	store := NewMemStore(nil)
	a := New(store, &bufLogger{})
	const n, b, v = 25, 10, 4
	mustRegister(t, a, "c", n, b, v)

	const callers = 100
	type pair struct{ ver, seq int }
	results := make([]pair, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx].ver, results[idx].seq, errs[idx] = a.Allocate(context.Background(), "c")
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[pair]bool{}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("caller %d unexpected error: %v", i, e)
		}
		if seen[results[i]] {
			t.Fatalf("duplicate allocation %+v", results[i])
		}
		seen[results[i]] = true
	}
	if len(seen) != callers {
		t.Fatalf("distinct = %d, want %d", len(seen), callers)
	}
	flat := make([]int, 0, callers)
	for p := range seen {
		flat = append(flat, (p.ver-1)*n+p.seq)
	}
	sort.Ints(flat)
	for i, x := range flat {
		if x != i {
			t.Fatalf("allocation set not a continuous prefix at %d: got %d", i, x)
		}
	}
}

// blockingStore 阻塞第一次预留，制造「批耗尽时多个调用者并发到达」。
type blockingStore struct {
	Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32

	mu        sync.Mutex
	blockDone bool
}

func (s *blockingStore) CompareAndSwapReserve(ctx context.Context, tenant string, old, next Watermark) (ReserveStatus, error) {
	s.calls.Add(1)
	s.mu.Lock()
	block := !s.blockDone
	s.blockDone = true
	s.mu.Unlock()
	if block {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	return s.Store.CompareAndSwapReserve(ctx, tenant, old, next)
}

// 批耗尽时并发调用者共用同一次预留：底层预留次数等于批次数，而非调用者数。
func TestConcurrentCallersShareSingleReservation(t *testing.T) {
	inner := NewMemStore(nil)
	bs := &blockingStore{Store: inner, started: make(chan struct{}), release: make(chan struct{})}
	a := New(bs, &bufLogger{})
	const n, b, v = 8, 3, 1
	mustRegister(t, a, "g", n, b, v)

	const waiters = 5
	type pair struct{ ver, seq int }
	res := make([]pair, waiters)
	errs := make([]error, waiters)
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res[idx].ver, res[idx].seq, errs[idx] = a.Allocate(context.Background(), "g")
		}(i)
	}
	<-bs.started
	close(bs.release)
	wg.Wait()

	got := map[pair]bool{}
	for _, e := range errs {
		if e != nil {
			t.Fatalf("unexpected error %v", e)
		}
	}
	for _, p := range res {
		if got[p] {
			t.Fatalf("duplicate concurrent allocation %+v", p)
		}
		got[p] = true
	}
	for i := waiters; i < n; i++ {
		ver, seq, err := a.Allocate(context.Background(), "g")
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		p := pair{ver, seq}
		if got[p] {
			t.Fatalf("duplicate later allocation %+v", p)
		}
		got[p] = true
	}
	if len(got) != n {
		t.Fatalf("distinct seqs = %d, want %d", len(got), n)
	}
	// B=3,N=8 -> 批次 3,3,2，共 3 次预留。
	if n := bs.calls.Load(); n != 3 {
		t.Fatalf("persist reservation calls = %d, want 3 (dedup expected)", n)
	}
	if _, _, err := a.Allocate(context.Background(), "g"); !errors.Is(err, ErrKeysExhausted) {
		t.Fatalf("want exhausted, got %v", err)
	}
}

// 明确失败下并发等待者全部失败；底层只有一次预留尝试，水位保持不变。
func TestConcurrentWaitersAllFailOnDefiniteFailure(t *testing.T) {
	inner := NewMemStore(func(_ string, _, _ Watermark) ReserveStatus {
		return ReserveFailed
	})
	bs := &blockingStore{Store: inner, started: make(chan struct{}), release: make(chan struct{})}
	a := New(bs, &bufLogger{})
	mustRegister(t, a, "f", 4, 2, 1)

	const waiters = 4
	joined := make(chan struct{}, waiters-1)
	var joinedCount atomic.Int32
	a.onJoinInflight = func() {
		if joinedCount.Add(1) <= waiters-1 {
			joined <- struct{}{}
		}
	}
	var wg sync.WaitGroup
	failed := atomic.Int32{}
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := a.Allocate(context.Background(), "f"); errors.Is(err, ErrReserveFailed) {
				failed.Add(1)
			}
		}()
	}
	<-bs.started
	for i := 0; i < waiters-1; i++ {
		<-joined
	}
	close(bs.release)
	wg.Wait()
	if failed.Load() != waiters {
		t.Fatalf("failed = %d, want %d", failed.Load(), waiters)
	}
	if got := bs.calls.Load(); got != 1 {
		t.Fatalf("persist calls = %d, want 1", got)
	}
	n, _, _, wm, err := inner.LoadTenant(context.Background(), "f")
	if err != nil || n != 4 || wm != (Watermark{1, 0}) {
		t.Fatalf("watermark changed after definite failure: n=%d wm=%+v err=%v", n, wm, err)
	}
}

// 确定性：相同操作序列 + 相同故障脚本，两次运行的成功发放序列完全一致。
func TestDeterministicWithSameFaultScript(t *testing.T) {
	script := func(attempt int) ReserveStatus {
		switch attempt {
		case 2, 5:
			return ReserveUnknown
		case 3:
			return ReserveFailed
		default:
			return ReserveOK
		}
	}
	run := func() []int {
		var attempt int
		var mu sync.Mutex
		store := NewMemStore(func(_ string, _, _ Watermark) ReserveStatus {
			mu.Lock()
			attempt++
			st := script(attempt)
			mu.Unlock()
			return st
		})
		a := New(store, &bufLogger{})
		mustRegister(t, a, "d", 6, 2, 2)
		var flat []int
		for {
			ver, seq, err := a.Allocate(context.Background(), "d")
			if errors.Is(err, ErrKeysExhausted) {
				return flat
			}
			if err != nil {
				continue
			}
			flat = append(flat, (ver-1)*6+seq)
			if len(flat) > 100 {
				t.Fatal("run did not terminate")
			}
		}
	}
	first := run()
	second := run()
	if len(first) == 0 {
		t.Fatal("no successful allocations")
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("non-deterministic results:\n%v\n%v", first, second)
	}
	seen := map[int]bool{}
	for _, x := range first {
		if seen[x] {
			t.Fatalf("duplicate flat seq %d", x)
		}
		seen[x] = true
	}
}
