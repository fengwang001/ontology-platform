package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/audit"
	"ontology/cursor"
	"ontology/page"
	"ontology/row"
)

var failed bool

func check(name, detail string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func checkBitFlip() {
	valid := cursor.Encode(1.5, "r0007", cursor.Forward)
	var counts [3]int
	bad := 0
	for i := range valid {
		for bit := 0; bit < 8; bit++ {
			mut := make([]byte, len(valid))
			copy(mut, valid)
			mut[i] ^= 1 << uint(bit)
			_, err := cursor.Decode(mut)
			switch {
			case errors.Is(err, cursor.ErrTruncated):
				counts[0]++
			case errors.Is(err, cursor.ErrDirection):
				counts[1]++
			case errors.Is(err, cursor.ErrChecksum):
				counts[2]++
			default:
				bad++
			}
		}
	}
	total := len(valid) * 8
	check("bitflip-rejected",
		fmt.Sprintf("variants=%d truncated=%d direction=%d checksum=%d", total, counts[0], counts[1], counts[2]),
		bad == 0 && counts[0]+counts[1]+counts[2] == total)
}

func ids(rows []row.Row) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = r.ID
	}
	return strings.Join(parts, ",")
}

func dupScoreStore() *row.Store {
	s := row.New()
	for i := 0; i < 6; i++ {
		s.Add(row.Row{Score: 1.0, ID: fmt.Sprintf("d%d", i)})
	}
	for i := 6; i < 10; i++ {
		s.Add(row.Row{Score: float64(i), ID: fmt.Sprintf("d%d", i)})
	}
	return s
}

func forwardAll(s *row.Store, size int) [][]row.Row {
	var pages [][]row.Row
	cur := []byte(nil)
	for {
		res, err := page.Forward(s, cur, size)
		if err != nil || len(res.Rows) == 0 {
			return pages
		}
		pages = append(pages, res.Rows)
		cur = res.Next
	}
}

func checkDupScores() {
	s := dupScoreStore()
	pages := forwardAll(s, 3)
	var all []row.Row
	seen := map[string]int{}
	for _, p := range pages {
		for _, r := range p {
			seen[r.ID]++
			all = append(all, r)
		}
	}
	want := "d0,d1,d2,d3,d4,d5,d6,d7,d8,d9"
	ok := len(all) == 10 && ids(all) == want
	for _, n := range seen {
		ok = ok && n == 1
	}
	check("dup-scores-ten-rows", fmt.Sprintf("pages=%d order=%s", len(pages), ids(all)), ok)
}

func checkBackward() {
	s := dupScoreStore()
	p1, _ := page.Forward(s, nil, 3)
	p2, _ := page.Forward(s, p1.Next, 3)
	p3, _ := page.Forward(s, p2.Next, 3)
	back, err := page.Backward(s, p3.Reverse, 3)
	ok := err == nil && ids(back.Rows) == ids(p2.Rows)
	check("backward-equals-page2", fmt.Sprintf("back=%s want=%s", ids(back.Rows), ids(p2.Rows)), ok)
}

func checkConcurrentMutations() {
	s := row.New()
	for i := 0; i < 10; i++ {
		s.Add(row.Row{Score: float64(i), ID: fmt.Sprintf("k%d", i)})
	}
	p1, _ := page.Forward(s, nil, 2)
	p2, _ := page.Forward(s, p1.Next, 2)
	s.Add(row.Row{Score: 0.5, ID: "a1"}) // into already-turned range
	s.Add(row.Row{Score: 1.5, ID: "a2"})
	s.Add(row.Row{Score: 6.5, ID: "b1"}) // into not-yet-turned range
	s.Add(row.Row{Score: 7.5, ID: "b2"})
	s.Del("k4") // delete a row of the upcoming third page
	seen := map[string]bool{}
	dups := 0
	for _, r := range append(append([]row.Row{}, p1.Rows...), p2.Rows...) {
		seen[r.ID] = true
	}
	var later []row.Row
	cur := p2.Next
	for {
		res, _ := page.Forward(s, cur, 2)
		if len(res.Rows) == 0 {
			break
		}
		for _, r := range res.Rows {
			if seen[r.ID] {
				dups++
			}
			seen[r.ID] = true
			later = append(later, r)
		}
		cur = res.Next
	}
	got := ids(later)
	ok := dups == 0 && !seen["k4"] && seen["b1"] && seen["b2"] &&
		strings.Index(got, "b1") < strings.Index(got, "b2") && got == "k5,k6,b1,k7,b2,k8,k9"
	check("concurrent-mutations", fmt.Sprintf("later=%s dups=%d", got, dups), ok)
}

func checkCompareBound() {
	s := row.New()
	const n = 10000
	for i := 0; i < n; i++ {
		s.Add(row.Row{Score: float64(i), ID: fmt.Sprintf("r%05d", i)})
	}
	mid, _ := page.Forward(s, nil, n/2)
	res, err := page.Forward(s, mid.Next, 20)
	bound := page.Bound(20, n)
	ok := err == nil && len(res.Rows) == 20 && res.Compared <= bound
	check("compare-bound", fmt.Sprintf("compared=%d bound=%d", res.Compared, bound), ok)
}

func checkCrossDirection() {
	s := dupScoreStore()
	p1, _ := page.Forward(s, nil, 3)
	_, err := page.Backward(s, p1.Next, 3)
	ok := errors.Is(err, cursor.ErrDirection)
	check("cross-direction-rejected", fmt.Sprintf("err=%v", err), ok)
}

func checkEmptyCursor() {
	s := dupScoreStore()
	fwd, err1 := page.Forward(s, nil, 4)
	back, err2 := page.Backward(s, nil, 3)
	ok := err1 == nil && err2 == nil &&
		ids(fwd.Rows) == "d0,d1,d2,d3" && ids(back.Rows) == "d7,d8,d9"
	check("empty-cursor-start", fmt.Sprintf("fwd=%s back=%s", ids(fwd.Rows), ids(back.Rows)), ok)
}

func checkDeletedCursor() {
	s := dupScoreStore()
	p1, _ := page.Forward(s, nil, 3)
	s.Del("d2") // the row the cursor points at
	res, err := page.Forward(s, p1.Next, 3)
	ok := err == nil && ids(res.Rows) == "d3,d4,d5"
	check("deleted-cursor-continues", fmt.Sprintf("next=%s", ids(res.Rows)), ok)
}

func checkAudit() {
	s := dupScoreStore()
	pages := forwardAll(s, 3)
	universe := s.Snapshot()
	okGood := audit.Verify(pages, universe) == nil
	bad := append([][]row.Row{}, pages...)
	bad[1] = append(bad[1], bad[1][0]) // inject a duplicate row
	okBad := errors.Is(audit.Verify(bad, universe), audit.ErrMismatch)
	check("audit-selfcheck", fmt.Sprintf("clean-pass=%v dup-detected=%v", okGood, okBad),
		okGood && okBad)
}

func main() {
	checkDupScores()
	checkBackward()
	checkConcurrentMutations()
	checkCompareBound()
	checkBitFlip()
	checkCrossDirection()
	checkEmptyCursor()
	checkDeletedCursor()
	checkAudit()
	if failed {
		fmt.Println("TOTAL FAIL")
		os.Exit(1)
	}
	fmt.Println("TOTAL OK")
}
