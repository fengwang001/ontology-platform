// Command demo exercises the interval index end to end and prints
// one OK/FAIL line per requirement. It takes no arguments.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sync"
	"unsafe"

	"ontology/ival"
	"ontology/query"
	"ontology/tree"
)

var fails int

func report(name string, ok bool) {
	line := "OK   " + name
	if !ok {
		line = "FAIL " + name
		fails++
	}
	fmt.Println(line)
}

func iv(lo, hi int64) ival.Interval { return ival.Interval{Lo: lo, Hi: hi} }

func mustInsert(tr *tree.Tree, ivs ...ival.Interval) {
	for _, x := range ivs {
		if err := tr.Insert(x); err != nil {
			panic(err)
		}
	}
}

// visited reads the unexported query counter for display only; the
// counter stays out of the public API by design.
func visited(q *query.Querier) int64 {
	f := reflect.ValueOf(q).Elem().FieldByName("visited")
	return *(*int64)(unsafe.Pointer(f.UnsafeAddr()))
}

func main() {
	tr := tree.New()
	mustInsert(tr, iv(1, 3), iv(3, 5), iv(2, 2))
	q := query.New(tr)
	ov, _ := q.Overlap(iv(1, 3))
	report("touching intervals do not overlap", slices.Equal(ov, []ival.Interval{iv(1, 3)}))
	report("stab at touch point belongs to right", slices.Equal(q.Stab(3), []ival.Interval{iv(3, 5)}))
	report("zero-length insertable", tr.Size() == 3)
	report("zero-length never stabbed", slices.Equal(q.Stab(2), []ival.Interval{iv(1, 3)}))
	zov, _ := q.Overlap(iv(1, 5))
	report("zero-length never overlap-hit", slices.Equal(zov, []ival.Interval{iv(1, 3), iv(3, 5)}))

	rng := rand.New(rand.NewSource(3))
	match, orderOK := true, true
	for round := 0; round < 30; round++ {
		var all []ival.Interval
		rt := tree.New()
		for i := 0; i < 50; i++ {
			lo, hi := rng.Int63n(100), rng.Int63n(100)
			if lo > hi {
				lo, hi = hi, lo
			}
			x := iv(lo, hi)
			mustInsert(rt, x)
			all = append(all, x)
		}
		rq := query.New(rt)
		got, _ := rq.Overlap(iv(20, 60))
		var want []ival.Interval
		for _, x := range all {
			if ival.Overlaps(x, iv(20, 60)) {
				want = append(want, x)
			}
		}
		slices.SortFunc(want, ival.Compare)
		match = match && slices.Equal(got, want)
		st := tree.New()
		for _, i := range rng.Perm(len(all)) {
			mustInsert(st, all[i])
		}
		sgot, _ := query.New(st).Overlap(iv(20, 60))
		orderOK = orderOK && slices.Equal(sgot, got)
	}
	report("matches naive scan (30 random rounds)", match)
	report("result order independent of insertion", orderOK)

	mt := tree.New()
	mustInsert(mt, iv(1, 5), iv(1, 5), iv(1, 5))
	_ = mt.Delete(iv(1, 5))
	mgot, _ := query.New(mt).Overlap(iv(0, 9))
	report("multiset delete removes exactly one", len(mgot) == 2)
	_ = mt.Delete(iv(1, 5))
	_ = mt.Delete(iv(1, 5))
	gone, _ := query.New(mt).Overlap(iv(0, 9))
	report("deleted intervals never reappear", len(gone) == 0)

	report("error: invalid interval insert", tr.Insert(iv(9, 1)) == ival.ErrInvalid)
	report("error: delete missing interval", tr.Delete(iv(7, 8)) == tree.ErrNotFound)
	_, qerr := q.Overlap(iv(9, 1))
	report("error: invalid query interval", qerr == ival.ErrInvalid)

	lt := tree.New(tree.WithMaxIntervals(1))
	mustInsert(lt, iv(0, 1))
	rej := lt.Insert(iv(1, 2)) == tree.ErrTooMany
	usable := lt.Delete(iv(0, 1)) == nil && lt.Insert(iv(2, 3)) == nil
	report("interval limit rejected, still usable", rej && usable)
	bq := query.New(tr, query.WithMaxBatch(1))
	_, berr := bq.Batch([]ival.Interval{iv(0, 2), iv(2, 4)})
	_, bok := bq.Batch([]ival.Interval{iv(0, 2)})
	report("batch limit rejected, still usable", berr == query.ErrBatchTooLarge && bok == nil)

	ct := tree.New()
	mustInsert(ct, iv(0, 10), iv(5, 9))
	cq := query.New(ct)
	want := cq.Stab(6)
	var wg sync.WaitGroup
	same := true
	for k := 0; k < 16; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				if !slices.Equal(cq.Stab(6), want) {
					same = false
				}
			}
		}()
	}
	wg.Wait()
	report("concurrent queries bitwise identical", same)

	var counts [2]int64
	for i, n := range []int{1000, 100000} {
		dt := tree.New()
		for k := 0; k < n; k++ {
			mustInsert(dt, iv(int64(2*k), int64(2*k+1)))
		}
		dq := query.New(dt)
		dq.Stab(int64(n))
		counts[i] = visited(dq)
	}
	report(fmt.Sprintf("visited nodes N=1k:%d N=100k:%d (<=100)", counts[0], counts[1]),
		counts[0] <= 100 && counts[1] <= 100)
	report("tree.Check self-audit passes", tr.Check() == nil && mt.Check() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
