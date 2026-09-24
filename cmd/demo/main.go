// Command demo exercises the attribute-level permission filter end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var passed, total int

func check(ok bool, name, detail string) {
	total++
	status := "OK  "
	if !ok {
		status = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func rowString(row map[string]any) string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, row[k])
	}
	return b.String()
}

func main() {
	pol := policy.New(map[string][]string{
		"analyst": {"name", "dept"},
		"admin":   {"name", "dept", "salary", "secret"},
		"empty":   {},
	})
	eng := filter.NewEngine(pol)

	check(pol.Visible("analyst", "name") && !pol.Visible("analyst", "salary") &&
		pol.Visible("admin", "secret") && !pol.HasRole("ghost"),
		"policy: role->columns lookup", "analyst={name,dept} admin=full")

	folded := predicate.Fold(predicate.OrOf(
		predicate.Compare("secret", predicate.Eq, 1), predicate.True()))
	check(folded.Kind == predicate.Const && folded.Bool &&
		predicate.NodeCount(predicate.AndOf(
			predicate.Compare("a", predicate.Eq, 1),
			predicate.Compare("b", predicate.Eq, 2))) == 3,
		"predicate: fold absorbs unread OR branch", "secret=1 OR true -> const(true)")

	refs, err := eng.Check("analyst", predicate.NotOf(predicate.Compare("secret", predicate.Eq, 1)))
	check(errors.Is(err, filter.ErrInvisibleColumn) && len(refs) == 1 &&
		refs[0].Col == "secret" && refs[0].Paths[0] == "NOT/CMP",
		"filter: NOT(secret=1) rejected", "col=secret path=NOT/CMP")

	_, err = eng.Check("analyst", predicate.Compare("secret", predicate.IsNull, nil))
	check(errors.Is(err, filter.ErrInvisibleColumn), "filter: secret IS NULL rejected", "no rows returned")

	row := map[string]any{"name": "alice", "dept": "eng", "salary": 100, "secret": nil}
	kept, pruned, _ := eng.PruneRow("analyst", row)
	zeroFilled := map[string]any{"name": "alice", "dept": "eng", "salary": nil, "secret": nil}
	check(len(kept) == 2 && strings.Join(pruned, ",") == "salary,secret" &&
		rowString(kept) != rowString(zeroFilled),
		"filter: pruned row drops hidden cols", "kept={name,dept}, distinct from zero-filled")

	p := predicate.AndOf(
		predicate.Compare("name", predicate.Eq, "x"),
		predicate.OrOf(predicate.Compare("dept", predicate.Eq, "y"),
			predicate.NotOf(predicate.Compare("dept", predicate.Eq, "z"))))
	_, err = eng.Check("analyst", p)
	check(err == nil && eng.Visited() == predicate.NodeCount(predicate.Fold(p)),
		"filter: single traversal", fmt.Sprintf("visited=%d nodes=%d", eng.Visited(), predicate.NodeCount(predicate.Fold(p))))

	wide := map[string]any{}
	for i := 0; i < 1000; i++ {
		wide[fmt.Sprintf("c%04d", i)] = i
	}
	narrow := filter.NewEngine(policy.New(map[string][]string{"r": {"c0000", "c0001", "c0002", "c0003", "c0004"}}))
	kept, _, _ = narrow.PruneRow("r", wide)
	check(len(kept) == 5 && narrow.Copies() <= 4*5,
		"filter: prune cost O(visible)", fmt.Sprintf("copies=%d bound=%d (1000 cols, 5 visible)", narrow.Copies(), 4*5))

	mismatch := 0
	for mask := 0; mask < 8; mask++ {
		var vis []string
		for i, c := range []string{"a", "b", "c"} {
			if mask&(1<<i) != 0 {
				vis = append(vis, c)
			}
		}
		e := filter.NewEngine(policy.New(map[string][]string{"r": vis}))
		forms := []*predicate.Node{
			predicate.Compare("a", predicate.Eq, 1),
			predicate.NotOf(predicate.Compare("a", predicate.Eq, 1)),
			predicate.AndOf(predicate.Compare("a", predicate.Eq, 1), predicate.Compare("b", predicate.Eq, 2), predicate.Compare("c", predicate.Eq, 3)),
			predicate.OrOf(predicate.Compare("a", predicate.Eq, 1), predicate.Compare("b", predicate.Eq, 2), predicate.Compare("c", predicate.Eq, 3)),
		}
		used := [][]string{{"a"}, {"a"}, {"a", "b", "c"}, {"a", "b", "c"}}
		for f, form := range forms {
			wantReject := false
			for _, c := range used[f] {
				seen := false
				for _, v := range vis {
					if v == c {
						seen = true
					}
				}
				if !seen {
					wantReject = true
				}
			}
			_, err := e.Check("r", form)
			if errors.Is(err, filter.ErrInvisibleColumn) != wantReject {
				mismatch++
			}
		}
	}
	check(mismatch == 0, "filter: exhaustive 8x4 combos match definition", "32/32 as documented")

	_, err = eng.Check("empty", predicate.Compare("name", predicate.Eq, 1))
	keptE, prunedE, _ := eng.PruneRow("empty", row)
	check(errors.Is(err, filter.ErrInvisibleColumn) && len(keptE) == 0 && len(prunedE) == 4,
		"filter: empty visible set", "any predicate rejected, row pruned to nothing")

	_, err = eng.Check("admin", predicate.Compare("secret", predicate.Eq, 1))
	keptA, prunedA, _ := eng.PruneRow("admin", row)
	check(err == nil && len(keptA) == 4 && len(prunedA) == 0,
		"filter: full visible set", "predicate allowed, row kept whole")

	cols := []string{"salary", "secret", "ssn", "bonus"}
	paths := []string{"CMP", "AND[0]/CMP", "OR[1]/NOT/CMP"}
	base := ""
	same := true
	for trial := 0; trial < 20; trial++ {
		rep := report.New()
		pc := append([]string(nil), cols...)
		rand.Shuffle(len(pc), func(i, j int) { pc[i], pc[j] = pc[j], pc[i] })
		rep.AddPruned(pc...)
		pp := append([]string(nil), paths...)
		rand.Shuffle(len(pp), func(i, j int) { pp[i], pp[j] = pp[j], pp[i] })
		for _, pth := range pp {
			rep.AddRef("secret", pth)
		}
		rep.SetRejected(true)
		if trial == 0 {
			base = rep.String()
		} else if rep.String() != base {
			same = false
		}
	}
	check(same, "report: byte-identical across 20 shuffles", "sorted, deduped, order-free")

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
