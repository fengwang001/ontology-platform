package refheap

import (
	"reflect"
	"testing"
)

func mustOK[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func eqInt64(t *testing.T, name string, got, want []int64) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// Worked example transcribed from the specification.
func TestSpecWorkedExample(t *testing.T) {
	h, err := NewHeap(100, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	q := h.CreateQueue()
	if q != 1 {
		t.Fatalf("queue = %d", q)
	}
	o1 := mustOK(h.Alloc(10, 0, false))
	o2 := mustOK(h.Alloc(20, 1, false))
	o3 := mustOK(h.Alloc(30, 0, true))
	if err := h.SetField(o2, 0, o3); err != nil {
		t.Fatal(err)
	}
	o4 := mustOK(h.Alloc(10, 0, false))
	r5 := mustOK(h.NewRef(Weak, o2, q, 5, 0))
	r6 := mustOK(h.NewRef(Soft, o4, q, 5, 0))
	r7 := mustOK(h.NewRef(Phantom, o3, q, 5, 0))
	for _, id := range []int64{o1, r5, r6, r7} {
		if err := h.SetRoot(id); err != nil {
			t.Fatal(err)
		}
	}
	if h.Used() != 85 {
		t.Fatalf("used = %d", h.Used())
	}

	res, err := h.Collect(7)
	if err != nil {
		t.Fatal(err)
	}
	eqInt64(t, "reclaimed", res.Reclaimed, []int64{o2})
	eqInt64(t, "soft", res.SoftCleared, nil)
	eqInt64(t, "weak", res.WeakCleared, []int64{r5})
	eqInt64(t, "catch", res.CatchAll, nil)
	eqInt64(t, "final", res.Finalized, []int64{o3})
	if res.Used != 65 {
		t.Fatalf("used = %d", res.Used)
	}
	if h.LastMarkVisitCount() != 6 { // survivors: 1,3,4,5,6,7
		t.Fatalf("hits = %d", h.LastMarkVisitCount())
	}

	p, err := h.Poll(q)
	if err != nil || p != r5 {
		t.Fatalf("poll = %d,%v", p, err)
	}
	g, err := h.Get(r6, 8)
	if err != nil || g != o4 {
		t.Fatalf("get soft = %d,%v", g, err)
	}
	if g, err = h.Get(r5, 8); err != nil || g != 0 {
		t.Fatalf("get cleared weak = %d,%v", g, err)
	}
	if g, err = h.Get(r7, 8); err != nil || g != o3 {
		t.Fatalf("get phantom = %d,%v", g, err)
	}
	fz, err := h.Finalize(1)
	if err != nil || !reflect.DeepEqual(fz, []int64{o3}) {
		t.Fatalf("finalize = %v,%v", fz, err)
	}

	res, err = h.Collect(9)
	if err != nil {
		t.Fatal(err)
	}
	eqInt64(t, "reclaimed2", res.Reclaimed, []int64{o3})
	eqInt64(t, "catch2", res.CatchAll, []int64{r7})
	if res.Used != 35 {
		t.Fatalf("used2 = %d", res.Used)
	}

	res, err = h.Collect(1000)
	if err != nil {
		t.Fatal(err)
	}
	eqInt64(t, "soft3", res.SoftCleared, []int64{r6})
	eqInt64(t, "reclaimed3", res.Reclaimed, []int64{o4})
	if res.Used != 25 {
		t.Fatalf("used3 = %d", res.Used)
	}
}

// Finalizer chain: both mutually-related finalizable objects are selected in
// one round and stay alive while waiting in the finalization queue.
func TestFinalizerChain(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	root := mustOK(h.Alloc(1, 0, false))
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	o8 := mustOK(h.Alloc(1, 0, true))
	o9 := mustOK(h.Alloc(1, 1, true))
	if err := h.SetField(o9, 0, o8); err != nil {
		t.Fatal(err)
	}
	res, err := h.Collect(0)
	if err != nil {
		t.Fatal(err)
	}
	eqInt64(t, "selected", res.Finalized, []int64{o8, o9})
	eqInt64(t, "reclaimed", res.Reclaimed, nil)

	res, err = h.Collect(0)
	if err != nil {
		t.Fatal(err)
	}
	eqInt64(t, "reclaimed while pending", res.Reclaimed, nil)

	fz, _ := h.Finalize(10)
	eqInt64(t, "finalize", fz, []int64{o8, o9})
	res, err = h.Collect(0)
	if err != nil {
		t.Fatal(err)
	}
	eqInt64(t, "reclaimed after finalize", res.Reclaimed, []int64{o8, o9})
}
