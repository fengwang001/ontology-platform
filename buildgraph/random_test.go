package buildgraph

import (
	"math/rand"
	"sort"
	"testing"
)

// randomGraph builds a DAG in topological insertion order: edges e0..e(n-1),
// each may reference earlier edge outputs or fresh source paths, distributed
// over explicit / implicit / order-only inputs. Returns the graph and target
// outputs. The graph state is then randomized: files may be missing, mtimes
// randomized, and each edge may or may not have a matching log entry.
func randomGraph(r *rand.Rand, n int) (*Graph, []string, [][]string) {
	g := New()
	outputs := make([]string, n)
	allInputs := make([][]string, n)
	for i := 0; i < n; i++ {
		id := edgeName(i)
		out := id + ".out"
		outputs[i] = out

		var explicit, implicit, orderOnly []string
		addInputs := func(dst *[]string, count int, allowOuts bool) {
			for k := 0; k < count; k++ {
				if allowOuts && i > 0 && r.Intn(2) == 0 {
					j := r.Intn(i)
					*dst = append(*dst, outputs[j])
				} else {
					src := "src" + itoa(r.Intn(n+2))
					*dst = append(*dst, src)
				}
			}
		}
		addInputs(&explicit, r.Intn(4), true)
		addInputs(&implicit, r.Intn(3), true)
		addInputs(&orderOnly, r.Intn(3), true)
		// Occasional duplicates across/within lists are legal.
		if i > 0 && r.Intn(4) == 0 {
			explicit = append(explicit, outputs[r.Intn(i)])
		}
		allInputs[i] = append(append(append([]string{}, explicit...), implicit...), orderOnly...)

		cmd := "cmd"
		if r.Intn(3) == 0 {
			cmd = "cmd-variant"
		}
		if err := g.AddEdge(id, cmd, []string{out}, explicit, implicit, orderOnly, r.Intn(2) == 0); err != nil {
			panic("random graph add: " + err.Error())
		}
	}

	// Collect every path and assign randomized existence/mtimes.
	paths := map[string]bool{}
	for _, ins := range allInputs {
		for _, p := range ins {
			paths[p] = true
		}
	}
	for _, p := range outputs {
		paths[p] = true
	}
	for p := range paths {
		if r.Intn(10) == 0 {
			continue // missing file
		}
		g.mtimes[p] = int64(r.Intn(20) + 1)
	}

	// Random log entries: only when all explicit/implicit inputs exist;
	// command may or may not match the current one.
	for i := 0; i < n; i++ {
		e := g.edges[edgeName(i)]
		if r.Intn(2) == 0 {
			continue
		}
		ok := true
		for _, list := range [][]string{e.explicit, e.implicit} {
			for _, p := range list {
				if _, exists := g.mtimes[p]; !exists {
					ok = false
				}
			}
		}
		if !ok {
			continue
		}
		cmd := e.cmd
		if r.Intn(4) == 0 {
			cmd = "stale-command"
		}
		g.logs[e.id] = logEntry{cmd: cmd, inMax: int64(r.Intn(20))}
		for _, p := range e.outputs {
			if _, exists := g.mtimes[p]; !exists && r.Intn(2) == 0 {
				g.mtimes[p] = int64(r.Intn(20) + 1)
			}
		}
	}

	var targets []string
	for k := 0; k < 1+r.Intn(3); k++ {
		targets = append(targets, outputs[r.Intn(n)])
	}
	if r.Intn(5) == 0 {
		targets = append(targets, "totally-unknown-"+itoa(r.Intn(3)))
	}
	return g, targets, allInputs
}

func edgeName(i int) string {
	// Fixed-width ids keep byte order aligned with numeric order in small graphs.
	return "e" + padID(i)
}

func padID(i int) string {
	const width = 4
	s := itoa(i)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	var skipped, compared int
	for tc := 0; tc < cases; tc++ {
		r := rand.New(rand.NewSource(int64(tc) + 1))
		n := 1 + r.Intn(12)
		g, targets, allInputs := randomGraph(r, n)

		got, gerr := g.DirtySet(targets)
		naive, nerr := g.NaiveDirtySet(targets)
		if (gerr != nil) != (nerr != nil) {
			t.Fatalf("case %d error mismatch: impl=%v naive=%v", tc, gerr, nerr)
		}
		if gerr != nil {
			if gerr.Error() != nerr.Error() {
				t.Fatalf("case %d error detail mismatch: %v vs %v", tc, gerr, nerr)
			}
			skipped++
			continue
		}

		var want []string
		for id, reason := range naive {
			if reason != "" {
				want = append(want, id)
			}
		}
		sort.Strings(want)

		t.Logf("case=%d edges=%d targets=%v dirty=%v", tc, n, targets, got)
		for _, id := range want {
			e := g.edges[id]
			t.Logf("  edge %s restat=%v cmd=%q log=%+v inputs=%v outputs=%v reason=%q",
				id, e.restat, e.cmd, g.logs[id],
				append(append([]string{}, e.explicit...), append(e.implicit, e.orderOnly...)...),
				e.outputs, naive[id])
		}

		if len(got) != len(want) {
			t.Fatalf("case %d dirty mismatch:\n impl=%v\n naive=%v", tc, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("case %d dirty mismatch at %d:\n impl=%v\n naive=%v", tc, i, got, want)
			}
		}

		// Eval count must equal closure size.
		closure := closureSize(g, targets)
		if g.EvalCount() != closure {
			t.Fatalf("case %d eval count %d != closure %d", tc, g.EvalCount(), closure)
		}
		compared++
		_ = allInputs
	}
	t.Logf("differential: compared=%d skipped(missing/error)=%d", compared, skipped)
}

func closureSize(g *Graph, targets []string) int {
	seen := map[string]bool{}
	var stack []string
	for _, p := range targets {
		if id, ok := g.producer[p]; ok && !seen[id] {
			seen[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		e := g.edges[id]
		for _, list := range [][]string{e.explicit, e.implicit, e.orderOnly} {
			for _, p := range list {
				if pid, ok := g.producer[p]; ok && !seen[pid] {
					seen[pid] = true
					stack = append(stack, pid)
				}
			}
		}
	}
	return len(seen)
}

func TestReverifyAfterComplete(t *testing.T) {
	// Re-run DirtySet after completing dirty edges (re-check loop): converges
	// to clean and agrees with the naive evaluator at every round.
	r := rand.New(rand.NewSource(42))
	g, targets, _ := randomGraph(r, 8)
	// Remove missing files in closure by creating them, then complete iteratively.
	for round := 0; round < 50; round++ {
		dirty, err := g.DirtySet(targets)
		if err != nil {
			ms, ok := err.(*MissingSourceError)
			if !ok {
				t.Fatal(err)
			}
			if err := g.SetMtime(ms.Path, 1); err != nil {
				t.Fatal(err)
			}
			continue
		}
		naive, err := g.NaiveDirtySet(targets)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range dirty {
			if naive[id] == "" {
				t.Fatalf("round %d: %s dirty but naive clean", round, id)
			}
		}
		if len(dirty) == 0 {
			return
		}
		for _, id := range dirty {
			e := g.edges[id]
			missing := false
			for _, list := range [][]string{e.explicit, e.implicit} {
				for _, p := range list {
					if _, ok := g.mtimes[p]; !ok {
						missing = true
					}
				}
			}
			if missing {
				continue
			}
			outs := map[string]int64{}
			for _, p := range e.outputs {
				outs[p] = g.inputMax(e) + 1
			}
			if err := g.Complete(id, outs); err != nil {
				t.Fatalf("complete %s: %v", id, err)
			}
		}
	}
	t.Fatal("did not converge to clean")
}
