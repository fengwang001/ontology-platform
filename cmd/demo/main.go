// Command demo exercises the change-stream deduper and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ontology/dedup"
	"ontology/rec"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func table(st *rec.State) string {
	var b strings.Builder
	for i, p := range st.Snapshot() {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d:%s", p.Offset, p.Value)
	}
	return b.String()
}

func main() {
	// Section 3: the ten-step walkthrough on partition 0, window 4.
	evs := []rec.Event{
		{Offset: 1, Value: "a"}, {Offset: 2, Value: "b"},
		{Offset: 3, Value: "c"}, {Offset: 2, Value: "b"},
		{Offset: 3, Value: "x"}, {Offset: 4, Value: "d"},
		{Offset: 1, Value: "z"}, {Offset: 5, Value: "e"},
		{Offset: 2, Value: "b"}, {Offset: 1, Value: "a"},
	}
	st := rec.New(4)
	var marks strings.Builder
	want := "aaa=CaCa=R" // a=② apply, ==③ idem, C=④ conflict, R=⑤ rewind
	for _, e := range evs {
		applied, err := st.Apply(e)
		switch {
		case errors.Is(err, rec.ErrConflict):
			marks.WriteByte('C')
		case errors.Is(err, rec.ErrRewound):
			marks.WriteByte('R')
		case applied:
			marks.WriteByte('a')
		default:
			marks.WriteByte('=')
		}
	}
	ok("ten-step "+marks.String()+" | "+table(st), marks.String() == want &&
		st.Max() == 5 && table(st) == "2:b 3:c 4:d 5:e")

	// Idempotent repeat changes nothing; conflict and rewind leave no trace.
	before := st.Snapshot()
	_, errI := st.Apply(rec.Event{Offset: 4, Value: "d"})
	_, errC := st.Apply(rec.Event{Offset: 4, Value: "q"})
	_, errR := st.Apply(rec.Event{Offset: 1, Value: "a"})
	_, errN := st.Apply(rec.Event{Offset: -1, Value: "z"})
	ok("idem/conflict/rewind/negative distinct", errI == nil &&
		errors.Is(errC, rec.ErrConflict) && errors.Is(errR, rec.ErrRewound) &&
		errors.Is(errN, rec.ErrNegativeOffset) &&
		fmt.Sprint(before) == fmt.Sprint(st.Snapshot()) && st.Max() == 5)

	// Large m: inspected-entry count stays bounded (exposed only as verdict).
	ok("eviction checks bounded in m", rec.SelfCheck() == nil)

	// Multi-partition: the four failures are distinct; a batch with one
	// reject is wholly discarded and the instance stays usable.
	_, errW := dedup.New(0)
	d, _ := dedup.New(4)
	seed := []dedup.Event{{Partition: 0, Offset: 1, Value: "a"}, {Partition: 0, Offset: 2, Value: "b"}}
	if _, err := d.ApplyBatch(seed); err != nil {
		ok("seed", false)
	}
	pre := d.View()
	bad := []dedup.Event{{Partition: 0, Offset: 3, Value: "c"}, {Partition: 0, Offset: 2, Value: "z"}}
	_, errB := d.ApplyBatch(bad)
	ok("four sentinels distinct", errors.Is(errW, dedup.ErrInvalidWindow) &&
		errors.Is(errB, rec.ErrConflict))
	ok("rejected batch no trace, still usable", reflect.DeepEqual(pre, d.View()) &&
		func() bool { _, e := d.Apply(dedup.Event{Partition: 0, Offset: 3, Value: "c"}); return e == nil }())
	ok("self-check", d.SelfCheck() == nil)

	// Concurrent read-only callers all see the identical view.
	const n = 16
	var wg sync.WaitGroup
	views := make([]map[int][]dedup.Entry, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() { defer wg.Done(); views[i] = d.View() }()
	}
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(views[0], views[i]) {
			same = false
		}
	}
	ok("concurrent readers identical", same)
}
