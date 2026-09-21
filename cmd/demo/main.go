// Command demo exercises the thresholded edit-distance matcher end to
// end. It takes no arguments, uses no network, prints one OK/FAIL line
// per check plus a summary, and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology"
)

var failures int

func check(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	// 1. Exact distance within k equals the unlimited full distance.
	res, err := ontology.Distance("kitten", "sitting", 3)
	full, uerr := ontology.DistanceUnlimited("kitten", "sitting")
	check(err == nil && uerr == nil && !res.Exceeded && res.Distance == 3 && res.Distance == full,
		"within-k exact distance: kitten/sitting k=3 -> %d, unlimited -> %d", res.Distance, full)

	// 2. Two 1000-rune, completely different strings, k=2: early
	// termination keeps filled cells in O(k*len), bound (2k+1)*1001=5005.
	a1k, b1k := strings.Repeat("a", 1000), strings.Repeat("b", 1000)
	res, err = ontology.Distance(a1k, b1k, 2)
	check(err == nil && res.Exceeded && res.Stats.CellsFilled > 0 && res.Stats.CellsFilled <= 5005,
		"1000 vs 1000 runes k=2: exceeded, cells filled = %d (bound 5005, full matrix 1000000)",
		res.Stats.CellsFilled)

	// 3. Length difference > k: exceeded with zero cells filled.
	res, err = ontology.Distance("aaaaaa", "aa", 2)
	check(err == nil && res.Exceeded && res.Stats.CellsFilled == 0,
		"length diff 4 > k=2: exceeded, cells filled = %d", res.Stats.CellsFilled)

	// 4. Code-point semantics: "café" vs "cafe" is 1, not 2.
	res, err = ontology.Distance("café", "cafe", 1)
	n, rerr := ontology.RuneLen("café")
	check(err == nil && rerr == nil && !res.Exceeded && res.Distance == 1 && n == 4,
		"café vs cafe: distance = %d (RuneLen(café) = %d)", res.Distance, n)

	// 5. A 4-byte emoji counts as one code point.
	res, err = ontology.Distance("a\U0001F600b", "ab", 1)
	check(err == nil && !res.Exceeded && res.Distance == 1,
		"emoji insertion: distance = %d", res.Distance)

	// 6. Invalid UTF-8: decidable error naming side and byte offset.
	_, err = ontology.Distance("ok", "ab\xffcd", 3)
	var uerr2 *ontology.UTF8Error
	check(errors.As(err, &uerr2) && uerr2.Side == ontology.SideRight && uerr2.ByteOffset == 2,
		"invalid UTF-8: %v", err)

	// 7. Negative k is a decidable error; k=0 is an equality check.
	_, err = ontology.Distance("a", "a", -1)
	eq, eerr := ontology.Distance("same", "same", 0)
	neq, nerr := ontology.Distance("same", "diff", 0)
	check(errors.Is(err, ontology.ErrNegativeK) && eerr == nil && nerr == nil &&
		!eq.Exceeded && eq.Distance == 0 && neq.Exceeded,
		"k=-1 -> ErrNegativeK; k=0: equal -> 0, different -> exceeded")

	// 8. Symmetry: distance and filled-cell count are identical.
	fwd, ferr := ontology.Distance("kitten", "sitting", 3)
	rev, rerr2 := ontology.Distance("sitting", "kitten", 3)
	check(ferr == nil && rerr2 == nil && fwd.Distance == rev.Distance &&
		fwd.Stats.CellsFilled == rev.Stats.CellsFilled,
		"symmetry: d=%d cells=%d both directions", fwd.Distance, fwd.Stats.CellsFilled)

	// 9. Case folding: simple fold equates STRASSE-only via ß orbit.
	fres, ferr := ontology.DistanceWithOptions("Hello", "hELLO", 0, ontology.Options{FoldCase: true})
	check(ferr == nil && !fres.Exceeded && fres.Distance == 0,
		"fold case: Hello vs hELLO k=0 -> distance %d", fres.Distance)

	// 10. 100k runes per side, k=3: work arrays stay at 2k+1 = 7 ints.
	a100k := []rune(strings.Repeat("ab", 50_000))
	b100k := append([]rune(nil), a100k...)
	b100k[7], b100k[50_000], b100k[99_999] = 'x', 'y', 'z'
	res, err = ontology.Distance(string(a100k), string(b100k), 3)
	check(err == nil && !res.Exceeded && res.Distance == 3 && res.Stats.MaxWorkArrayLen == 7,
		"100k runes k=3: distance = %d, max work array len = %d (vs 100k x 100k full matrix)",
		res.Distance, res.Stats.MaxWorkArrayLen)

	fmt.Printf("TOTAL %d checks, %d failed\n", 10, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
