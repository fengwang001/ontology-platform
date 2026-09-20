// Command demo exercises the equality index and selectivity estimator
// end to end and prints one OK/FAIL verdict per check.
package main

import (
	"fmt"
	"os"

	"ontology"
)

var failed int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func main() {
	s := ontology.NewStore("color")
	// 4000 rows: color c0..c3, score 0..9, plus nil/missing/empty cases.
	for i := 0; i < 4000; i++ {
		id := fmt.Sprintf("e%04d", i)
		s.Upsert(id, map[string]any{"color": fmt.Sprintf("c%d", i%4), "score": i % 10})
	}
	s.Upsert("nil-1", map[string]any{"color": nil, "score": 1})
	s.Upsert("missing-1", map[string]any{"score": 2})
	s.Upsert("empty-1", map[string]any{"color": "", "score": 3})

	check("verify", s.Verify() == nil, "index matches full-table rescan")

	s.Upsert("e0000", map[string]any{"color": "c1", "score": 0}) // was c0
	oldGone := !contains(s.Lookup("color", "c0"), "e0000")
	newSeen := contains(s.Lookup("color", "c1"), "e0000")
	check("update", oldGone && newSeen, "old value misses, new value hits")

	s.Upsert("e0001", map[string]any{"color": nil, "score": 1})
	missing, nils := s.IsNull("color")
	nilOK := contains(nils, "e0001") && contains(nils, "nil-1")
	distinct := contains(missing, "missing-1") && !contains(missing, "e0001")
	check("nil-vs-missing", nilOK && distinct, "nil and missing reported separately")

	check("empty-string", contains(s.Lookup("color", ""), "empty-1"),
		"empty string is a normal indexed value")

	sum := s.IndexedRowCount("color") + s.MissingCount("color") + s.NullCount("color")
	check("count-sum", sum == s.TotalRows(),
		fmt.Sprintf("indexed+missing+nil=%d total=%d", sum, s.TotalRows()))

	ex := s.Estimate("color", "c2")
	check("estimate-exact", ex.Exact && ex.ErrorBound == 0 && ex.Count == len(s.Lookup("color", "c2")),
		fmt.Sprintf("exact count=%d", ex.Count))

	est := s.Estimate("score", 7)
	truth := 400 // e0000..e3999 with score i%10==7; extras have score 1,2,3
	within := truth >= est.Count-est.ErrorBound && truth <= est.Count+est.ErrorBound
	small := est.RowsExamined*10 < s.TotalRows()
	check("estimate-sample", within && small && !est.Exact,
		fmt.Sprintf("est=%d±%d true=%d examined=%d/%d",
			est.Count, est.ErrorBound, truth, est.RowsExamined, s.TotalRows()))

	s.Delete("e0002")
	s.Delete("nil-1")
	_, nilsAfter := s.IsNull("color")
	gone := !contains(s.Lookup("color", "c2"), "e0002") && !contains(nilsAfter, "nil-1")
	check("delete", gone && s.Verify() == nil, "deleted entities leave no residue")

	fmt.Printf("SUMMARY: %d check(s) failed\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
