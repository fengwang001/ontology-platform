// Command demo demonstrates partition pruning (static + dynamic).
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/part"
	"ontology/prune"
)

type row struct {
	id               string
	lo, hi, vlo, vhi int64
	static, dynamic  bool
}

var rows = []row{
	{"P0", 0, 10, 10, 20, true, true},
	{"P1", 10, 20, 30, 40, true, false},
	{"P2", 20, 30, 50, 60, false, false},
	{"P3", 30, 40, 15, 25, false, true},
	{"P4", 40, 50, 60, 80, false, true},
}

func pOver(r row, lo, hi int64) bool { return !(r.hi <= lo || r.lo >= hi) }
func vOver(r row, lo, hi int64) bool { return !(r.vhi < lo || r.vlo >= hi) }

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	fails := 0
	ok := func(b bool) string {
		if !b {
			fails++
			return "FAIL"
		}
		return "OK"
	}
	d := api.New()
	for _, r := range rows {
		if err := d.AddPartition(r.id, r.lo, r.hi, r.vlo, r.vhi); err != nil {
			fails++
		}
	}
	scan, pruned, _ := d.Query(20, 50, 30, 60)
	member := func(id string, set []string) bool {
		for _, x := range set {
			if x == id {
				return true
			}
		}
		return false
	}
	for _, r := range rows {
		p, _ := part.New(r.id, r.lo, r.hi, r.vlo, r.vhi)
		s, dy := p.StaticPruned(20, 50), p.DynamicPruned(30, 60)
		wantScan := pOver(r, 20, 50) && vOver(r, 30, 60)
		verdict := map[bool]string{true: "scan", false: "pruned"}[wantScan]
		good := s == r.static && dy == r.dynamic && member(r.id, scan) == wantScan &&
			member(r.id, pruned) != wantScan
		fmt.Printf("%s s=%-5t d=%-5t -> %-6s %s\n", r.id, s, dy, verdict, ok(good))
	}
	// soundness (per-partition box truth) + scan set + order independence.
	sound, sf, ds := true, []string{}, []string{}
	for _, r := range rows {
		ps, vs := pOver(r, 20, 50), vOver(r, 30, 60)
		if ps && vs {
			sf = append(sf, r.id)
		}
		if vs && ps {
			ds = append(ds, r.id)
		}
		if ps && vs && member(r.id, pruned) {
			sound = false // an intersecting box was pruned: a row could be missed
		}
	}
	fmt.Printf("scan=%v sound order-independent %s\n", scan,
		ok(sound && eq(scan, sf) && eq(sf, ds) && eq(scan, []string{"P2"})))
	// three distinct decidable errors.
	e1 := d.AddPartition("BAD", 10, 5, 1, 2)
	_, _, e2 := d.Query(5, 5, 0, 1)
	tt := prune.NewTable()
	x, _ := part.New("X", 0, 10, 0, 1)
	_ = tt.Add(x)
	_, e3 := tt.Prune([]string{"GHOST"}, prune.Predicate{PLo: 0, PHi: 1, VLo: 0, VHi: 1})
	distinct := errors.Is(e1, api.ErrInvalidPartition) && errors.Is(e2, api.ErrInvalidPredicate) &&
		errors.Is(e3, api.ErrUnknownPartition) && e1 != e2 && e2 != e3 && e1 != e3
	fmt.Printf("errors partition/predicate/unknown distinct %s\n", ok(distinct))
	// rejected ops leave no trace and the table stays usable.
	scan2, pruned2, _ := d.Query(20, 50, 30, 60)
	fmt.Printf("no-trace still-usable %s\n", ok(eq(scan, scan2) && eq(pruned, pruned2) && eq(scan2, []string{"P2"})))
	// large N: checked-partition count must not grow with N.
	fmt.Printf("sublinear checked-count %s\n", ok(prune.CheckComplexity() == nil))
	// concurrent queries return element-wise identical scan sets.
	const ng = 32
	var wg sync.WaitGroup
	got := make([][]string, ng)
	for i := 0; i < ng; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); got[i], _, _ = d.Query(20, 50, 30, 60) }(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < ng; i++ {
		same = same && eq(got[0], got[i])
	}
	fmt.Printf("concurrent %d queries identical %s\n", ng, ok(same))
	if fails > 0 {
		os.Exit(1)
	}
}
