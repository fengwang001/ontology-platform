package mig

import (
	"errors"
	"reflect"
	"testing"

	"ontology/keyenc"
)

func mustPut(t *testing.T, h *Hybrid, k, v int64) {
	t.Helper()
	if err := h.Put(k, v); err != nil {
		t.Fatalf("Put(%d,%d): %v", k, v, err)
	}
}

func scanMap(scan []KV) map[int64]int64 {
	m := make(map[int64]int64, len(scan))
	for _, e := range scan {
		m[e.Key] = e.Value
	}
	return m
}

func scanKeys(scan []KV) []int64 {
	ks := make([]int64, len(scan))
	for i, e := range scan {
		ks[i] = e.Key
	}
	return ks
}

func checkCounterInvariant(t *testing.T, h *Hybrid) {
	t.Helper()
	phys, logicalTotal, lastLogical, moved, probed := h.counters()
	t.Logf("判定依据: 累计Scan物理产出=%d 累计返回条数=%d 本次返回=%d; 最近Step搬迁=%d 探测=%d",
		phys, logicalTotal, lastLogical, moved, probed)
	if phys != logicalTotal {
		t.Fatalf("scan physical outputs %d != total returned %d", phys, logicalTotal)
	}
	if moved > 0 && probed > 2*moved {
		t.Fatalf("step probed %d > 2*moved %d", probed, moved)
	}
}

// Example one: {-2:a,1:b,5:c}, Step(1) moves -2 even though its physical
// order in A puts it last; scan splits the A negative/non-negative halves.
func TestExampleOneStepAndScan(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, -2, 1)
	mustPut(t, h, 1, 2)
	mustPut(t, h, 5, 3)

	moved, err := h.Step(1)
	if err != nil || moved != 1 || h.Watermark() != -1 {
		t.Fatalf("Step(1) = %d,w=%d,err=%v", moved, h.Watermark(), err)
	}
	if v, ok, _ := h.Get(-2); !ok || v != 1 {
		t.Fatalf("Get(-2) after migration = %d,%v", v, ok)
	}
	scan, err := h.Scan(-5, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scanKeys(scan), []int64{-2, 1, 5}) {
		t.Fatalf("scan order = %v", scanKeys(scan))
	}
	if got := scanMap(scan); got[-2] != 1 || got[1] != 2 || got[5] != 3 {
		t.Fatalf("scan values = %v", got)
	}
	checkCounterInvariant(t, h)
}

// Example two: negative segment [E1(-3), +inf) must not use E1(0) as high.
func TestExampleTwoNegativeInfinityScan(t *testing.T) {
	h := NewHybrid()
	for _, k := range []int64{-3, -1, 0, 2} {
		mustPut(t, h, k, k)
	}
	scan, err := h.Scan(-3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scanKeys(scan), []int64{-3, -1}) {
		t.Fatalf("[-3,0) = %v, want [-3 -1]", scanKeys(scan))
	}
	checkCounterInvariant(t, h)
}

// Example three: w=3 routes boundary keys correctly; a later Step only moves
// w to moved-key+1, so a fresh key on A keeps A non-empty until re-Step.
func TestExampleThreeRoutingAndFinish(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, -5, 0)
	mustPut(t, h, 0, 0)
	mustPut(t, h, 1, 1)
	mustPut(t, h, 2, 2)
	moved, _ := h.Step(10000)
	if moved != 4 || h.Watermark() != 3 {
		t.Fatalf("drain = %d, w=%d want 4,3", moved, h.Watermark())
	}
	mustPut(t, h, 2, 99) // k < w -> B overwrite
	mustPut(t, h, 3, 30) // k >= w -> A
	if existed, err := h.Delete(2); err != nil || !existed {
		t.Fatalf("Delete(2) on B = %v,%v", existed, err)
	}
	if err := h.Finish(); !errors.Is(err, ErrNotDrained) {
		t.Fatalf("Finish with non-empty A err = %v", err)
	}
	moved, _ = h.Step(1) // moves 3, w=4
	if moved != 1 {
		t.Fatalf("Step = %d want 1", moved)
	}
	if err := h.Finish(); err != nil {
		t.Fatalf("Finish after drain: %v", err)
	}
	if h.Watermark() != keyenc.MaxKey+1 {
		t.Fatalf("w after Finish = %d", h.Watermark())
	}
	mustPut(t, h, 100, 7) // post-Finish everything routes to B
	if v, ok, _ := h.Get(100); !ok || v != 7 {
		t.Fatalf("post-Finish Put/Get = %d,%v", v, ok)
	}
	scan, _ := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)
	if !reflect.DeepEqual(scanKeys(scan), []int64{-5, 0, 1, 3, 100}) {
		t.Fatalf("post-Finish full scan = %v", scanKeys(scan))
	}
	checkCounterInvariant(t, h)
}

func TestMixedSignScanOrderAndBoundaries(t *testing.T) {
	h := NewHybrid()
	for _, k := range []int64{-7, -1, 0, 4} {
		mustPut(t, h, k, k*10)
	}
	scan, err := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)
	if err != nil || !reflect.DeepEqual(scanKeys(scan), []int64{-7, -1, 0, 4}) {
		t.Fatalf("full scan = %v, err=%v", scanKeys(scan), err)
	}
	scan, _ = h.Scan(-7, 0) // hi == 0: negative infinity segment
	if !reflect.DeepEqual(scanKeys(scan), []int64{-7, -1}) {
		t.Fatalf("[-7,0) = %v", scanKeys(scan))
	}
	scan, _ = h.Scan(0, 5) // lo == 0: non-negative segment
	if !reflect.DeepEqual(scanKeys(scan), []int64{0, 4}) {
		t.Fatalf("[0,5) = %v", scanKeys(scan))
	}
	// w reaches 0 (after moving -7,-1,0...): lo == w and hi == w boundaries.
	if moved, _ := h.Step(2); moved != 2 || h.Watermark() != 0 {
		t.Fatalf("Step moved %d w %d", moved, h.Watermark())
	}
	scan, _ = h.Scan(0, keyenc.MaxKey+1)
	if !reflect.DeepEqual(scanKeys(scan), []int64{0, 4}) {
		t.Fatalf("A segment [0,+inf) = %v", scanKeys(scan))
	}
	scan, _ = h.Scan(keyenc.MinKey, 0)
	if !reflect.DeepEqual(scanKeys(scan), []int64{-7, -1}) {
		t.Fatalf("B segment [-inf,0) = %v", scanKeys(scan))
	}
	scan, _ = h.Scan(0, 0)
	if len(scan) != 0 {
		t.Fatalf("empty scan produced %d rows", len(scan))
	}
	checkCounterInvariant(t, h)
}

func TestBoundaryOwnershipWMinus1AndW(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, 1, 10)
	mustPut(t, h, 2, 20)
	mustPut(t, h, 3, 30)
	if moved, _ := h.Step(2); moved != 2 || h.Watermark() != 3 {
		t.Fatalf("Step = %d, w=%d", moved, h.Watermark())
	}
	mustPut(t, h, 2, 200) // w-1 -> B overwrite
	if v, ok, _ := h.Get(2); !ok || v != 200 {
		t.Fatalf("w-1 on B: %d,%v", v, ok)
	}
	if existed, _ := h.Delete(2); !existed {
		t.Fatal("Delete(w-1) must hit B")
	}
	if _, ok, _ := h.Get(2); ok {
		t.Fatal("w-1 still visible after B delete")
	}
	if v, ok, _ := h.Get(3); !ok || v != 30 {
		t.Fatalf("w on A: %d,%v", v, ok)
	}
	if existed, _ := h.Delete(3); !existed {
		t.Fatal("Delete(w) must hit A")
	}
}

func TestStepPartialTwoResidueInvisibleAndRecover(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, -2, 1)
	mustPut(t, h, 1, 2)
	mustPut(t, h, 5, 3)
	if _, err := h.Step(1); err != nil {
		t.Fatal(err)
	}
	if err := h.StepPartial(2); err != nil {
		t.Fatalf("StepPartial(2): %v", err)
	}
	if !h.Crashed() || h.Watermark() != 2 {
		t.Fatalf("after partial2: crashed=%v w=%d", h.Crashed(), h.Watermark())
	}
	if v, ok, _ := h.Get(1); !ok || v != 2 {
		t.Fatalf("Get(1) in crash = %d,%v", v, ok)
	}
	scan, _ := h.Scan(0, 6)
	if !reflect.DeepEqual(scanKeys(scan), []int64{1, 5}) {
		t.Fatalf("crash scan = %v want [1 5]", scanKeys(scan))
	}
	checkCounterInvariant(t, h)
	bd, ad, err := h.Recover()
	if err != nil || bd != 0 || ad != 1 {
		t.Fatalf("Recover = (%d,%d),%v want (0,1)", bd, ad, err)
	}
	if h.Crashed() {
		t.Fatal("still crashed after Recover")
	}
	if v, ok, _ := h.Get(1); !ok || v != 2 {
		t.Fatalf("Get(1) after roll-forward = %d,%v", v, ok)
	}
	if _, _, err := h.Recover(); !errors.Is(err, ErrNotCrashed) {
		t.Fatalf("Recover twice = %v", err)
	}
}

func TestStepPartialOneResidueInvisibleAndRecover(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, 1, 2)
	mustPut(t, h, 5, 3)
	if err := h.StepPartial(1); err != nil {
		t.Fatalf("StepPartial(1): %v", err)
	}
	if !h.Crashed() || h.Watermark() != keyenc.MinKey {
		t.Fatalf("after partial1: crashed=%v w=%d", h.Crashed(), h.Watermark())
	}
	if v, ok, _ := h.Get(1); !ok || v != 2 {
		t.Fatalf("Get(1) p=1 = %d,%v", v, ok)
	}
	scan, _ := h.Scan(0, 6)
	if !reflect.DeepEqual(scanKeys(scan), []int64{1, 5}) {
		t.Fatalf("p=1 scan = %v", scanKeys(scan))
	}
	checkCounterInvariant(t, h)
	bd, ad, err := h.Recover()
	if err != nil || bd != 1 || ad != 0 {
		t.Fatalf("Recover = (%d,%d),%v want (1,0)", bd, ad, err)
	}
	if v, ok, _ := h.Get(1); !ok || v != 2 {
		t.Fatalf("Get(1) after rollback = %d,%v", v, ok)
	}
}

func TestCrashedStateRejectsMutations(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, 1, 1)
	if err := h.StepPartial(1); err != nil {
		t.Fatal(err)
	}
	for _, act := range []func() error{
		func() error { return h.Put(2, 2) },
		func() error { _, e := h.Delete(1); return e },
		func() error { _, e := h.Step(1); return e },
		func() error { return h.StepPartial(1) },
		func() error { return h.Finish() },
	} {
		if err := act(); !errors.Is(err, ErrCrashed) {
			t.Fatalf("mutation in crash err = %v want ErrCrashed", err)
		}
	}
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	h := NewHybrid()
	mustPut(t, h, 1, 1)
	mustPut(t, h, 2, 2)
	if moved, err := h.Step(10000); err != nil || moved != 2 {
		t.Fatalf("drain = %d,%v", moved, err)
	}
	if _, err := h.Step(1); err != nil || h.Crashed() {
		t.Fatalf("empty Step err=%v crashed=%v (want moved 0, no crash)", err, h.Crashed())
	}
	if err := h.StepPartial(1); !errors.Is(err, ErrDrained) {
		t.Fatalf("empty StepPartial = %v want ErrDrained", err)
	}
	if h.Crashed() {
		t.Fatal("ErrDrained must not enter crashed state")
	}
	w := h.Watermark()
	before, _ := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)

	// Invalid arguments: key/value/n/p/lo/hi out of range, lo > hi.
	if err := h.Put(keyenc.MinKey-1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Put bad key = %v", err)
	}
	if err := h.Put(0, 1_000_000_001); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Put bad value = %v", err)
	}
	if _, err := h.Delete(keyenc.MaxKey + 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Delete bad key = %v", err)
	}
	if _, _, err := h.Get(1 << 40); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Get bad key = %v", err)
	}
	if _, err := h.Step(0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Step(0) = %v", err)
	}
	if _, err := h.Step(10_001); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Step(10001) = %v", err)
	}
	if err := h.StepPartial(3); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("StepPartial(3) = %v", err)
	}
	if _, err := h.Scan(-3, -4); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Scan lo>hi = %v", err)
	}
	if _, err := h.Scan(keyenc.MinKey-1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Scan bad lo = %v", err)
	}
	if _, err := h.Scan(0, keyenc.MaxKey+2); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Scan bad hi = %v", err)
	}

	if h.Watermark() != w {
		t.Fatalf("watermark changed by rejections: %d != %d", h.Watermark(), w)
	}
	after, _ := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("state changed by rejections: before=%v after=%v", before, after)
	}

	// Invalid argument beats crashed state in rejection order.
	h2 := NewHybrid()
	mustPut(t, h2, 0, 0)
	if err := h2.StepPartial(1); err != nil {
		t.Fatal(err)
	}
	if err := h2.Put(keyenc.MaxKey+1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("bad arg must beat crashed: %v", err)
	}
	if _, err := h2.Scan(keyenc.MinKey-1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("bad scan arg must beat crashed: %v", err)
	}
	// But Get/Scan with valid args still work while crashed.
	if v, ok, err := h2.Get(0); err != nil || !ok || v != 0 {
		t.Fatalf("Get while crashed = %d,%v,%v", v, ok, err)
	}
}
