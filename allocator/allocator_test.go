package allocator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// bufLogger 同时输出到测试日志与缓冲区，便于断言「输入/输出/判定依据」已打印。
type bufLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
	t   *testing.T
}

func (l *bufLogger) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	l.buf.WriteString(line + "\n")
	l.mu.Unlock()
	if l.t != nil {
		l.t.Log(line)
	}
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func mustRegister(t *testing.T, a *Allocator, tenant string, n, b, v int) {
	t.Helper()
	if err := a.Register(context.Background(), tenant, n, b, v); err != nil {
		t.Fatalf("register %q: %v", tenant, err)
	}
}

func mustAlloc(t *testing.T, a *Allocator, tenant string) (int, int) {
	t.Helper()
	ver, seq, err := a.Allocate(context.Background(), tenant)
	if err != nil {
		t.Fatalf("allocate %q: %v", tenant, err)
	}
	return ver, seq
}

// 无崩溃无失败：版本内序号恰为从 0 起的连续前缀，版本边界正确切换。
func TestContinuousPrefixAndVersionRollover(t *testing.T) {
	store := NewMemStore(nil)
	a := New(store, &bufLogger{t: t})
	const n, b, v = 5, 2, 2
	mustRegister(t, a, "t1", n, b, v)
	for i := 0; i < n*v; i++ {
		ver, seq := mustAlloc(t, a, "t1")
		wantVer := i/n + 1
		if ver != wantVer || seq != i%n {
			t.Fatalf("alloc #%d = (v%d,seq%d), want (v%d,seq%d)", i, ver, seq, wantVer, i%n)
		}
	}
	if _, _, err := a.Allocate(context.Background(), "t1"); !errors.Is(err, ErrKeysExhausted) {
		t.Fatalf("want ErrKeysExhausted, got %v", err)
	}
}

// 批在版本边界被截断：N=5,B=4 -> 每版本批次为 4,1（不跨版本）。
func TestBatchTruncatedAtVersionBoundary(t *testing.T) {
	var mu sync.Mutex
	var observed []int
	store := NewMemStore(func(_ string, _, next Watermark) ReserveStatus {
		mu.Lock()
		defer mu.Unlock()
		observed = append(observed, next.NextSeq)
		return ReserveOK
	})
	a := New(store, &bufLogger{t: t})
	const n, b, v = 5, 4, 2
	mustRegister(t, a, "t1", n, b, v)
	for i := 0; i < n*v; i++ {
		mustAlloc(t, a, "t1")
	}
	want := []int{4, 5, 4, 5}
	if fmt.Sprint(observed) != fmt.Sprint(want) {
		t.Fatalf("reservation watermarks = %v, want %v (batches 4,1,4,1)", observed, want)
	}
}

// 崩溃恢复：发放 0,1,2（B=2 时持久化已预留到 seq=4），
// 新实例从高水位 (1,4) 继续，序号 3 作废，每次崩溃浪费 < B 个。
func TestCrashRecoveryFromHighWatermark(t *testing.T) {
	store := NewMemStore(nil)
	a1 := New(store, &bufLogger{t: t})
	const n, b, v = 10, 2, 2
	mustRegister(t, a1, "t1", n, b, v)
	for i := 0; i < 3; i++ {
		mustAlloc(t, a1, "t1")
	}
	a2 := New(store, &bufLogger{t: t})
	if err := a2.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	ver, seq := mustAlloc(t, a2, "t1")
	if ver != 1 || seq != 4 {
		t.Fatalf("after recovery got (v%d,seq%d), want (v1,seq4)", ver, seq)
	}
	// 从高水位继续直到全部 N*V 个序号发完，检查全局无重复。
	seen := map[[2]int]bool{{1, 0}: true, {1, 1}: true, {1, 2}: true, {1, 4}: true}
	for {
		vv, s, err := a2.Allocate(context.Background(), "t1")
		if errors.Is(err, ErrKeysExhausted) {
			break
		}
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		key := [2]int{vv, s}
		if seen[key] {
			t.Fatalf("duplicate (v%d,seq%d) after recovery", vv, s)
		}
		seen[key] = true
	}
	// 本次崩溃作废了序号 3（预留 [0,4)，崩溃前只发放 0,1,2），故少 1 个，且浪费 1 < B=2。
	if len(seen) != n*v-1 {
		t.Fatalf("distinct allocations after recovery = %d, want %d", len(seen), n*v-1)
	}
}

// 每次崩溃浪费序号不超过 B。
func TestCrashWasteAtMostBatchSize(t *testing.T) {
	store := NewMemStore(nil)
	const n, b, v = 10, 3, 1
	a := New(store, &bufLogger{t: t})
	mustRegister(t, a, "t1", n, b, v)
	mustAlloc(t, a, "t1")
	lastSeq := 0
	for crash := 0; crash < 2; crash++ {
		a = New(store, &bufLogger{t: t})
		if err := a.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, seq := mustAlloc(t, a, "t1")
		if seq <= lastSeq {
			t.Fatalf("crash %d non-monotonic seq=%d", crash, seq)
		}
		if seq-lastSeq > b {
			t.Fatalf("crash %d wasted %d > B=%d", crash, seq-lastSeq, b)
		}
		lastSeq = seq
	}
}

// 明确失败：确定未落盘，内存与持久化水位都不变，重试仍从 seq=0 发放。
func TestReserveDefiniteFailure(t *testing.T) {
	var attempts int
	store := NewMemStore(func(_ string, _, _ Watermark) ReserveStatus {
		attempts++
		if attempts == 1 {
			return ReserveFailed
		}
		return ReserveOK
	})
	a := New(store, &bufLogger{t: t})
	const n, b, v = 4, 2, 1
	mustRegister(t, a, "t1", n, b, v)
	if _, _, err := a.Allocate(context.Background(), "t1"); !errors.Is(err, ErrReserveFailed) {
		t.Fatalf("want ErrReserveFailed, got %v", err)
	}
	ver, seq := mustAlloc(t, a, "t1")
	if ver != 1 || seq != 0 {
		t.Fatalf("after definite failure got (v%d,seq%d), want (v1,seq0)", ver, seq)
	}
	if _, s := mustAlloc(t, a, "t1"); s != 1 {
		t.Fatalf("second alloc seq = %d, want 1", s)
	}
}

// 结果未知：该批视为已预留并整批作废，本次失败，下次从该批之后重新预留。
func TestReserveUnknownOutcome(t *testing.T) {
	var attempts int
	store := NewMemStore(func(_ string, _, _ Watermark) ReserveStatus {
		attempts++
		if attempts == 1 {
			return ReserveUnknown
		}
		return ReserveOK
	})
	a := New(store, &bufLogger{t: t})
	const n, b, v = 6, 2, 1
	mustRegister(t, a, "t1", n, b, v)
	if _, _, err := a.Allocate(context.Background(), "t1"); !errors.Is(err, ErrReserveUnknown) {
		t.Fatalf("want ErrReserveUnknown, got %v", err)
	}
	ver, seq := mustAlloc(t, a, "t1")
	if ver != 1 || seq != 2 {
		t.Fatalf("after unknown got (v%d,seq%d), want (v1,seq2)", ver, seq)
	}
	// 首次成功分配又预留了 [2,4)：恢复后下一个序号是 4。
	a2 := New(store, &bufLogger{t: t})
	if err := a2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	v2, s2 := mustAlloc(t, a2, "t1")
	if v2 != 1 || s2 != 4 {
		t.Fatalf("post-recovery got (v%d,seq%d), want (v1,seq4)", v2, s2)
	}
}

// 最后版本首批预留结果未知：本次失败，随后报密钥耗尽。
func TestExhaustionAfterUnknownOnLastBatch(t *testing.T) {
	store := NewMemStore(func(_ string, _, next Watermark) ReserveStatus {
		if next.Version == 2 {
			return ReserveUnknown
		}
		return ReserveOK
	})
	a := New(store, &bufLogger{t: t})
	const n, b, v = 2, 2, 2
	mustRegister(t, a, "t1", n, b, v)
	for i := 0; i < n; i++ {
		mustAlloc(t, a, "t1")
	}
	if _, _, err := a.Allocate(context.Background(), "t1"); !errors.Is(err, ErrReserveUnknown) {
		t.Fatalf("want ErrReserveUnknown, got %v", err)
	}
	if _, _, err := a.Allocate(context.Background(), "t1"); !errors.Is(err, ErrKeysExhausted) {
		t.Fatalf("want ErrKeysExhausted, got %v", err)
	}
}

// 校验顺序与错误可区分性。
func TestValidationOrder(t *testing.T) {
	store := NewMemStore(nil)
	a := New(store, &bufLogger{t: t})
	if _, _, err := a.Allocate(context.Background(), ""); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty allocate: %v", err)
	}
	if err := a.Register(context.Background(), "", 1, 1, 1); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty register: %v", err)
	}
	for _, params := range [][3]int{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {3, 4, 1}, {-1, 1, 1}} {
		if err := a.Register(context.Background(), "bad", params[0], params[1], params[2]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("params %v: %v", params, err)
		}
	}
	mustRegister(t, a, "dup", 2, 2, 1)
	if err := a.Register(context.Background(), "dup", 2, 2, 1); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("dup register: %v", err)
	}
	if _, _, err := a.Allocate(context.Background(), "ghost"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("missing tenant: %v", err)
	}
}

// 多租户水位互不影响。
func TestMultipleTenantsIsolated(t *testing.T) {
	store := NewMemStore(nil)
	a := New(store, &bufLogger{t: t})
	mustRegister(t, a, "A", 3, 2, 1)
	mustRegister(t, a, "B", 5, 3, 1)
	if ver, s := mustAlloc(t, a, "A"); ver != 1 || s != 0 {
		t.Fatalf("A first = (v%d,seq%d)", ver, s)
	}
	if ver, s := mustAlloc(t, a, "B"); ver != 1 || s != 0 {
		t.Fatalf("B first = (v%d,seq%d)", ver, s)
	}
	if _, s := mustAlloc(t, a, "A"); s != 1 {
		t.Fatalf("A second = seq%d, want 1", s)
	}
	for i := 1; i < 5; i++ {
		if _, s := mustAlloc(t, a, "B"); s != i {
			t.Fatalf("B #%d = seq%d", i, s)
		}
	}
	if _, _, err := a.Allocate(context.Background(), "B"); !errors.Is(err, ErrKeysExhausted) {
		t.Fatalf("B exhausted: %v", err)
	}
	if _, s := mustAlloc(t, a, "A"); s != 2 {
		t.Fatalf("A after B exhausted = seq%d, want 2", s)
	}
}

// 日志中打印输入、输出与判定依据。
func TestLogsContainInputOutputAndReason(t *testing.T) {
	store := NewMemStore(nil)
	logger := &bufLogger{}
	a := New(store, logger)
	mustRegister(t, a, "logt", 2, 2, 1)
	if _, _, err := a.Allocate(context.Background(), ""); err == nil {
		t.Fatal("want error")
	}
	mustAlloc(t, a, "logt")
	out := logger.String()
	for _, want := range []string{"input=", "output=", "reason=", "(v1,seq0)"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
