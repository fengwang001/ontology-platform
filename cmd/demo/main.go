// Command demo exercises the per-group Top-N selector end to end and
// prints one OK/FAIL verdict line per scenario. Exit code is always 0.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology"
)

var passed, failed int

func check(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func cfg(n int) ontology.Config {
	return ontology.Config{GroupKey: "g", ScoreKey: "s", TieKey: "t", N: n}
}

func demoMemoryBound() {
	s, _ := ontology.New(cfg(5))
	for g := 0; g < 1000; g++ {
		gk := fmt.Sprintf("g%04d", g)
		for i := 0; i < 10000; i++ {
			s.Add(map[string]any{"g": gk, "s": float64(i), "t": "x"})
		}
	}
	held, groups := s.Stats()
	check(held <= groups*5 && s.Processed() == 10_000_000,
		"memory bound: held=%d groups=%d processed=%d", held, groups, s.Processed())
}

func demoSmallGroup() {
	s, _ := ontology.New(cfg(5))
	s.Add(map[string]any{"g": "a", "s": 2.0, "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "x"})
	rows := s.Snapshot()[0].Rows
	check(len(rows) == 2 && rows[0]["s"] == 2.0, "group smaller than N returns all %d rows", len(rows))
}

func demoBadN() {
	_, err := ontology.New(cfg(0))
	check(errors.Is(err, ontology.ErrNonPositiveN), "N<=0 rejected: %v", err)
}

func demoShuffleStable() {
	base := make([]map[string]any, 20)
	for i := range base {
		base[i] = map[string]any{"g": "a", "s": 7.5, "t": "same", "id": i}
	}
	top := func(rows []map[string]any) []map[string]any {
		s, _ := ontology.New(cfg(5))
		for _, r := range rows {
			s.Add(r)
		}
		return s.Snapshot()[0].Rows
	}
	rng := rand.New(rand.NewSource(1))
	shuf := func() []map[string]any {
		out := make([]map[string]any, 20)
		for i, j := range rng.Perm(20) {
			out[i] = base[j]
		}
		return out
	}
	check(reflect.DeepEqual(top(shuf()), top(shuf())),
		"full ties: two shuffles give identical Top-5")
}

func demoNullGroups() {
	s, _ := ontology.New(cfg(1))
	s.Add(map[string]any{"s": 1.0})
	s.Add(map[string]any{"g": nil, "s": 1.0})
	s.Add(map[string]any{"g": "", "s": 1.0})
	snap := s.Snapshot()
	ok := len(snap) == 3 &&
		snap[0].Group.Kind == ontology.KeyMissing &&
		snap[1].Group.Kind == ontology.KeyNull &&
		snap[2].Group.Kind == ontology.KeyValue && snap[2].Group.Value == ""
	check(ok, "null-ish keys split into 3 groups: %v %v %v",
		snap[0].Group, snap[1].Group, snap[2].Group)
}

func demoSkips() {
	s, _ := ontology.New(cfg(3))
	s.Add(map[string]any{"g": "a", "s": "bad"})
	s.Add(map[string]any{"g": "a", "s": math.NaN()})
	s.Add(map[string]any{"g": "a", "s": 1.0})
	nn, nan, _ := s.Skips(ontology.GroupKey{Kind: ontology.KeyValue, Value: "a"})
	check(nn == 1 && nan == 1, "skip counts: nonNumeric=%d nan=%d", nn, nan)
}

func demoInfinity() {
	s, _ := ontology.New(cfg(2))
	s.Add(map[string]any{"g": "a", "s": math.Inf(-1), "t": "x"})
	s.Add(map[string]any{"g": "a", "s": math.Inf(1), "t": "x"})
	rows := s.Snapshot()[0].Rows
	check(rows[0]["s"] == math.Inf(1) && rows[1]["s"] == math.Inf(-1),
		"infinities ranked: top=%v second=%v", rows[0]["s"], rows[1]["s"])
}

func main() {
	demoMemoryBound()
	demoSmallGroup()
	demoBadN()
	demoShuffleStable()
	demoNullGroups()
	demoSkips()
	demoInfinity()
	fmt.Printf("TOTAL passed=%d failed=%d\n", passed, failed)
}
