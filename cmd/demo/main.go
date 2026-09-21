// demo 实际演练分组内 Top-N 选择器的各项语义并逐条打印判定。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology"
)

var passed, failed int

func report(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func cfg(n int) ontology.Config {
	return ontology.Config{N: n, GroupColumn: "g", ScoreColumn: "s", TieColumn: "t"}
}

func demoMemoryBound() {
	s, _ := ontology.New(cfg(5))
	row := map[string]any{"g": "", "s": 0.0, "t": "x"}
	boundOK := true
	for g := 0; g < 1000; g++ {
		row["g"] = fmt.Sprintf("g%04d", g)
		for i := 0; i < 10000; i++ {
			row["s"] = float64(i)
			s.Add(row)
		}
		held, groups := s.Stats()
		if held > groups*5 {
			boundOK = false
		}
	}
	held, groups := s.Stats()
	report(boundOK && held == 5000 && groups == 1000,
		"memory: 1000 groups x 10000 rows, N=5 -> held=%d (bound %d), processed=%d",
		held, groups*5, s.Processed())
}

func demoSmallGroup() {
	s, _ := ontology.New(cfg(5))
	for i := 0; i < 3; i++ {
		s.Add(map[string]any{"g": "a", "s": float64(i), "t": "x"})
	}
	got := len(s.Snapshot()[0].Rows)
	report(got == 3, "small group: 3 rows with N=5 -> returned %d (all)", got)
}

func demoInvalidN() {
	_, err := ontology.New(cfg(0))
	report(errors.Is(err, ontology.ErrInvalidN), "invalid N: N=0 -> %v", err)
}

func demoShuffleDeterministic() {
	base := []map[string]any{
		{"g": "a", "s": 7.0, "t": "same", "p": "r1"},
		{"g": "a", "s": 7.0, "t": "same", "p": "r2"},
		{"g": "a", "s": 7.0, "t": "same", "p": "r3"},
		{"g": "a", "s": 7.0, "t": "same", "p": "r4"},
		{"g": "a", "s": 7.0, "t": "same", "p": "r5"},
	}
	topIDs := func(seed int64) []string {
		rows := append([]map[string]any(nil), base...)
		rand.New(rand.NewSource(seed)).Shuffle(len(rows), func(i, j int) {
			rows[i], rows[j] = rows[j], rows[i]
		})
		s, _ := ontology.New(cfg(3))
		for _, r := range rows {
			s.Add(r)
		}
		var ids []string
		for _, gs := range s.Snapshot() {
			for i := range gs.Rows {
				ids = append(ids, gs.Rows[i].ID())
			}
		}
		return ids
	}
	a, b := topIDs(1), topIDs(2)
	same := len(a) == len(b)
	for i := range a {
		if same && a[i] != b[i] {
			same = false
		}
	}
	report(same, "shuffle: all-tied rows shuffled twice -> identical top-3 (%v)", same)
}

func demoEmptyKeys() {
	s, _ := ontology.New(cfg(1))
	s.Add(map[string]any{"s": 1.0, "t": "a"})
	s.Add(map[string]any{"g": nil, "s": 2.0, "t": "b"})
	s.Add(map[string]any{"g": "", "s": 3.0, "t": "c"})
	snap := s.Snapshot()
	ok := len(snap) == 3 &&
		snap[0].Key.Class == ontology.KeyMissing &&
		snap[1].Key.Class == ontology.KeyNil &&
		snap[2].Key.Class == ontology.KeyEmpty
	report(ok, "empty keys: missing/nil/empty -> 3 groups [%s %s %s]",
		snap[0].Key, snap[1].Key, snap[2].Key)
}

func demoSkipCounts() {
	s, _ := ontology.New(cfg(3))
	s.Add(map[string]any{"g": "a", "t": "x"})
	s.Add(map[string]any{"g": "a", "s": "bad", "t": "x"})
	s.Add(map[string]any{"g": "a", "s": math.NaN(), "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "x"})
	g := s.Snapshot()[0]
	report(g.Skipped == 2 && g.NaN == 1 && len(g.Rows) == 1,
		"skip counts: skipped=%d nan=%d ranked=%d", g.Skipped, g.NaN, len(g.Rows))
}

func demoInfinity() {
	s, _ := ontology.New(cfg(2))
	s.Add(map[string]any{"g": "a", "s": math.Inf(-1), "t": "neg"})
	s.Add(map[string]any{"g": "a", "s": math.Inf(1), "t": "pos"})
	s.Add(map[string]any{"g": "a", "s": 1e300, "t": "big"})
	rows := s.Snapshot()[0].Rows
	ok := math.IsInf(rows[0].Score, 1) && rows[1].Score == 1e300
	report(ok, "infinity: top-2 scores = [%v %v]", rows[0].Score, rows[1].Score)
}

func main() {
	demoMemoryBound()
	demoSmallGroup()
	demoInvalidN()
	demoShuffleDeterministic()
	demoEmptyKeys()
	demoSkipCounts()
	demoInfinity()
	fmt.Printf("total: %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
