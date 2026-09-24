// Command demo runs the budgeted graph walker acceptance checks.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"ontology/audit"
	"ontology/graph"
	"ontology/resume"
	"ontology/walk"
)

var failures int

func report(ok bool, label string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, label)
}

func main() {
	g := graph.New()
	g.AddEdge("b", "a")
	g.AddEdge("b", "c")
	g.AddEdge("a", "a")
	report(slices.Equal(g.Out("b"), []string{"a", "c"}) && g.Has("a") && !g.Has("zz"),
		"graph: adjacency sorted by target ID")

	walkChecks()
	resumeChecks()
	auditChecks()

	fmt.Printf("%s total %d check(s) failed\n", map[bool]string{true: "OK", false: "FAIL"}[failures == 0], failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func auditChecks() {
	g := sampleGraph()
	err := audit.Equivalent(g, "a", 5)
	report(err == nil, "audit: every split point matches single-run traversal")
}

func resumeChecks() {
	g := sampleGraph()
	seg, err := walk.Walk(g, walk.Initial("a"), 2)
	data := resume.Encode(seg.Next)
	back, derr := resume.Decode(data, g)
	ok := err == nil && derr == nil && len(back.Visited) == 2
	for i := range data {
		for bit := 0; bit < 8; bit++ {
			variant := append([]byte(nil), data...)
			variant[i] ^= 1 << bit
			_, derr := resume.Decode(variant, g)
			if derr == nil {
				ok = false
				continue
			}
			switch {
			case errors.Is(derr, resume.ErrIncomplete),
				errors.Is(derr, resume.ErrUnknownNode),
				errors.Is(derr, resume.ErrChecksum):
			default:
				ok = false
			}
		}
	}
	report(ok, "resume: all bit-flipped checkpoints rejected and classified")
}

// sampleGraph builds a graph with a cycle, a self-loop, a duplicate edge and
// an unreachable node: a->{b,c,b}, b->{d}, c->{d,e}, d->{a}, e->{e}, z.
func sampleGraph() *graph.Graph {
	g := graph.New()
	for _, e := range [][2]string{
		{"a", "b"}, {"a", "c"}, {"a", "b"}, {"b", "d"},
		{"c", "d"}, {"c", "e"}, {"d", "a"}, {"e", "e"},
	} {
		g.AddEdge(e[0], e[1])
	}
	g.AddNode("z")
	return g
}

func walkChecks() {
	g := sampleGraph()

	first, err := walk.Walk(g, walk.Initial("a"), 4)
	same := err == nil
	for i := 0; i < 20 && same; i++ {
		r, err := walk.Walk(g, walk.Initial("a"), 4)
		same = err == nil && slices.Equal(r.Visited, first.Visited)
	}
	report(same, "walk: 20 repeated runs visit identical sequences")

	zero, err := walk.Walk(g, walk.Initial("a"), 0)
	report(err == nil && len(zero.Visited) == 0 && slices.Equal(zero.Next.Ready, []string{"a"}) &&
		zero.Next.Pending == nil && zero.Next.Visited == nil, "walk: zero budget is idempotent")

	full, err := walk.Walk(g, walk.Initial("a"), 1<<30)
	again, err2 := walk.Walk(g, full.Next, 5)
	report(err == nil && full.Next.Done() && err2 == nil && len(again.Visited) == 0,
		"walk: done checkpoint resumes to empty sequence")

	reachable := len(full.Visited)
	exact := reachable == 5
	for budget := 0; budget <= reachable+2 && exact; budget++ {
		r, err := walk.Walk(g, walk.Initial("a"), budget)
		exact = err == nil && r.Stats.Visited == min(budget, reachable)
	}
	report(exact, "walk: visited count == min(budget, reachable)")

	star := graph.New()
	for i := 0; i < 10000; i++ {
		star.AddEdge("hub", fmt.Sprintf("leaf%05d", i))
	}
	sr, err := walk.Walk(star, walk.Initial("hub"), 100)
	report(err == nil && sr.Stats.PeakQueue <= 4*100,
		fmt.Sprintf("walk: star(10k) budget 100 peak queue %d <= 400", sr.Stats.PeakQueue))

	seen := map[string]bool{}
	dups := false
	for _, id := range full.Visited {
		dups = dups || seen[id]
		seen[id] = true
	}
	report(!dups && !seen["z"], "walk: cycle/self-loop/duplicate edge cause no re-visit")

	mut := sampleGraph()
	seg, err := walk.Walk(mut, walk.Initial("a"), 1)
	mut.RemoveNode("c")
	_, err = walk.Walk(mut, seg.Next, 10)
	report(err != nil && errors.Is(err, walk.ErrNodeGone) && strings.Contains(err.Error(), `"c"`),
		"walk: node deleted mid-resume is reported by name")
}
