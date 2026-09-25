package rnd_test

import (
	"fmt"
	"testing"

	"ontology/rnd"
)

// Fixed weight table prescribed by the task (treated as hash output).
var tableW = map[string]map[string]int{
	"k1": {"A": 10, "B": 7, "C": 3, "D": 11},
	"k2": {"A": 4, "B": 9, "C": 5, "D": 3},
	"k3": {"A": 6, "B": 6, "C": 2, "D": 6},
	"k4": {"A": 8, "B": 2, "C": 9, "D": 7},
}

// tableBest is the prescribed rule: max weight, ties -> smallest ID.
func tableBest(key string, nodes []string) string {
	best, bw := "", -1
	for _, n := range nodes {
		if x := tableW[key][n]; x > bw || (x == bw && n < best) {
			best, bw = n, x
		}
	}
	return best
}

func TestSixStepTable(t *testing.T) {
	keys, abc := []string{"k1", "k2", "k3", "k4"}, []string{"A", "B", "C"}
	// Steps 1-4: expected owners as each key joins.
	want := []map[string]string{
		{"k1": "A"},
		{"k1": "A", "k2": "B"},
		{"k1": "A", "k2": "B", "k3": "A"},
		{"k1": "A", "k2": "B", "k3": "A", "k4": "C"},
	}
	cur := map[string]string{}
	for step, k := range keys {
		cur[k] = tableBest(k, abc)
		for kk, w := range want[step] {
			if cur[kk] != w {
				t.Fatalf("step %d: %s = %s want %s", step+1, kk, cur[kk], w)
			}
		}
	}
	// Step 5 AddNode(D): exactly k1 migrates A->D (11 > 10); k3 ties at 6.
	abcd := []string{"A", "B", "C", "D"}
	var addMoves []string
	for _, k := range keys {
		if n := tableBest(k, abcd); n != cur[k] {
			addMoves = append(addMoves, k+":"+cur[k]+"->"+n)
			cur[k] = n
		}
	}
	if len(addMoves) != 1 || addMoves[0] != "k1:A->D" {
		t.Fatalf("step 5 moves = %v, want [k1:A->D]", addMoves)
	}
	// Step 6 RemoveNode(A): only A-owned k3 relocates; B=D=6 tie -> B.
	var rmMoves []string
	for _, k := range keys {
		if cur[k] == "A" {
			rmMoves = append(rmMoves, k+":A->"+tableBest(k, []string{"B", "C", "D"}))
		}
	}
	if len(rmMoves) != 1 || rmMoves[0] != "k3:A->B" {
		t.Fatalf("step 6 moves = %v, want [k3:A->B]", rmMoves)
	}
}

func TestWrongTieRules(t *testing.T) { // (甲)
	largest := func(key string, nodes []string) string {
		best, bw := "", -1
		for _, n := range nodes {
			if x := tableW[key][n]; x > bw || (x == bw && n > best) {
				best, bw = n, x
			}
		}
		return best
	}
	laterWins := func(key string, nodes []string) string { // >= : later scan overwrites
		best, bw := "", -1
		for _, n := range nodes {
			if x := tableW[key][n]; x >= bw {
				best, bw = n, x
			}
		}
		return best
	}
	if got := largest("k3", []string{"A", "B", "C"}); got != "B" {
		t.Fatalf("(甲) largest-ID tie break = %s, want B", got)
	}
	if got := laterWins("k3", []string{"A", "B", "C"}); got != "B" {
		t.Fatalf("(甲) later-scan-wins = %s, want B", got)
	}
	if got := tableBest("k3", []string{"A", "B", "C"}); got != "A" {
		t.Fatalf("correct rule = %s, want A", got)
	}
}

func TestModuloExtraMigrations(t *testing.T) { // (乙)
	hash := map[string]int{"k1": 10, "k2": 13, "k3": 11, "k4": 5}
	names := []string{"A", "B", "C", "D"}
	cases := []struct {
		key, want string
	}{
		{"k3", "k3:C->D"}, // 11 mod 3 = 2(C); 11 mod 4 = 3(D)
		{"k4", "k4:C->B"}, // 5 mod 3 = 2(C); 5 mod 4 = 1(B)
	}
	var extra []string
	for _, k := range []string{"k1", "k2", "k3", "k4"} {
		if a, b := hash[k]%3, hash[k]%4; a != b && k != "k1" {
			extra = append(extra, fmt.Sprintf("%s:%s->%s", k, names[a], names[b]))
		}
	}
	if len(extra) != len(cases) {
		t.Fatalf("extra modulo migrations = %v", extra)
	}
	for i, tc := range cases {
		if extra[i] != tc.want {
			t.Fatalf("got %s want %s", extra[i], tc.want)
		}
	}
}

func TestWeightDeterministic(t *testing.T) {
	for _, k := range []string{"k1", "k2", "k3"} {
		for _, n := range []string{"A", "B", "C", "D"} {
			w := rnd.Weight(k, n)
			for i := 0; i < 100; i++ {
				if rnd.Weight(k, n) != w {
					t.Fatalf("Weight(%s,%s) varies across calls", k, n)
				}
			}
		}
	}
	a, b := rnd.Weight("k3", "A"), rnd.Weight("k3", "B")
	if a == b { // sanity: distinct pairs need not collide in general
		t.Log("k3 weights for A and B happen to collide under FNV (allowed)")
	}
}
