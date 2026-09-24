package align

import (
	"errors"
	"fmt"
	"maps"
	"ontology/chanbuf"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func pushAll(t *testing.T, a *Align, seq []Item, bs int) {
	t.Helper()
	for i := 0; i < len(seq); i += bs {
		if _, err := a.Push(seq[i:min(i+bs, len(seq))]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotMatchesCut(t *testing.T) {
	for _, bs := range []int{1, 7, 1 << 20} {
		for _, seq := range genSeqs() {
			a := New(1 << 20)
			pushAll(t, a, seq, bs)
			for n := int64(1); n <= barCount(seq); n++ {
				if got, ok := a.Snapshot(n); !ok || !maps.Equal(got, CutReference(seq, n)) {
					t.Errorf("bs=%d n=%d: %v != cut", bs, n, got)
				}
			}
		}
	}
}

func TestPerChannelOrder(t *testing.T) {
	for _, seq := range genSeqs() {
		a := New(1 << 20)
		pushAll(t, a, seq, 1)
		var want, out [2][]Item
		for _, it := range recs(seq) {
			want[it.Ch] = append(want[it.Ch], it)
		}
		sumBefore, n := map[string]int64{}, int64(0)
		for _, o := range a.st.out {
			if o.Kind == Record {
				out[o.Ch] = append(out[o.Ch], o)
				sumBefore[o.Key] += o.Val
				continue
			}
			if n++; !maps.Equal(sumBefore, CutReference(seq, n)) {
				t.Errorf("records before barrier %d != cut", n)
			}
		}
		for c := range 2 {
			if got := append(out[c], recs(a.st.ch[c].Items())...); !slices.Equal(got, want[c]) {
				t.Errorf("channel %d: out+buffered != arrival order", c)
			}
		}
	}
}

func TestNoLossNoDup(t *testing.T) {
	for _, seq := range genSeqs() {
		a := New(1 << 20)
		pushAll(t, a, seq, 13)
		sumOut := map[string]int64{}
		for _, o := range recs(a.st.out) {
			sumOut[o.Key] += o.Val
		}
		n := len(recs(a.st.out)) + len(recs(a.st.ch[0].Items())) + len(recs(a.st.ch[1].Items()))
		if n != len(recs(seq)) || !maps.Equal(a.State(), sumOut) {
			t.Errorf("out+buf %d != arrived %d, or state != sum(out)", n, len(recs(seq)))
		}
	}
}

func TestRejectLeavesNoTrace(t *testing.T) {
	type tc struct {
		batch []Item
		want  error
	}
	table := map[string]tc{
		"bad channel":   {[]Item{{Ch: 2, Kind: Record, Key: "x"}}, ErrBadElem},
		"empty key":     {[]Item{{Ch: 1, Kind: Record}}, ErrBadElem},
		"unknown kind":  {[]Item{{Ch: 1, Kind: chanbuf.Kind(9)}}, ErrBadElem},
		"barrier order": {[]Item{{Ch: 1, Kind: Barrier, ID: 7}}, ErrBarrierOrder},
		"buffer full":   {[]Item{{Ch: 0, Kind: Record, Key: "b"}, {Ch: 0, Kind: Record, Key: "c"}}, ErrBufferFull},
		"mid-batch":     {[]Item{{Ch: 1, Kind: Record, Key: "ok"}, {Ch: 1, Kind: Record}}, ErrBadElem},
	}
	for name, c := range table {
		a := New(1)
		pushAll(t, a, []Item{{Ch: 0, Kind: Record, Key: "a", Val: 1}, {Ch: 0, Kind: Barrier, ID: 1}}, 1)
		trace := func() string {
			return fmt.Sprint(a.State(), a.st.out, a.st.ch[0].Items(), a.st.ch[1].Items(), len(a.st.snap))
		}
		before := trace()
		if _, err := a.Push(c.batch); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if before != trace() {
			t.Errorf("%s: rejected batch left a trace", name)
		}
		if _, err := a.Push([]Item{{Ch: 1, Kind: Record, Key: "z", Val: 5}}); err != nil {
			t.Errorf("%s: unusable after rejection: %v", name, err)
		}
	}
	if errors.Is(ErrBadElem, ErrBarrierOrder) || errors.Is(ErrBadElem, ErrBufferFull) ||
		errors.Is(ErrBarrierOrder, ErrBufferFull) || errors.Is(ErrBarrierOrder, ErrBadElem) ||
		errors.Is(ErrBufferFull, ErrBadElem) || errors.Is(ErrBufferFull, ErrBarrierOrder) {
		t.Error("sentinel errors not mutually distinct")
	}
}

func TestCheckedScaling(t *testing.T) {
	if err := checkScaling(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentReads(t *testing.T) {
	a := New(1 << 20)
	pushAll(t, a, genSeqs()[3], 37)
	wantState, wantSnap := a.State(), a.st.snap[1]
	if wantSnap == nil {
		t.Fatal("no alignment")
	}
	var bad atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s, _ := a.Snapshot(1)
				if !maps.Equal(a.State(), wantState) || !maps.Equal(s, wantSnap) {
					bad.Add(1)
				}
			}
			if SelfCheck() != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d inconsistent reads", bad.Load())
	}
}
