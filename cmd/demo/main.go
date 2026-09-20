// Command demo exercises the ontology equality index and selectivity
// estimator end to end, printing one OK/FAIL verdict per check.
package main

import (
	"fmt"
	"os"

	"ontology"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func main() {
	s := ontology.New("dept", "tag")
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("e%04d", i)
		attrs := map[string]any{"dept": fmt.Sprintf("d%d", i%5)}
		switch {
		case i%10 == 0:
			attrs["city"] = "shanghai" // unindexed attr, 500 rows
		case i%10 == 1:
			attrs["city"] = nil // explicit nil
		case i%10 == 2:
			attrs["tag"] = "" // empty string: a normal, indexed value
		case i%10 == 3:
			attrs["tag"] = nil // explicit nil on an indexed attr
		case i%10 == 4:
			attrs["tag"] = "x"
		}
		s.Upsert(id, attrs)
	}

	check("verify", s.Verify() == nil, "index matches full scan")

	s.Upsert("e0007", map[string]any{"dept": "d9", "city": "beijing"})
	oldGone := len(s.Query("dept", "d2")) == 999
	newFound := len(s.Query("dept", "d9")) == 1
	check("update", oldGone && newFound, "old value misses, new value hits")

	s.Upsert("e0007", map[string]any{"dept": "d9", "city": nil})
	missing, nilVal := s.IsNull("city")
	inNil := contains(nilVal, "e0007")
	distinguishable := !contains(missing, "e0007") && len(missing) > 0 && len(nilVal) > 0
	check("nil-vs-missing", inNil && distinguishable,
		fmt.Sprintf("nil=%d missing=%d", len(nilVal), len(missing)))

	emptyHits := s.Query("tag", "")
	check("empty-string", len(emptyHits) == 500, "empty string indexed as normal value")

	indexed, miss, nilC := s.AttrCounts("tag")
	check("counts_sum", indexed+miss+nilC == s.Len(),
		fmt.Sprintf("%d+%d+%d=%d rows", indexed, miss, nilC, s.Len()))

	exact := s.Estimate("dept", "d0")
	truthDept := len(s.Query("dept", "d0"))
	check("estimate_exact", exact.Exact && exact.Value == truthDept,
		fmt.Sprintf("est=%d truth=%d exact=%v", exact.Value, truthDept, exact.Exact))

	est := s.Estimate("city", "shanghai")
	truthCity := 500
	inBound := !est.Exact && truthCity >= est.Value-est.Bound && truthCity <= est.Value+est.Bound
	fewRows := est.RowsExamined*10 < s.Len()
	lo := est.Value - est.Bound
	if lo < 0 {
		lo = 0
	}
	check("estimate_sampled", inBound && fewRows,
		fmt.Sprintf("truth=%d in [%d,%d], examined=%d/%d rows",
			truthCity, lo, est.Value+est.Bound, est.RowsExamined, s.Len()))

	s.Delete("e0007")
	_, nilAfter := s.IsNull("city")
	residue := len(s.Query("dept", "d9")) == 0 && !contains(nilAfter, "e0007")
	check("delete", residue && s.Verify() == nil, "no residue after delete")

	fmt.Printf("TOTAL %d checks, %d failed\n", 8, failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
