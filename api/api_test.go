package api_test

import (
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/seg"
)

func rec(k string, v int64, op, val string) seg.Record {
	return seg.Record{Key: k, Version: v, Op: op, Val: val}
}
func put(k string, v int64, val string) seg.Record { return rec(k, v, seg.OpPut, val) }
func del(k string, v int64) seg.Record             { return rec(k, v, seg.OpDel, "") }
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func ingest(t *testing.T, st *api.Store, seq ...seg.Record) {
	t.Helper()
	for _, r := range seq {
		must(t, st.Ingest(r))
	}
}
func batch(seq []seg.Record) map[string]string {
	win, out := map[string]seg.Record{}, map[string]string{}
	for _, r := range seq {
		if cur, ok := win[r.Key]; !ok || r.Newer(cur) {
			win[r.Key] = r
		}
	}
	for k, r := range win {
		if !r.Tombstone() {
			out[k] = r.Val
		}
	}
	return out
}
func TestViewMatchesBatchRecompute(t *testing.T) {
	seq := []seg.Record{put("a", 1, "x"), put("b", 2, "y"), put("a", 3, "x2"),
		put("c", 4, "z"), del("b", 5), del("a", 6), put("c", 7, "z2"), put("b", 8, "y2")}
	for v := int64(9); v <= 300; v++ { // generated tail: 7 keys, mixed ops
		k, op, val := string(rune('a'+v%7)), seg.OpPut, fmt.Sprint(v)
		if v%5 == 0 {
			op, val = seg.OpDel, ""
		}
		seq = append(seq, rec(k, v, op, val))
	}
	st := api.New()
	ingest(t, st, seq...)
	if got, want := st.View(), batch(seq); !maps.Equal(got, want) {
		t.Fatalf("view %v != batch recompute %v", got, want)
	}
}
func TestCompactSelfConsistent(t *testing.T) {
	for _, m := range []int{8, 100, 10000} { // m historical puts on key k
		st := api.New()
		for v := int64(1); v <= int64(m); v++ {
			must(t, st.Ingest(put("k", v, fmt.Sprint(v))))
		}
		ingest(t, st, put("a", int64(m)+1, "x"), del("gone", int64(m)+2))
		before := st.View()
		out, err := st.Compact()
		must(t, err)
		if len(out.Records) != 2 || out.Records[0].Key != "a" || out.Records[1].Key != "k" {
			t.Fatalf("m=%d: compacted = %+v, want live a+k", m, out.Records)
		}
		for i, r := range out.Records {
			if r.Tombstone() || (i > 0 && out.Records[i-1].Key >= r.Key) {
				t.Fatalf("m=%d: bad output at %d", m, i)
			}
		}
		if !maps.Equal(st.View(), before) {
			t.Fatalf("m=%d: view changed by compact", m)
		}
	}
}
func TestWatermarkMonotonic(t *testing.T) {
	st := api.New()
	for v := int64(1); v <= 50; v++ {
		must(t, st.Ingest(put("k", v, "x")))
		if st.Watermark() != v {
			t.Fatalf("watermark = %d, want %d", st.Watermark(), v)
		}
	}
	_ = st.Ingest(put("k", 50, "y")) // rejected: not increasing
	if _, err := st.Compact(); err != nil || st.Watermark() != 50 {
		t.Fatalf("after reject+compact: err=%v watermark=%d, want 50", err, st.Watermark())
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	// the four error categories must be distinguishable
	if len(map[error]bool{seg.ErrKey: true, seg.ErrVersion: true, seg.ErrOp: true, seg.ErrVal: true}) != 4 {
		t.Fatal("sentinels not distinct")
	}
	bads := []struct {
		rec  seg.Record
		want error
	}{
		{rec("", 6, "put", "v"), seg.ErrKey}, {rec("d", 0, "put", "v"), seg.ErrVersion},
		{rec("d", 3, "put", "v"), seg.ErrVersion}, // not strictly increasing (max is 3)
		{rec("d", 6, "nop", "v"), seg.ErrOp}, {rec("d", 6, "put", ""), seg.ErrVal},
		{rec("d", 6, "del", "v"), seg.ErrVal},
	}
	st := api.New()
	ingest(t, st, put("a", 1, "x"), del("b", 2), put("c", 3, "z"))
	wm, n := st.Watermark(), len(st.View())
	for _, tc := range bads {
		if err := st.Ingest(tc.rec); !errors.Is(err, tc.want) {
			t.Fatalf("ingest %+v: err = %v, want %v", tc.rec, err, tc.want)
		}
		if st.Watermark() != wm || len(st.View()) != n {
			t.Fatalf("rejected %+v mutated state", tc.rec)
		}
	}
	must(t, st.Ingest(put("d", 6, "ok"))) // store still usable
	must(t, api.New().SelfCheck())
}
func TestConcurrentReaders(t *testing.T) {
	st := api.New()
	ingest(t, st, put("a", 1, "x"), del("a", 2), put("b", 3, "y"))
	_, err := st.Compact()
	must(t, err)
	wantView, wantMark := st.View(), st.Watermark()
	var wg sync.WaitGroup
	var drift atomic.Bool
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if !maps.Equal(st.View(), wantView) || st.Watermark() != wantMark || st.SelfCheck() != nil {
					drift.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if drift.Load() {
		t.Fatal("concurrent read drifted")
	}
}
