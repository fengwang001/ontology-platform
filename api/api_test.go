package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/comp"
	"ontology/seg"
)

func put(k string, v int64, val string) seg.Rec {
	return seg.Rec{Key: k, Version: v, Op: seg.OpPut, Val: val}
}
func del(k string, v int64) seg.Rec { return seg.Rec{Key: k, Version: v, Op: seg.OpDel} }

func eightRecs() []seg.Rec {
	return []seg.Rec{put("a", 1, "x"), put("b", 2, "y"), put("a", 3, "x2"),
		put("c", 4, "z"), del("b", 5), del("a", 6), put("c", 7, "z2"), put("b", 8, "y2")}
}

func loaded(t *testing.T) *api.DB {
	t.Helper()
	d := api.New()
	for _, r := range eightRecs() {
		if err := d.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func batch(recs []seg.Rec) map[string]string {
	win := map[string]seg.Rec{}
	for _, r := range recs {
		if w, ok := win[r.Key]; !ok || r.Version > w.Version {
			win[r.Key] = r
		}
	}
	out := map[string]string{}
	for k, r := range win {
		if r.Op != seg.OpDel {
			out[k] = r.Val
		}
	}
	return out
}

// TestViewMatchesBatch pins invariant 1: View == batch recompute at every prefix.
func TestViewMatchesBatch(t *testing.T) {
	cases := map[string][]seg.Rec{
		"section3": eightRecs(),
		"only del": {put("a", 1, "x"), del("a", 2)},
		"reborn":   {put("a", 1, "x"), del("a", 2), put("a", 3, "z")},
	}
	for name, recs := range cases {
		d := api.New()
		var seen []seg.Rec
		for i, r := range recs {
			if err := d.Ingest(r); err != nil {
				t.Fatalf("%s step %d: %v", name, i, err)
			}
			seen = append(seen, r)
			if got := d.View(); !reflect.DeepEqual(got, batch(seen)) {
				t.Errorf("%s step %d: view=%v want %v", name, i, got, batch(seen))
			}
		}
	}
}

func TestWatermarkMonotone(t *testing.T) {
	d := api.New()
	var prev int64
	for i, r := range eightRecs() {
		if err := d.Ingest(r); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if w := d.Watermark(); w < prev || w != r.Version {
			t.Errorf("step %d: wm=%d prev=%d want %d", i, w, prev, r.Version)
		}
		prev = d.Watermark()
	}
	out, err := d.Compact([]*comp.Segment{{Records: eightRecs()}})
	if err != nil || out.Watermark != 8 || d.Watermark() != 8 {
		t.Fatalf("after compact: out=%v db wm=%d", out, d.Watermark())
	}
}

func TestRejectLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		r    seg.Rec
		want error
	}{
		{"empty key", put("", 9, "q"), seg.ErrEmptyKey},
		{"old version", put("a", 3, "q"), api.ErrBadVersion},
		{"non-positive", put("a", 0, "q"), api.ErrBadVersion},
		{"bad op", seg.Rec{Key: "e", Version: 9, Op: "patch", Val: "q"}, seg.ErrBadOp},
		{"put no val", put("e", 9, ""), seg.ErrBadVal},
		{"del with val", seg.Rec{Key: "e", Version: 9, Op: seg.OpDel, Val: "q"}, seg.ErrBadVal},
	}
	for _, tc := range cases {
		d := loaded(t)
		v0, w0 := d.View(), d.Watermark()
		if err := d.Ingest(tc.r); !errors.Is(err, tc.want) {
			t.Errorf("%s: err=%v want %v", tc.name, err, tc.want)
		}
		if d.Watermark() != w0 || !reflect.DeepEqual(d.View(), v0) {
			t.Errorf("%s: state changed view=%v wm=%d want %v wm=%d", tc.name, d.View(), d.Watermark(), v0, w0)
		}
		if err := d.Ingest(put("d", 9, "q")); err != nil {
			t.Errorf("%s: unusable after rejection: %v", tc.name, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentReaders(t *testing.T) {
	d := loaded(t)
	if _, err := d.Compact([]*comp.Segment{{Records: eightRecs()}}); err != nil {
		t.Fatal(err)
	}
	const n = 32
	var wg sync.WaitGroup
	views := make([]map[string]string, n)
	wms := make([]int64, n)
	start := make(chan struct{})
	for i := range views {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			views[i], wms[i] = d.View(), d.Watermark()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(views[i], views[0]) || wms[i] != wms[0] {
			t.Fatalf("reader %d differs: %v/%d vs %v/%d", i, views[i], wms[i], views[0], wms[0])
		}
	}
}
