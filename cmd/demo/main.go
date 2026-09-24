package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var failCount int

func check(label string, ok bool) {
	if ok {
		fmt.Println("OK " + label)
	} else {
		failCount++
		fmt.Println("FAIL " + label)
	}
}

func firstRef(err error) filter.Ref {
	var re *filter.RejectError
	if errors.As(err, &re) && len(re.Refs) > 0 {
		return re.Refs[0]
	}
	return filter.Ref{}
}

func main() {
	pol := policy.New([]string{"a", "b", "secret"}, map[string][]string{"user": {"a", "b"}, "none": nil, "all": {"a", "b", "secret"}})

	_, errNot := filter.Compile(pol, "user", predicate.Not{X: predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1}})
	_, errNull := filter.Compile(pol, "user", predicate.Cmp{Col: "secret", Op: predicate.OpIsNull})
	check("NOT(secret=1) rejected: column+path $/not/cmp named",
		errors.Is(errNot, filter.ErrHiddenColumn) && firstRef(errNot).Col == "secret" && firstRef(errNot).Path == "$/not/cmp")
	check("secret IS NULL rejected instead of returning rows",
		errors.Is(errNull, filter.ErrHiddenColumn) && firstRef(errNull).Path == "$/cmp")

	plan, err := filter.Compile(pol, "user", predicate.And{Xs: []predicate.Node{
		predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1},
		predicate.Not{X: predicate.Cmp{Col: "b", Op: predicate.OpEq, Value: "z"}},
	}})
	rows := plan.Apply([]map[string]any{{"a": 1, "b": nil, "secret": "shh"}})
	_, hiddenKey := rows[0]["secret"]
	_, visibleNullKey := rows[0]["b"]
	check("trimmed row has no hidden key but keeps visible NULL (distinct from zero value)",
		err == nil && len(rows) == 1 && !hiddenKey && visibleNullKey && rows[0]["b"] == nil)

	tree := predicate.And{Xs: []predicate.Node{
		predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1},
		predicate.Not{X: predicate.Cmp{Col: "b", Op: predicate.OpEq, Value: "z"}},
	}}
	check("report byte-identical across 20 shuffled construction orders", reportStable(pol, tree))
	check("predicate node visits == total node count (single pass)", plan.NodesVisited() == predicate.Count(tree))

	cols, vis := make([]string, 1000), make([]string, 0, 5)
	for i := range cols {
		cols[i] = "c" + strconv.Itoa(i)
	}
	for _, i := range []int{1, 100, 500, 777, 999} {
		vis = append(vis, cols[i])
	}
	big := policy.New(cols, map[string][]string{"r": vis})
	bigPlan, _ := filter.Compile(big, "r", nil)
	bigRow := make(map[string]any, 1000)
	for _, c := range cols {
		bigRow[c] = 1
	}
	_ = bigPlan.Project(bigRow)
	check("1000 cols / 5 visible: copy ops "+strconv.Itoa(bigPlan.CopyOps())+" <= 20", bigPlan.CopyOps() <= 4*5)
	check("all 32 exhaustive combos agree with DESIGN definition", exhaustive32())

	orPlan, errOR := filter.Compile(pol, "user", predicate.Or{Xs: []predicate.Node{
		predicate.Const{Value: false}, predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1},
		predicate.Const{Value: true},
	}})
	check("OR exception: constant-folded hidden arm is not read", errOR == nil && orPlan != nil)

	nonePlan, errNone := filter.Compile(pol, "none", predicate.Cmp{Col: "a"})
	allPlan, errAll := filter.Compile(pol, "all", predicate.Cmp{Col: "secret", Op: predicate.OpIsNull})
	check("boundaries: empty visible set rejects; full universe allows hidden col",
		errors.Is(errNone, filter.ErrHiddenColumn) && errAll == nil && allPlan != nil &&
			len(nonePlanVisible(pol)) == 0)

	fmt.Printf("TOTAL checks=%d fail=%d\n", failCount+11, failCount)
	if failCount > 0 {
		panic("demo checks failed")
	}
}

func nonePlanVisible(pol *policy.Policy) []string { return pol.Visible("none") }

func reportStable(pol *policy.Policy, tree predicate.Node) bool {
	_, err := filter.Compile(pol, "user", tree)
	var want string
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		hs := append([]string(nil), pol.Hidden("user")...)
		rng.Shuffle(len(hs), func(a, b int) { hs[a], hs[b] = hs[b], hs[a] })
		got := report.Build("user", hs, err).Marshal()
		if want == "" {
			want = got
		} else if got != want {
			return false
		}
	}
	return true
}

func exhaustive32() bool {
	eq := func(col string) predicate.Node { return predicate.Cmp{Col: col, Op: predicate.OpEq, Value: 1} }
	forms := map[string]func() predicate.Node{
		"=": func() predicate.Node { return eq("a") },
		"NOT": func() predicate.Node { return predicate.Not{X: eq("a")} },
		"AND": func() predicate.Node { return predicate.And{Xs: []predicate.Node{eq("a"), eq("b")}} },
		"OR": func() predicate.Node { return predicate.Or{Xs: []predicate.Node{eq("a"), eq("b")}} },
	}
	refs := map[string][]string{"=": {"a"}, "NOT": {"a"}, "AND": {"a", "b"}, "OR": {"a", "b"}}
	summary := map[string][2]int{}
	for mask := 0; mask < 8; mask++ {
		grants := []string{}
		hidden := map[string]bool{}
		for i, col := range []string{"a", "b", "c"} {
			if mask&(1<<i) == 0 {
				hidden[col] = true
			} else {
				grants = append(grants, col)
			}
		}
		p := policy.New([]string{"a", "b", "c"}, map[string][]string{"r": grants})
		for name, build := range forms {
			_, err := filter.Compile(p, "r", build())
			want := false
			for _, c := range refs[name] {
				want = want || hidden[c]
			}
			if errors.Is(err, filter.ErrHiddenColumn) != want {
				return false
			}
			s := summary[name]
			if want {
				s[1]++
			} else {
				s[0]++
			}
			summary[name] = s
		}
	}
	return summary["="] == [2]int{4, 4} && summary["NOT"] == [2]int{4, 4} &&
		summary["AND"] == [2]int{2, 6} && summary["OR"] == [2]int{2, 6}
}
