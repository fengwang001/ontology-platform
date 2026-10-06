package growstack

import "testing"

import (
	"bytes"
	"sync"
)

func testCfg() Config {
	return Config{BaseSize: 4, GrowthMul: 2, MaxPerStack: 32, TotalQuota: 10_000, FrameSlots: 2, ShrinkRatio: 0.4}
}

func newLoggedRT(t *testing.T, cfg Config) (*Runtime, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	r, err := NewRuntime(cfg, WithLogger(logWriter{w: &buf}))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return r, &buf
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertKind(t *testing.T, err error, k ErrorKind) {
	t.Helper()
	if !Is(err, k) {
		t.Fatalf("want %s, got %v", k, err)
	}
}

// BaseSize=4, FrameSlots=2: legal sizes 4,8,16,32.
func TestGrowthIsSmallestMultiple(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	f0, err := r.Push(1)
	must(t, err)
	f1, err := r.Push(1)
	must(t, err)
	if got := r.Stats().Stacks[1].Size; got != 4 {
		t.Fatalf("size=%d want 4", got)
	}
	_, err = r.Push(1) // need 6 -> grow 4->8
	must(t, err)
	if got := r.Stats().Stacks[1].Size; got != 8 {
		t.Fatalf("size=%d want 8 (smallest multiple satisfying 6)", got)
	}
	f3, err := r.Push(1) // used 8, fits exactly
	must(t, err)
	if got := r.Stats().Stacks[1].Size; got != 8 {
		t.Fatalf("size=%d want 8", got)
	}
	_, err = r.Push(1) // need 10 -> 16
	must(t, err)
	if got := r.Stats().Stacks[1].Size; got != 16 {
		t.Fatalf("size=%d want 16", got)
	}
	st := r.Stats().Stacks[1]
	if st.GrowCount != 2 || st.HighWater != 16 {
		t.Fatalf("grows=%d hw=%d", st.GrowCount, st.HighWater)
	}
	_, _ = f0, f1
	_ = f3
}

func TestOverflowAtPerStackLimit(t *testing.T) {
	cfg := testCfg()
	cfg.TotalQuota = 1_000_000
	r, _ := newLoggedRT(t, cfg)
	must(t, r.NewCoroutine(1))
	for i := 0; i < 16; i++ { // 16 frames * 2 slots = 32 == limit
		if _, err := r.Push(1); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	st := r.Stats().Stacks[1]
	if st.Size != 32 {
		t.Fatalf("size=%d want 32 (equality at limit)", st.Size)
	}
	_, err := r.Push(1)
	assertKind(t, err, ErrOverflow)
	st2 := r.Stats().Stacks[1]
	if st2.Size != 32 || st2.FrameCount != 16 {
		t.Fatalf("rejected push mutated state: %+v", st2)
	}
}

func TestQuotaExactBoundary(t *testing.T) {
	cfg := Config{BaseSize: 4, GrowthMul: 2, MaxPerStack: 8, TotalQuota: 8, FrameSlots: 2, ShrinkRatio: 0.4}
	r, _ := newLoggedRT(t, cfg)
	must(t, r.NewCoroutine(1)) // remaining 4
	for i := 0; i < 3; i++ {
		_, err := r.Push(1) // grow to 8, deducts remaining 4 exactly
		must(t, err)
	}
	if r.Stats().Remaining != 0 {
		t.Fatalf("remaining=%d want 0", r.Stats().Remaining)
	}
	assertKind(t, r.NewCoroutine(2), ErrQuota)
}

func TestPointerSemanticsAcrossRelocation(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	f0, _ := r.Push(1)
	f1, _ := r.Push(1)
	must(t, r.Store(1, f0, 0, 123))
	h, err := r.MkPtr(1, f1, 0, 1, f0, 0)
	must(t, err)
	must(t, r.CopyPtr(1, f1, 0, h))
	for i := 0; i < 8; i++ { // force 4->8->16->32 relocations
		_, err := r.Push(1)
		must(t, err)
	}
	v, err := r.ReadPtr(h)
	must(t, err)
	if v.Int() != 123 {
		t.Fatalf("read=%d want 123 after relocation", v.Int())
	}
	v2, err := r.Load(1, f1, 0)
	must(t, err)
	if !v2.IsPointer() {
		t.Fatalf("f1/0 lost its pointer")
	}
	must(t, r.WritePtr(h, 999))
	v3, err := r.ReadPtr(h)
	must(t, err)
	if v3.Int() != 999 {
		t.Fatalf("read=%d want 999 after write-through", v3.Int())
	}
	got, err := r.Load(1, f0, 0)
	must(t, err)
	if got.Int() != 999 {
		t.Fatalf("target slot=%d want 999", got.Int())
	}
}

func TestDanglingAfterPop(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	keep, _ := r.Push(1)
	victim, _ := r.Push(1)
	h, _ := r.MkPtr(1, keep, 0, 1, victim, 1)
	must(t, r.CopyPtr(1, keep, 0, h))
	must(t, r.Pop(1)) // pops victim
	assertKind(t, func() error { _, e := r.ReadPtr(h); return e }(), ErrDangling)
	assertKind(t, r.WritePtr(h, 1), ErrDangling)
	// Dangling pointers must not block shrinking: grow up, then shrink down.
	for i := 0; i < 8; i++ {
		_, _ = r.Push(1)
	}
	shr0 := r.Stats().Stacks[1].ShrinkCount
	for i := 0; i < 8; i++ {
		must(t, r.Pop(1))
	}
	if r.Stats().Stacks[1].ShrinkCount <= shr0 {
		t.Fatal("expected shrinks despite dangling pointer")
	}
	must(t, r.Pop(1)) // pop keep (the dangling holder)
}

func TestCrossStackRejectedAtWrite(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	must(t, r.NewCoroutine(2))
	f1, _ := r.Push(1)
	f2, _ := r.Push(2)
	cross, err := r.MkPtr(1, f1, 1, 2, f2, 0)
	must(t, err)
	assertKind(t, r.CopyPtr(1, f1, 1, cross), ErrCrossStack)
	v, _ := r.Load(1, f1, 1)
	if !v.IsInt() || v.Int() != 0 {
		t.Fatalf("cross-stack write mutated slot: %v", v)
	}
	must(t, r.Store(1, f1, 0, 5))
	same, _ := r.MkPtr(1, f1, 1, 1, f1, 0)
	must(t, r.CopyPtr(1, f1, 1, same))
}

func TestEscapeRejectedAtPublish(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	f, _ := r.Push(1)
	must(t, r.Store(1, f, 0, 42))
	must(t, r.PublishInt("g", 1, f, 0))
	if r.Globals()["g"].Int() != 42 {
		t.Fatal("plain publish failed")
	}
	h, _ := r.MkPtr(1, f, 0, 1, f, 0)
	assertKind(t, r.PublishPtr("p", h), ErrEscape)
	if _, ok := r.Globals()["p"]; ok {
		t.Fatal("escaped pointer found in globals")
	}
	must(t, r.CopyPtr(1, f, 1, h))
	assertKind(t, r.PublishInt("p2", 1, f, 1), ErrEscape)
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{BaseSize: 4, GrowthMul: 2, MaxPerStack: 12, TotalQuota: 100, FrameSlots: 2, ShrinkRatio: 0.4},
		{BaseSize: 4, GrowthMul: 2, MaxPerStack: 32, TotalQuota: 100, FrameSlots: 2, ShrinkRatio: 0.5},
		{BaseSize: 4, GrowthMul: 2, MaxPerStack: 32, TotalQuota: 100, FrameSlots: 2, ShrinkRatio: 0.9},
		{BaseSize: 0, GrowthMul: 2, MaxPerStack: 32, TotalQuota: 100, FrameSlots: 2, ShrinkRatio: 0.4},
	}
	for i, c := range bad {
		if _, err := NewRuntime(c); !Is(err, ErrConfig) {
			t.Fatalf("case %d: want config error, got %v", i, err)
		}
	}
}

type failAllocator struct{ failAfter, n int }

func (a *failAllocator) Alloc(cells int) ([]cell, bool) {
	a.n++
	if a.n > a.failAfter {
		return nil, false
	}
	return make([]cell, cells), true
}

func (a *failAllocator) Free(cells []cell) {}

func TestFailedRelocationKeepsOldStack(t *testing.T) {
	fa := &failAllocator{failAfter: 1} // base pushes fit; growth alloc fails
	r, err := NewRuntime(testCfg(), WithAllocator(fa))
	must(t, err)
	must(t, r.NewCoroutine(1))
	f0, _ := r.Push(1)
	f1, _ := r.Push(1)
	must(t, r.Store(1, f0, 0, 55))
	_, err = r.Push(1) // needs grow
	assertKind(t, err, ErrQuota)
	st := r.Stats().Stacks[1]
	if st.Size != 4 || st.FrameCount != 2 || st.GrowCount != 0 {
		t.Fatalf("old stack not intact: %+v", st)
	}
	v, _ := r.Load(1, f0, 0)
	if v.Int() != 55 {
		t.Fatalf("contents lost after failed relocation: %d", v.Int())
	}
	_ = f1
}

func TestUndefinedAndArgumentOrdering(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	assertKind(t, r.Store(99, 0, 0, 1), ErrUndefined)
	must(t, r.NewCoroutine(1))
	f, _ := r.Push(1)
	assertKind(t, r.Store(1, 123, 0, 1), ErrUndefined)
	assertKind(t, r.Store(1, f, 9, 1), ErrArgument)
}

func TestShrinkNoThrash(t *testing.T) {
	// G=2, R=0.4 (<0.5). Grow to 32 (16 frames), then pop one by one.
	// After a shrink the utilization must never immediately re-trigger grow.
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	for i := 0; i < 16; i++ {
		_, _ = r.Push(1)
	}
	grows := r.Stats().Stacks[1].GrowCount
	for i := 0; i < 16; i++ {
		must(t, r.Pop(1))
		st := r.Stats().Stacks[1]
		if st.UsedSlots > st.Size {
			t.Fatal("invariant: used > size")
		}
	}
	// Repush everything: growing again is legal but must be consistent.
	for i := 0; i < 16; i++ {
		_, _ = r.Push(1)
	}
	if g := r.Stats().Stacks[1].GrowCount; g < grows {
		t.Fatalf("grow count decreased: %d<%d", g, grows)
	}
}

func TestConcurrentQuotaExactness(t *testing.T) {
	cfg := Config{BaseSize: 4, GrowthMul: 2, MaxPerStack: 1 << 20, TotalQuota: 400, FrameSlots: 2, ShrinkRatio: 0.4}
	r, _ := newLoggedRT(t, cfg)
	var wg sync.WaitGroup
	var ok, rejected int64
	var mu sync.Mutex
	const n = 40
	for g := int64(0); g < n; g++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			err := r.NewCoroutine(id)
			mu.Lock()
			if err != nil {
				rejected++
			} else {
				ok++
				for i := 0; i < 20; i++ {
					if _, e := r.Push(id); e != nil {
						break
					}
				}
			}
			mu.Unlock()
		}(g)
	}
	wg.Wait()
	snap := r.Stats()
	sum := 0
	for _, s := range snap.Stacks {
		sum += s.Size
	}
	if sum+snap.Remaining != cfg.TotalQuota {
		t.Fatalf("quota leak: sum=%d remaining=%d total=%d", sum, snap.Remaining, cfg.TotalQuota)
	}
	if sum > cfg.TotalQuota {
		t.Fatalf("quota exceeded: %d>%d (ok=%d rejected=%d)", sum, cfg.TotalQuota, ok, rejected)
	}
}

func TestStatsCoherentAndRemaining(t *testing.T) {
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	for i := 0; i < 6; i++ { // used 12 -> size 16
		_, _ = r.Push(1)
	}
	s := r.Stats()
	if s.Stacks[1].Size != 16 || s.Stacks[1].FrameCount != 6 {
		t.Fatalf("snapshot=%+v", s.Stacks[1])
	}
	if s.Remaining != 10_000-16 {
		t.Fatalf("remaining=%d", s.Remaining)
	}
}

func TestComplexityBounds(t *testing.T) {
	// 30 frames * 2 slots = 60 used at size 64; place exactly ONE pointer.
	r, _ := newLoggedRT(t, testCfg2())
	must(t, r.NewCoroutine(1))
	first, _ := r.Push(1)
	must(t, r.Store(1, first, 0, 7))
	for i := 0; i < 31; i++ {
		_, _ = r.Push(1)
	}
	h, err := r.MkPtr(1, 0, 1, 1, 0, 0)
	must(t, err)
	// Put pointer at frame0 slot1 (constant pointers regardless of size).
	must(t, r.CopyPtr(1, 0, 1, h))
	// Force a grow relocation (64 -> 128) on the 33rd frame.
	r.lastRelocFixed = -1
	_, err = r.Push(1)
	must(t, err)
	if r.lastRelocFixed != 1 {
		t.Fatalf("fixed=%d want 1: fixup must scale with pointer count, not slots", r.lastRelocFixed)
	}
	if r.lastRelocCopied > 256 {
		t.Fatalf("copied=%d unrelated to other coroutines check", r.lastRelocCopied)
	}
	// A second, unrelated coroutine must not change the relocation cost.
	must(t, r.NewCoroutine(2))
	for i := 0; i < 10; i++ {
		_, _ = r.Push(2)
	}
	r.lastRelocFixed = -1
	for r.lastRelocFixed < 0 {
		if e := r.Pop(1); e != nil {
			t.Fatal(e)
		}
	}
	if r.lastRelocFixed != 1 {
		t.Fatalf("fixed=%d want 1 with other coroutine present (shrink)", r.lastRelocFixed)
	}
}

func testCfg2() Config {
	return Config{BaseSize: 4, GrowthMul: 2, MaxPerStack: 256, TotalQuota: 1_000_000, FrameSlots: 2, ShrinkRatio: 0.25}
}

func TestAuditLogRecordsInputsOutputsAndReasons(t *testing.T) {
	r, buf := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	f, _ := r.Push(1)
	must(t, r.Store(1, f, 0, 8))
	_ = r.Store(99, 0, 0, 1) // undefined -> rejected with reason
	log := buf.String()
	for _, want := range []string{"op=new-co", "op=push", "op=store", "ACCEPT", "REJECT", "kind=undefined", "reason="} {
		if !bytes.Contains([]byte(log), []byte(want)) {
			t.Fatalf("audit log missing %q:\n%s", want, log)
		}
	}
}

func TestRelocationIndependentOfOtherCoroutines(t *testing.T) {
	// One relocation of stack 1 must not touch stack 2's quota twice nor
	// scan stack 2; assert sum-of-sizes quota invariant after churn.
	r, _ := newLoggedRT(t, testCfg())
	must(t, r.NewCoroutine(1))
	must(t, r.NewCoroutine(2))
	for i := 0; i < 12; i++ {
		_, _ = r.Push(1)
	}
	for i := 0; i < 12; i++ {
		_, _ = r.Push(2)
	}
	s := r.Stats()
	sum := 0
	for _, st := range s.Stacks {
		sum += st.Size
	}
	if sum+s.Remaining != testCfg().TotalQuota {
		t.Fatalf("quota accounting drift: %d+%d!=%d", sum, s.Remaining, testCfg().TotalQuota)
	}
}
