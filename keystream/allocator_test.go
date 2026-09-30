package keystream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// testLogger 把判定日志写入 testing 输出（输入/输出/判定依据可见）。
type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Logf("%s", strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func newTestAllocator(t *testing.T, store Store) *Allocator {
	t.Helper()
	return New(store, testLogger{t})
}

func mustRegister(t *testing.T, a *Allocator, tenant string, n, b, v int64) {
	t.Helper()
	if err := a.Register(context.Background(), tenant, n, b, v); err != nil {
		t.Fatalf("Register(%q) unexpected error: %v", tenant, err)
	}
}

func mustAllocate(t *testing.T, a *Allocator, tenant string) Allocation {
	t.Helper()
	al, err := a.Allocate(context.Background(), tenant)
	if err != nil {
		t.Fatalf("Allocate(%q) unexpected error: %v", tenant, err)
	}
	return al
}

// expectPersistError 断言错误是 *PersistError 并校验 Unknown 标志。
func expectPersistError(t *testing.T, err error, wantUnknown bool) {
	t.Helper()
	var pe *PersistError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PersistError(unknown=%v), got %T: %v", wantUnknown, err, err)
	}
	if pe.Unknown != wantUnknown {
		t.Fatalf("want PersistError.Unknown=%v, got %v", wantUnknown, pe.Unknown)
	}
}

func allocN(t *testing.T, a *Allocator, tenant string, count int) []Allocation {
	t.Helper()
	out := make([]Allocation, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, mustAllocate(t, a, tenant))
	}
	return out
}

// TestRegisterValidation 注册校验：空、重复、N/B/V 非正、B>N，顺序敏感。
func TestRegisterValidation(t *testing.T) {
	a := newTestAllocator(t, NewScriptedStore())

	if err := a.Register(context.Background(), "", 10, 3, 2); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty tenant: want ErrEmptyTenant, got %v", err)
	}

	mustRegister(t, a, "t1", 10, 3, 2)

	// 已存在优先于参数非法。
	if err := a.Register(context.Background(), "t1", 0, 0, 0); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("duplicate tenant: want ErrTenantExists, got %v", err)
	}

	for _, tc := range []struct{ n, b, v int64 }{
		{0, 1, 1},
		{10, 0, 1},
		{10, 1, 0},
		{-1, 1, 1},
		{10, 11, 1},
	} {
		name := fmt.Sprintf("bad-n%d-b%d-v%d", tc.n, tc.b, tc.v)
		if err := a.Register(context.Background(), name, tc.n, tc.b, tc.v); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config (n=%d,b=%d,v=%d): want ErrInvalidConfig, got %v", tc.n, tc.b, tc.v, err)
		}
	}
}

// TestAllocateValidationOrder 分配校验顺序：空 -> 未注册 -> 耗尽；持久化失败可区分。
func TestAllocateValidationOrder(t *testing.T) {
	a := newTestAllocator(t, NewScriptedStore())

	if _, err := a.Allocate(context.Background(), ""); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty: want ErrEmptyTenant, got %v", err)
	}
	if _, err := a.Allocate(context.Background(), "ghost"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("missing: want ErrTenantNotFound, got %v", err)
	}

	// N=2, V=1, B=2：只能发放 2 个序号，第 3 次报耗尽。
	mustRegister(t, a, "tiny", 2, 2, 1)
	if got := mustAllocate(t, a, "tiny"); got != (Allocation{1, 0}) {
		t.Fatalf("first alloc = %+v", got)
	}
	if got := mustAllocate(t, a, "tiny"); got != (Allocation{1, 1}) {
		t.Fatalf("second alloc = %+v", got)
	}
	if _, err := a.Allocate(context.Background(), "tiny"); !errors.Is(err, ErrKeyExhausted) {
		t.Fatalf("want ErrKeyExhausted, got %v", err)
	}

	// 持久化失败必须与业务错误可区分。
	store := NewScriptedStore(Fault{Op: "save_reserved", Call: 1, Unknown: false})
	a2 := newTestAllocator(t, store)
	mustRegister(t, a2, "p", 10, 3, 1)
	_, err := a2.Allocate(context.Background(), "p")
	expectPersistError(t, err, false)
	if errors.Is(err, ErrKeyExhausted) || errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("persist error must be distinguishable, got %v", err)
	}
}

// TestBatchTruncatedAtVersionBoundary 批在版本边界被截断：
// N=5, B=3 时批次为 3、2、3、2（末批取版本剩余数）。
func TestBatchTruncatedAtVersionBoundary(t *testing.T) {
	store := NewScriptedStore()
	a := newTestAllocator(t, store)
	mustRegister(t, a, "tenantA", 5, 3, 3)

	allocs := allocN(t, a, "tenantA", 15)

	// 无崩溃、无失败：每版本内恰为从 0 起的连续前缀。
	for i, al := range allocs {
		want := Allocation{Version: int64(i/5 + 1), Seq: int64(i % 5)}
		if al != want {
			t.Fatalf("alloc[%d] = %+v, want %+v", i, al, want)
		}
	}
	if _, err := a.Allocate(context.Background(), "tenantA"); !errors.Is(err, ErrKeyExhausted) {
		t.Fatalf("V=3, N=5 exhausted: want ErrKeyExhausted, got %v", err)
	}
}

// TestHighWatermarkTrack 验证高水位轨迹与版本边界截断一致：3,5,8,10。
func TestHighWatermarkTrack(t *testing.T) {
	store := NewScriptedStore()
	a := newTestAllocator(t, store)
	mustRegister(t, a, "h", 5, 3, 2)

	mustAllocate(t, a, "h")
	if got := store.Reserved("h"); got != 3 {
		t.Fatalf("after 1 issue reserved = %d, want 3", got)
	}
	allocN(t, a, "h", 2)    // seq1,2：第一批用完
	mustAllocate(t, a, "h") // seq3：触发截断批 [3,5)
	if got := store.Reserved("h"); got != 5 {
		t.Fatalf("reserved at boundary = %d, want 5", got)
	}
	// 截断批 [3,5) 内的最后一个序号：不触发新预留。
	if got := mustAllocate(t, a, "h"); got != (Allocation{1, 4}) {
		t.Fatalf("last seq of v1 = %+v, want {1 4}", got)
	}
	if got := store.Reserved("h"); got != 5 {
		t.Fatalf("reserved unchanged inside batch = %d, want 5", got)
	}
	if got := mustAllocate(t, a, "h"); got != (Allocation{2, 0}) {
		t.Fatalf("first alloc of v2 = %+v, want {2 0}", got)
	}
	if got := store.Reserved("h"); got != 8 {
		t.Fatalf("reserved in v2 first batch = %d, want 8", got)
	}
}

// TestCrashRecovery 崩溃后从持久层高水位继续，未发放预留序号作废，浪费 <= B。
func TestCrashRecovery(t *testing.T) {
	store := NewScriptedStore()
	a := newTestAllocator(t, store)
	mustRegister(t, a, "c", 10, 4, 2)

	// 预留 [0,4) 发 0-3，再预留 [4,8) 发 4。
	allocN(t, a, "c", 5)
	if got := store.Reserved("c"); got != 8 {
		t.Fatalf("reserved before crash = %d, want 8", got)
	}

	// 崩溃：丢弃内存状态，新建分配器并恢复。5,6,7 作废（3 <= B=4）。
	a2 := newTestAllocator(t, store)
	if err := a2.RecoverAll(context.Background()); err != nil {
		t.Fatalf("RecoverAll: %v", err)
	}

	got := mustAllocate(t, a2, "c")
	if got != (Allocation{1, 8}) {
		t.Fatalf("after recovery = %+v, want {1 8}", got)
	}
	if err := a2.Recover(context.Background(), "nobody"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("recover missing: want ErrTenantNotFound, got %v", err)
	}
	// 崩溃前发过 v1:0..4；恢复后 5,6,7 作废，绝不重放。
	if next := mustAllocate(t, a2, "c"); next != (Allocation{1, 9}) {
		t.Fatalf("next = %+v, want {1 9}", next)
	}
}

// TestDefinitiveFailureRetrySameBatch 明确失败：高水位不变，下一次重试同一批。
func TestDefinitiveFailureRetrySameBatch(t *testing.T) {
	store := NewScriptedStore(Fault{Op: "save_reserved", Call: 1, Unknown: false})
	a := newTestAllocator(t, store)
	mustRegister(t, a, "d", 10, 4, 1)

	_, err := a.Allocate(context.Background(), "d")
	expectPersistError(t, err, false)
	if got := store.Reserved("d"); got != 0 {
		t.Fatalf("definitive failure changed persisted hwm to %d", got)
	}

	// 故障解除后重试同一批：从 seq0 开始，没有序号丢失。
	store.SetFaults()
	if got := mustAllocate(t, a, "d"); got != (Allocation{1, 0}) {
		t.Fatalf("after definitive failure = %+v, want {1 0}", got)
	}
	if got := store.Reserved("d"); got != 4 {
		t.Fatalf("reserved after retry = %d, want 4", got)
	}
}

// TestUnknownOutcomeBatchVoided 结果未知：批视为已预留且作废，从该批之后重新预留。
func TestUnknownOutcomeBatchVoided(t *testing.T) {
	store := NewScriptedStore(Fault{Op: "save_reserved", Call: 1, Unknown: true})
	a := newTestAllocator(t, store)
	mustRegister(t, a, "u", 10, 4, 2)

	_, err := a.Allocate(context.Background(), "u")
	expectPersistError(t, err, true)
	if got := store.Reserved("u"); got != 4 {
		t.Fatalf("unknown failure persisted hwm = %d, want 4", got)
	}

	// 下一次从该批之后重新预留：得到 seq4，而不是 seq0。
	if got := mustAllocate(t, a, "u"); got != (Allocation{1, 4}) {
		t.Fatalf("after unknown failure = %+v, want {1 4} (0..3 voided)", got)
	}
	if got := store.Reserved("u"); got != 8 {
		t.Fatalf("reserved after re-reserve = %d, want 8", got)
	}
}

// TestUnknownFailureTruncatedBatch 结果未知命中边界截断批：浪费不超过 B，随后进入新版本。
func TestUnknownFailureTruncatedBatch(t *testing.T) {
	// N=5, B=4：第一批 [0,4)；边界批截断为 [4,5)（大小 1）。
	store := NewScriptedStore()
	a := newTestAllocator(t, store)
	mustRegister(t, a, "b", 5, 4, 2)

	allocN(t, a, "b", 4) // seq0..3
	store.SetFaults(Fault{Op: "save_reserved", Call: 2, Unknown: true})
	_, err := a.Allocate(context.Background(), "b")
	expectPersistError(t, err, true)

	store.SetFaults()
	if got := mustAllocate(t, a, "b"); got != (Allocation{2, 0}) {
		t.Fatalf("after unknown truncated batch = %+v, want {2 0}", got)
	}
}

// TestMultiTenantIsolation 多租户互不影响：一个租户的故障不阻断另一租户。
func TestMultiTenantIsolation(t *testing.T) {
	store := NewScriptedStore(Fault{Op: "save_reserved", Call: 1, Unknown: false})
	a := newTestAllocator(t, store)
	mustRegister(t, a, "alpha", 5, 2, 1)
	mustRegister(t, a, "beta", 5, 2, 1)

	// alpha 的第一次预留（第 1 次 save 调用）明确失败。
	if _, err := a.Allocate(context.Background(), "alpha"); err == nil {
		t.Fatalf("alpha should hit definitive failure")
	}
	// beta 预留时是第 2 次调用，不命中故障，正常发放。
	if got := mustAllocate(t, a, "beta"); got != (Allocation{1, 0}) {
		t.Fatalf("beta alloc = %+v, want {1 0}", got)
	}
	// alpha 故障解除后仍从 seq0 开始；两租户序号空间独立。
	store.SetFaults()
	if got := mustAllocate(t, a, "alpha"); got != (Allocation{1, 0}) {
		t.Fatalf("alpha retry = %+v, want {1 0}", got)
	}
	if got := mustAllocate(t, a, "beta"); got != (Allocation{1, 1}) {
		t.Fatalf("beta second = %+v, want {1 1}", got)
	}
}

// TestConcurrentNoDuplicates 100 个并发调用无重复，且只触发必要次数的预留（共用同一次预留）。
func TestConcurrentNoDuplicates(t *testing.T) {
	const goroutines = 100
	store := NewScriptedStore()
	a := newTestAllocator(t, store)
	// N=200, B=10：100 次发放恰好落在 10 个批次内。
	mustRegister(t, a, "concurrent", 200, 10, 2)

	var wg sync.WaitGroup
	results := make([]Allocation, goroutines)
	start := make(chan struct{})
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			al, err := a.Allocate(context.Background(), "concurrent")
			if err != nil {
				t.Errorf("concurrent allocate %d: %v", i, err)
				return
			}
			results[i] = al
		}()
	}
	close(start)
	wg.Wait()

	seen := make(map[Allocation]int, goroutines)
	for i, al := range results {
		if prev, dup := seen[al]; dup {
			t.Fatalf("duplicate allocation %+v at index %d and %d", al, prev, i)
		}
		seen[al] = i
	}

	// 100 个序号全部分布在版本 1 的 0..99（虽然以并发乱序到达）。
	for al := range seen {
		if al.Version != 1 || al.Seq < 0 || al.Seq >= goroutines {
			t.Fatalf("allocation out of expected range: %+v", al)
		}
	}
	if len(seen) != goroutines {
		t.Fatalf("unique allocations = %d, want %d", len(seen), goroutines)
	}

	// 100 个调用只应触发 10 次预留（每批 10，等待者共用当次预留）。
	if got := store.CallCount("save_reserved"); got != 10 {
		t.Fatalf("save_reserved calls = %d, want 10 (concurrent callers must share one reservation)", got)
	}
}

// TestFailedAllocationIssuesNothing 被拒绝或失败的分配绝不发放序号。
func TestFailedAllocationIssuesNothing(t *testing.T) {
	store := NewScriptedStore(Fault{Op: "save_reserved", Call: 1, Unknown: false})
	a := newTestAllocator(t, store)
	mustRegister(t, a, "f", 10, 4, 1)

	_, err := a.Allocate(context.Background(), "f")
	expectPersistError(t, err, false)
	store.SetFaults()

	// 失败之后第一次成功分配必须仍是 seq0，证明失败未占用任何序号。
	if got := mustAllocate(t, a, "f"); got != (Allocation{1, 0}) {
		t.Fatalf("seq after failed allocation = %+v, want {1 0}", got)
	}
}

// runFaultScript 用相同操作序列与相同故障脚本执行一遍，返回成功发放结果。
func runFaultScript(t *testing.T, faults []Fault) []Allocation {
	t.Helper()
	store := NewScriptedStore(faults...)
	a := newTestAllocator(t, store)
	mustRegister(t, a, "script", 10, 4, 2)

	var issued []Allocation
	for i := 0; i < 12; i++ {
		al, err := a.Allocate(context.Background(), "script")
		if err != nil {
			var pe *PersistError
			if !errors.As(err, &pe) {
				t.Fatalf("unexpected non-persist error at step %d: %v", i, err)
			}
			continue
		}
		issued = append(issued, al)
	}
	return issued
}

// TestDeterministicSameScript 相同操作序列 + 相同故障脚本 => 相同发放结果。
func TestDeterministicSameScript(t *testing.T) {
	faults := []Fault{
		{Op: "save_reserved", Call: 1, Unknown: false}, // 第一批明确失败，重试
		{Op: "save_reserved", Call: 3, Unknown: true},  // 第三批结果未知，作废
	}
	run1 := runFaultScript(t, faults)
	run2 := runFaultScript(t, faults)
	if len(run1) != len(run2) {
		t.Fatalf("runs differ in length: %d vs %d", len(run1), len(run2))
	}
	for i := range run1 {
		if run1[i] != run2[i] {
			t.Fatalf("runs differ at %d: %+v vs %+v", i, run1[i], run2[i])
		}
	}

	// 结果未知批次被跳过：发放集合里不会出现该批的序号（seq4..7 作废）。
	for _, al := range run1 {
		if al.Version == 1 && al.Seq >= 4 && al.Seq <= 7 {
			t.Fatalf("voided batch seq %d must never be issued: %+v", al.Seq, al)
		}
	}
	t.Logf("deterministic issued sequence: %+v", run1)
}
