// Demo for the snapshot-compaction store: prints one OK/FAIL line per
// required property, exits non-zero on any FAIL.
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/seg"
)

var failed atomic.Bool

func ok(pass bool, msg string) {
	if pass {
		fmt.Println("OK " + msg)
	} else {
		fmt.Println("FAIL " + msg)
		failed.Store(true)
	}
}

func viewStr(v map[string]string) string {
	ks := make([]string, 0, len(v))
	for k := range v {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = k + "=" + v[k]
	}
	return strings.Join(parts, ",")
}

func main() {
	// The eight-step scenario from NOTES.md (seg1=v1-5, seg2=v6-8).
	recs := []seg.Record{
		{Key: "a", Version: 1, Op: "put", Val: "x"},
		{Key: "b", Version: 2, Op: "put", Val: "y"},
		{Key: "a", Version: 3, Op: "put", Val: "x2"},
		{Key: "c", Version: 4, Op: "put", Val: "z"},
		{Key: "b", Version: 5, Op: "del"},
		{Key: "a", Version: 6, Op: "del"},
		{Key: "c", Version: 7, Op: "put", Val: "z2"},
		{Key: "b", Version: 8, Op: "put", Val: "y2"},
	}
	wantSteps := []string{
		"a=x", "a=x,b=y", "a=x2,b=y", "a=x2,b=y,c=z",
		"a=x2,c=z", "c=z", "c=z2", "b=y2,c=z2",
	}
	st := api.New()
	steps := make([]string, 0, len(recs))
	stepOK := true
	for i, r := range recs {
		if st.Ingest(r) != nil {
			stepOK = false
		}
		steps = append(steps, viewStr(st.View()))
		stepOK = stepOK && steps[i] == wantSteps[i]
	}
	ok(stepOK, "steps: "+strings.Join(steps, " | "))

	out, err := st.Compact([][]seg.Record{recs[5:], recs[:5]}...) // seg2 first, seg1 last
	view := st.View()
	ok(err == nil && viewStr(view) == "b=y2,c=z2", "final view: a gone, b=y2, c=z2")
	ok(st.Watermark() == 8, fmt.Sprintf("watermark: %d", st.Watermark()))
	delFree := err == nil
	if err == nil {
		for _, r := range out.Records {
			delFree = delFree && !r.Tombstone()
		}
		delFree = delFree && len(out.Records) == 2 && out.Watermark == 8
	}
	ok(delFree, "tombstone-gc: compacted output sorted, 2 live records, no del")

	// Four distinguishable rejection categories, then state untouched.
	bads := []struct {
		rec  seg.Record
		want error
	}{
		{seg.Record{Key: "", Version: 9, Op: "put", Val: "v"}, seg.ErrKey},
		{seg.Record{Key: "d", Version: 8, Op: "put", Val: "v"}, seg.ErrVersion},
		{seg.Record{Key: "d", Version: 9, Op: "nop", Val: "v"}, seg.ErrOp},
		{seg.Record{Key: "d", Version: 9, Op: "put"}, seg.ErrVal},
		{seg.Record{Key: "d", Version: 9, Op: "del", Val: "v"}, seg.ErrVal},
	}
	errOK, distinct := true, map[error]bool{}
	wmBefore, viewBefore := st.Watermark(), viewStr(st.View())
	for _, b := range bads {
		e := st.Ingest(b.rec)
		errOK = errOK && errors.Is(e, b.want)
		if u := errors.Unwrap(e); u != nil { // wrapped version error
			distinct[u] = true
		} else {
			distinct[e] = true
		}
	}
	ok(errOK && len(distinct) >= 4, "errors: key/version/op/val rejections are distinguishable")
	ok(st.Watermark() == wmBefore && viewStr(st.View()) == viewBefore &&
		st.Ingest(seg.Record{Key: "d", Version: 9, Op: "put", Val: "ok"}) == nil,
		"no-trace: rejections changed nothing, store still usable")

	// Large m: per-key latest pointer keeps compaction output O(1).
	largeOK := true
	for _, m := range []int{100, 1000, 10000} {
		s2 := api.New()
		for v := int64(1); v <= int64(m); v++ {
			if s2.Ingest(seg.Record{Key: "k", Version: v, Op: "put", Val: "old"}) != nil {
				largeOK = false
			}
		}
		_ = s2.Ingest(seg.Record{Key: "k", Version: int64(m) + 1, Op: "put", Val: "new"})
		o, e := s2.Compact()
		largeOK = largeOK && e == nil && len(o.Records) == 1 && o.Records[0].Val == "new"
	}
	ok(largeOK, "large-m: m=100,1000,10000 each compact to 1 winner (cmp count pinned by comp test)")

	// Concurrent read-only access must agree field by field.
	wantView, wantMark := viewStr(st.View()), st.Watermark()
	concOK := atomic.Bool{}
	concOK.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if viewStr(st.View()) != wantView || st.Watermark() != wantMark || st.SelfCheck() != nil {
					concOK.Store(false)
					return
				}
			}
		}()
	}
	wg.Wait()
	ok(concOK.Load(), "concurrent: 32 readers x 200 iters see identical view/watermark")

	if failed.Load() {
		os.Exit(1)
	}
}
