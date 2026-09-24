package main

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/assign"
	"ontology/group"
)

func ok(pass bool) string {
	if pass {
		return "OK"
	}
	return "FAIL"
}

func main() {
	// assign package: the seven one-change batches from NOTES.md (n=7).
	st := assign.New(7)
	members := map[string]struct{}{}
	batches := []struct {
		id   string
		join bool
	}{
		{"b", true}, {"a", true}, {"c", true}, {"d", true},
		{"a", false}, {"e", true}, {"c", false},
	}
	wantMig := []int{0, 3, 2, 1, 2, 1, 2}
	wantFinal := map[string][]int{
		"b": {0, 1, 3}, "d": {2, 5}, "e": {4, 6},
	}
	pass, cum := true, 0
	for i, b := range batches {
		if b.join {
			members[b.id] = struct{}{}
		} else {
			delete(members, b.id)
		}
		ids := make([]string, 0, len(members))
		for id := range members {
			ids = append(ids, id)
		}
		m := st.Rebalance(ids)
		cum += m
		if m != wantMig[i] {
			pass = false
		}
	}
	held := st.Held()
	if cum != 11 || len(held) != len(wantFinal) {
		pass = false
	}
	for id, ps := range wantFinal {
		g := held[id]
		if len(g) != len(ps) {
			pass = false
		}
		for j := range ps {
			if g[j] != ps[j] {
				pass = false
			}
		}
	}
	fmt.Printf("seven-batch sticky: %s (cum=%d, final=%v)\n", ok(pass), cum, held)

	// group package: four distinct sentinel errors, rejection leaves no trace.
	g := group.New(7, 3)
	if _, err := g.Apply([]group.Change{group.Joining("x"), group.Joining("y")}); err != nil {
		pass = false
	}
	before := g.Assignment()
	gen := g.Generation()
	cases := []struct {
		batch []group.Change
		want  error
	}{
		{[]group.Change{group.Joining("")}, group.ErrEmptyID},
		{[]group.Change{group.Joining("x")}, group.ErrDuplicate},
		{[]group.Change{group.Joining("z"), group.Leaving("z")}, group.ErrDuplicate},
		{[]group.Change{group.Leaving("ghost")}, group.ErrAbsent},
		{[]group.Change{group.Joining("z"), group.Joining("w")}, group.ErrTooManyMember},
	}
	for _, c := range cases {
		if _, err := g.Apply(c.batch); !errors.Is(err, c.want) {
			pass = false
		}
	}
	if g.Generation() != gen || fmt.Sprint(g.Assignment()) != fmt.Sprint(before) {
		pass = false
	}
	fmt.Printf("group errors + no-trace: %s\n", ok(pass))

	// api package: SelfCheck must pass on a fresh instance.
	err := api.New(7, 8).SelfCheck()
	fmt.Printf("api SelfCheck: %s (err=%v)\n", ok(err == nil), err)

	// assign package: partition touches must not grow linearly with m.
	fmt.Printf("bounded partition touches: %s\n", ok(assign.BoundedCheck()))
}
