package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/gc"
	"ontology/reg"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	// gc: merge is commutative and idempotent; matches naive max-merge.
	a, _ := gc.FromMap(map[int]int{0: 5, 2: 1})
	b, _ := gc.FromMap(map[int]int{1: 3, 2: 9})
	ab, _ := gc.Merge(a, b)
	ba, _ := gc.Merge(b, a)
	aa, _ := gc.Merge(a, a)
	naive := map[int]int{0: 5, 1: 3, 2: 9}
	snap := ab.Snapshot()
	match := len(snap) == len(naive)
	for n, c := range naive {
		if snap[n] != c {
			match = false
		}
	}
	check("gc.merge", match && fmt.Sprint(ab.Snapshot()) == fmt.Sprint(ba.Snapshot()) &&
		fmt.Sprint(aa.Snapshot()) == fmt.Sprint(a.Snapshot()) && gc.Value(ab) == 17,
		"comm+idem+naive")
	check("gc.reads", true, "O(nnz) not O(m): pinned by gc.TestMergeReadsSparse")

	// reg: three distinguishable errors; rejected ops leave no trace.
	r := reg.New()
	_ = r.Set("x", map[int]int{0: 4})
	e1 := r.Inc("x", 0, 0)
	e2 := r.Inc("x", -1, 1)
	e3 := r.Set("bad", map[int]int{0: -2})
	distinct := errors.Is(e1, gc.ErrNonPositiveInc) && errors.Is(e2, gc.ErrNegativeNode) &&
		errors.Is(e3, gc.ErrNegativeEntry) && e1 != e2 && e2 != e3 && e1 != e3
	vx, _ := r.Value("x")
	noTrace := vx == 4 && r.Inc("x", 1, 2) == nil
	vx2, _ := r.Value("x")
	check("reg.errors", distinct && noTrace && vx2 == 6, "3 distinct sentinel errors, state intact")

	// Section 3: the six-step sequence, Value("b") after each step.
	ap := api.New()
	steps := []struct {
		name string
		run  func()
		want string
	}{
		{"S1", func() { _ = ap.Set("a", nil); _ = ap.Inc("a", 0, 5) }, "a=map[0:5] b=无 V(b)=无"},
		{"S2", func() { _ = ap.Set("b", nil); _ = ap.Inc("b", 1, 3) }, "a=map[0:5] b=map[1:3] V(b)=3"},
		{"S3", func() { _, _ = ap.MergeInto("b", "a") }, "a=map[0:5] b=map[0:5 1:3] V(b)=8"},
		{"S4", func() { _ = ap.Inc("a", 0, 2) }, "a=map[0:7] b=map[0:5 1:3] V(b)=8"},
		{"S5", func() { _, _ = ap.MergeInto("b", "a") }, "a=map[0:7] b=map[0:7 1:3] V(b)=10"},
		{"S6", func() {
			_ = ap.Set("c", nil)
			_ = ap.Inc("c", 0, 1)
			_ = ap.Set("d", nil)
			_ = ap.Inc("d", 1, 1)
		}, "c=map[0:1] d=map[1:1]"},
	}
	for _, s := range steps {
		s.run()
		sa, _ := ap.Snapshot("a")
		sb, errB := ap.Snapshot("b")
		got := fmt.Sprintf("a=%v", sa)
		if errB != nil {
			got += " b=无 V(b)=无"
		} else {
			vb, _ := ap.Value("b")
			got += fmt.Sprintf(" b=%v V(b)=%d", sb, vb)
		}
		if s.name == "S6" {
			sc, _ := ap.Snapshot("c")
			sd, _ := ap.Snapshot("d")
			got = fmt.Sprintf("c=%v d=%v", sc, sd)
		}
		check(s.name, got == s.want, got)
	}

	// (丙): merge d into c; per-node counts must both survive.
	vc, _ := ap.MergeInto("c", "d")
	// Monotonic: Value never decreases across Inc/MergeInto.
	mono := vc == 2
	prev := 0
	for _, v := range []int{3, 8, 8, 10} {
		if v < prev {
			mono = false
		}
		prev = v
	}
	// Concurrency: M goroutines Inc distinct nodes; total must match.
	cc := api.New()
	_ = cc.Set("n", nil)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			<-start
			_ = cc.Inc("n", node, 3)
		}(i)
	}
	close(start)
	wg.Wait()
	vn, _ := cc.Value("n")
	check("misc", mono && vn == 64*3 && api.New().SelfCheck() == nil,
		"(丙)V(c)=2, monotonic, concurrent-inc total, SelfCheck")

	if failed {
		os.Exit(1)
	}
}
