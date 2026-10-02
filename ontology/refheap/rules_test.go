package refheap

import (
	"reflect"
	"testing"
)

func codeOf(err error) ErrorCode {
	if err == nil {
		return 0
	}
	return err.(*HeapError).Code
}

func assertErr(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if codeOf(err) != want {
		t.Fatalf("error code = %d (%v), want %d", codeOf(err), err, want)
	}
}

func mustOKInt(err error) {
	if err != nil {
		panic(err)
	}
}

// A reference object does not transmit strong reachability to its target.
func TestReferenceDoesNotTransmit(t *testing.T) {
	for _, k := range []RefKind{Soft, Weak, Phantom} {
		h, _ := NewHeap(1000, 100, 100)
		q := h.CreateQueue()
		target := mustOK(h.Alloc(10, 0, false))
		r := mustOK(h.NewRef(k, target, q, 5, 0))
		mustOKInt(h.SetRoot(r))
		res, err := h.Collect(1_000_000)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(res.Reclaimed, []int64{target}) {
			t.Fatalf("kind %d: reclaimed %v", k, res.Reclaimed)
		}
		g, _ := h.Get(r, 1_000_000)
		if g != 0 {
			t.Fatalf("kind %d: target not cleared: %d", k, g)
		}
		p, _ := h.Poll(q)
		if p != r {
			t.Fatalf("kind %d: queue got %d want %d", k, p, r)
		}
	}
}

// An unreachable reference object is neither processed nor enqueued.
func TestUnreachableReferenceIgnored(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	target := mustOK(h.Alloc(10, 0, false))
	r := mustOK(h.NewRef(Weak, target, q, 5, 0))
	res, err := h.Collect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Reclaimed, []int64{target, r}) {
		t.Fatalf("reclaimed = %v", res.Reclaimed)
	}
	if p, _ := h.Poll(q); p != 0 {
		t.Fatalf("unreachable ref enqueued: %d", p)
	}
}

// Soft window boundary: equality retains, one more clears. With C=100,
// U=10, M=5 and a 5-byte rooted soft ref, used0=5, free=95, window=45.
func TestSoftThresholdEquality(t *testing.T) {
	setup := func() (*Heap, int64, int64) {
		h, _ := NewHeap(100, 10, 5)
		target := mustOK(h.Alloc(50, 0, false))
		r := mustOK(h.NewRef(Soft, target, 0, 5, 0))
		mustOKInt(h.SetRoot(r))
		return h, r, target
	}

	h, r, target := setup()
	res, _ := h.Collect(45)
	if len(res.SoftCleared) != 0 || len(res.Reclaimed) != 0 {
		t.Fatalf("at boundary should retain: %+v", res)
	}
	res, _ = h.Collect(46)
	if !reflect.DeepEqual(res.SoftCleared, []int64{r}) ||
		!reflect.DeepEqual(res.Reclaimed, []int64{target}) {
		t.Fatalf("just past boundary should clear: %+v", res)
	}

	h, r, target = setupM0()
	res, _ = h.Collect(0)
	if len(res.SoftCleared) != 0 {
		t.Fatalf("M=0, now==ts should retain: %+v", res)
	}
	res, _ = h.Collect(1)
	if !reflect.DeepEqual(res.SoftCleared, []int64{r}) ||
		!reflect.DeepEqual(res.Reclaimed, []int64{target}) {
		t.Fatalf("M=0, now>ts should clear: %+v", res)
	}
}

func setupM0() (*Heap, int64, int64) {
	h, _ := NewHeap(100, 10, 0)
	target := mustOK(h.Alloc(50, 0, false))
	r := mustOK(h.NewRef(Soft, target, 0, 5, 0))
	mustOKInt(h.SetRoot(r))
	return h, r, target
}

// free is frozen at soft-phase start: retaining earlier targets must not
// shrink the window applied to later soft refs, and retained targets mark
// field chains so downstream weak refs survive.
func TestSoftFreeFrozenAndFieldPropagation(t *testing.T) {
	h, _ := NewHeap(100, 10, 5)
	q := h.CreateQueue()
	a := mustOK(h.Alloc(40, 1, false))
	b := mustOK(h.Alloc(10, 0, false))
	mustOKInt(h.SetField(a, 0, b))
	other := mustOK(h.Alloc(10, 0, false))
	r1 := mustOK(h.NewRef(Soft, a, 0, 5, 0))
	r2 := mustOK(h.NewRef(Soft, other, 0, 5, 0))
	rw := mustOK(h.NewRef(Weak, b, q, 5, 0))
	mustOKInt(h.SetRoot(r1))
	mustOKInt(h.SetRoot(r2))
	mustOKInt(h.SetRoot(rw))
	// used0 = 15 (three refs), free = 85, window = 40. If free were
	// recomputed after retaining a (40 bytes) the window would become 20 and
	// r2 (age 30) would wrongly clear. a marks b, so rw must survive too.
	res, _ := h.Collect(30)
	if len(res.SoftCleared) != 0 || len(res.WeakCleared) != 0 || len(res.Reclaimed) != 0 {
		t.Fatalf("unexpected clears/reclaims: %+v", res)
	}
}

// Weak references clear in phase 3 even though the target is resurrected in
// phase 4 of the same round.
func TestWeakClearedBeforeResurrection(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	x := mustOK(h.Alloc(10, 0, true))
	rw := mustOK(h.NewRef(Weak, x, q, 5, 0))
	mustOKInt(h.SetRoot(rw))
	res, _ := h.Collect(0)
	if !reflect.DeepEqual(res.WeakCleared, []int64{rw}) ||
		!reflect.DeepEqual(res.Finalized, []int64{x}) ||
		len(res.Reclaimed) != 0 {
		t.Fatalf("phase ordering wrong: %+v", res)
	}
	if g, _ := h.Get(rw, 0); g != 0 {
		t.Fatalf("weak target must stay cleared after resurrection: %d", g)
	}
}

// A phantom to a resurrected object is not enqueued this round, but enqueues
// on the next round after finalization and death.
func TestPhantomResurrectionThenNextRound(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	x := mustOK(h.Alloc(10, 0, true))
	rp := mustOK(h.NewRef(Phantom, x, q, 5, 0))
	mustOKInt(h.SetRoot(rp))
	res, _ := h.Collect(0)
	if len(res.CatchAll) != 0 || !reflect.DeepEqual(res.Finalized, []int64{x}) {
		t.Fatalf("phantom must survive resurrection round: %+v", res)
	}
	if p, _ := h.Poll(q); p != 0 {
		t.Fatalf("phantom enqueued early: %d", p)
	}
	fz, _ := h.Finalize(10)
	if !reflect.DeepEqual(fz, []int64{x}) {
		t.Fatalf("finalize = %v", fz)
	}
	res, _ = h.Collect(0)
	if !reflect.DeepEqual(res.CatchAll, []int64{rp}) ||
		!reflect.DeepEqual(res.Reclaimed, []int64{x}) {
		t.Fatalf("phantom should clear in round two: %+v", res)
	}
}

// A reference object pulled back by finalization (via a field) is handled by
// the phase-5 catch-all.
func TestResurrectedReferenceCaughtInPhase5(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	y := mustOK(h.Alloc(10, 0, false))
	rp := mustOK(h.NewRef(Phantom, y, q, 5, 0))
	x := mustOK(h.Alloc(10, 1, true))
	mustOKInt(h.SetField(x, 0, rp))
	res, _ := h.Collect(100)
	if !reflect.DeepEqual(res.Finalized, []int64{x}) {
		t.Fatalf("finalized = %v", res.Finalized)
	}
	if !reflect.DeepEqual(res.CatchAll, []int64{rp}) {
		t.Fatalf("phase5 should clear resurrected ref: %+v", res)
	}
	if !reflect.DeepEqual(res.Reclaimed, []int64{y}) {
		t.Fatalf("only y should be reclaimed: %v", res.Reclaimed)
	}
	if p, _ := h.Poll(q); p != rp {
		t.Fatalf("queue = %d", p)
	}
}

// Lower ids are processed first within a phase; the queue preserves that.
func TestPhaseOrderingAndFIFO(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	var refs []int64
	for i := 0; i < 4; i++ {
		target := mustOK(h.Alloc(10, 0, false))
		r := mustOK(h.NewRef(Weak, target, q, 5, 0))
		refs = append(refs, r)
		mustOKInt(h.SetRoot(r))
	}
	res, _ := h.Collect(100)
	if !reflect.DeepEqual(res.WeakCleared, refs) {
		t.Fatalf("weak order = %v want %v", res.WeakCleared, refs)
	}
	var polled []int64
	for {
		p, _ := h.Poll(q)
		if p == 0 {
			break
		}
		polled = append(polled, p)
	}
	if !reflect.DeepEqual(polled, refs) {
		t.Fatalf("queue order = %v", polled)
	}
}

// A queueless reference is cleared but never enqueued.
func TestNoQueueClearsOnly(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	target := mustOK(h.Alloc(10, 0, false))
	r := mustOK(h.NewRef(Weak, target, 0, 5, 0))
	mustOKInt(h.SetRoot(r))
	res, _ := h.Collect(100)
	if !reflect.DeepEqual(res.WeakCleared, []int64{r}) {
		t.Fatalf("cleared = %v", res.WeakCleared)
	}
	if p, _ := h.Poll(q); p != 0 {
		t.Fatalf("queueless ref appeared in a queue: %d", p)
	}
	if g, _ := h.Get(r, 100); g != 0 {
		t.Fatalf("target = %d", g)
	}
}

// Get refreshes only soft-reference timestamps.
func TestGetRefreshesSoftOnly(t *testing.T) {
	h, _ := NewHeap(100, 10, 5)
	target := mustOK(h.Alloc(50, 0, false))
	rs := mustOK(h.NewRef(Soft, target, 0, 5, 0))
	mustOKInt(h.SetRoot(rs))
	if g, _ := h.Get(rs, 40); g != target {
		t.Fatalf("get = %d", g)
	}
	res, _ := h.Collect(44) // refreshed age 4 <= window 45
	if len(res.SoftCleared) != 0 {
		t.Fatalf("refreshed soft ref wrongly cleared: %+v", res)
	}

	h2, _ := NewHeap(1000, 10, 5)
	t2 := mustOK(h2.Alloc(10, 0, false))
	rw := mustOK(h2.NewRef(Weak, t2, 0, 5, 0))
	rp := mustOK(h2.NewRef(Phantom, t2, 0, 5, 0))
	mustOKInt(h2.SetRoot(rw))
	mustOKInt(h2.SetRoot(rp))
	if g, _ := h2.Get(rw, 7); g != t2 {
		t.Fatalf("weak get = %d", g)
	}
	if g, _ := h2.Get(rp, 7); g != t2 {
		t.Fatalf("phantom get = %d", g)
	}
}
