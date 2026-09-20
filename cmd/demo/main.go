// Command demo exercises the equality index and selectivity
// estimator end to end, printing one OK/FAIL verdict per check.
package main

import (
	"fmt"
	"os"

	"ontology"
)

var passed, total int

func check(ok bool, format string, args ...any) {
	total++
	verdict := "FAIL"
	if ok {
		verdict = "OK  "
		passed++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func has(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func main() {
	s := ontology.NewStore("color")
	const n = 10000
	beijing := 0
	for i := 0; i < n; i++ {
		city := "other"
		if i%2 == 0 {
			city = "beijing"
			beijing++
		}
		s.Upsert(fmt.Sprintf("e%05d", i), map[string]any{
			"color": fmt.Sprintf("c%d", i%10),
			"city":  city,
		})
	}
	check(s.Verify() == nil, "Verify passes after bulk load of %d rows", n)

	s.Upsert("e00001", map[string]any{"color": "violet", "city": "beijing"})
	beijing++
	check(!has(s.Lookup("color", "c1"), "e00001") && has(s.Lookup("color", "violet"), "e00001"),
		"update: old value c1 misses, new value violet hits")

	s.Upsert("e00001", map[string]any{"color": nil, "city": "beijing"})
	s.Upsert("e-missing", map[string]any{"city": "other"})
	info := s.IsNull("color")
	check(has(info.Nil, "e00001") && has(info.Missing, "e-missing") && !has(info.Nil, "e-missing"),
		"nil vs missing: both in IsNull, separately readable")

	s.Upsert("e-empty", map[string]any{"color": ""})
	check(has(s.Lookup("color", ""), "e-empty"), "empty string is a normal indexed value")

	indexed, missing, null := s.Counts("color")
	check(indexed+missing+null == s.RowCount(),
		"counts: indexed(%d)+missing(%d)+nil(%d) = rows(%d)", indexed, missing, null, s.RowCount())

	est := s.Estimate("color", "c3")
	truth := len(s.Lookup("color", "c3"))
	check(est.Exact && est.Rows == truth && est.RowsChecked == 0,
		"indexed estimate exact: %d == truth %d (0 rows checked)", est.Rows, truth)

	sest := s.Estimate("city", "beijing")
	lo, hi := sest.Rows-sest.AbsErrorBound, sest.Rows+sest.AbsErrorBound
	check(!sest.Exact && beijing >= lo && beijing <= hi && sest.RowsChecked <= 512,
		"sampled estimate: truth %d in [%d,%d], checked %d/%d rows",
		beijing, lo, hi, sest.RowsChecked, s.RowCount())

	s.Delete("e00001")
	s.Delete("e-empty")
	gone := !has(s.Lookup("color", ""), "e-empty") && !has(s.IsNull("color").Nil, "e00001")
	check(gone && s.Verify() == nil, "delete leaves no residue; Verify still passes")

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
