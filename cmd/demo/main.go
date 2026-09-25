// Command demo exercises the snapshot compaction packages. It prints at
// most ten OK/FAIL lines and exits non-zero on any failure.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/comp"
	"ontology/seg"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		failed = true
		fmt.Printf("FAIL %s\n", name)
	}
}

func snap(d *api.DB) string {
	v := d.View()
	keys := []string{"a", "b", "c"}
	s := ""
	for _, k := range keys {
		if val, ok := v[k]; ok {
			if s != "" {
				s += ","
			}
			s += k + "=" + val
		}
	}
	return "{" + s + "}"
}

func main() {
	d := api.New()
	recs := []seg.Rec{
		{Key: "a", Version: 1, Op: seg.OpPut, Val: "x"},
		{Key: "b", Version: 2, Op: seg.OpPut, Val: "y"},
		{Key: "a", Version: 3, Op: seg.OpPut, Val: "x2"},
		{Key: "c", Version: 4, Op: seg.OpPut, Val: "z"},
		{Key: "b", Version: 5, Op: seg.OpDel},
		{Key: "a", Version: 6, Op: seg.OpDel},
		{Key: "c", Version: 7, Op: seg.OpPut, Val: "z2"},
		{Key: "b", Version: 8, Op: seg.OpPut, Val: "y2"},
	}
	want := []string{"{a=x}", "{a=x,b=y}", "{a=x2,b=y}", "{a=x2,b=y,c=z}",
		"{a=x2,c=z}", "{c=z}", "{c=z2}", "{b=y2,c=z2}"}
	got := make([]string, 8)
	for i, r := range recs {
		if err := d.Ingest(r); err != nil {
			failed = true
		}
		got[i] = snap(d)
	}
	check("8-step live snapshots: "+fmt.Sprint(got), reflect.DeepEqual(got, want))

	final := map[string]string{"b": "y2", "c": "z2"}
	check("final view b=y2,c=z2, a absent", reflect.DeepEqual(d.View(), final))
	check("watermark == 8", d.Watermark() == 8)

	s1 := &comp.Segment{Records: recs[0:5]}
	s2 := &comp.Segment{Records: recs[5:8]}
	out, err := d.Compact([]*comp.Segment{s2, s1})
	check("compact no del, key-sorted, wm=8", err == nil && out.Watermark == 8 &&
		len(out.Records) == 2 && out.Records[0].Key == "b" && out.Records[1].Key == "c" &&
		out.Records[0].Version == 8 && out.Records[1].Version == 7)

	bad := []seg.Rec{
		{Key: "", Version: 9, Op: seg.OpPut, Val: "q"},
		{Key: "a", Version: 3, Op: seg.OpPut, Val: "q"},
		{Key: "e", Version: 9, Op: "patch", Val: "q"},
		{Key: "e", Version: 9, Op: seg.OpPut, Val: ""},
	}
	sents := []error{seg.ErrEmptyKey, api.ErrBadVersion, seg.ErrBadOp, seg.ErrBadVal}
	distinct := true
	for i, r := range bad {
		distinct = distinct && errors.Is(d.Ingest(r), sents[i])
	}
	check("four distinct decidable errors", distinct)
	_, still := d.View()["e"]
	check("state unchanged after rejection", d.Watermark() == 8 && reflect.DeepEqual(d.View(), final) && !still)
	check("pointer seek O(1) for m in 100..10000", comp.NewMerger().VerifyPointerSeek([]int{100, 1000, 10000}) == nil)

	const n = 16
	var wg sync.WaitGroup
	views := make([]map[string]string, n)
	wms := make([]int64, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			views[i] = d.View()
			wms[i] = d.Watermark()
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && reflect.DeepEqual(views[i], views[0]) && wms[i] == wms[0]
	}
	check("concurrent readers identical", same)
	check("SelfCheck", d.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
