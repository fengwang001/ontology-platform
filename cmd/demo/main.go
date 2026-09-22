// Command demo exercises every guarantee of the workflow orchestrator
// and prints one OK/FAIL line per guarantee. It reads no arguments,
// touches no network and no filesystem.
package main

import (
	"context"
	"fmt"
	"os"

	"ontology/graph"
	"ontology/step"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

func buildGraph(nodes []string, edges [][2]string) *graph.Graph {
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			panic(err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			panic(err)
		}
	}
	return g
}

func noop(id string) step.Step {
	return step.Step{ID: id, Run: func(context.Context) error { return nil }}
}

func main() {
	check("layered schedule runs one layer fully concurrent", checkLayeredConcurrency())
	check("cycle rejected with a real closed path", checkCyclePath())
	check("failure triggers reverse-topological compensation", checkReverseCompensation())
	check("compensation happens exactly once per succeeded step", checkCompensateOnce())
	check("compensation failures aggregate, others continue", checkCompensationAggregation())
	check("every crash point recovers without re-running done steps", checkCrashPoints())
	check("replaying one journal is idempotent", checkReplayIdempotent())
	check("torn half record is dropped, state stays consistent", checkTornRecord())
	check("N retries means exactly N+1 executions", checkRetryBoundary())
	check("replay node visits independent of trail length", checkReplayComplexity())
	check("three resource limits reject distinguishably", checkLimits())
	check("queries are stable and non-advancing", checkQueryStability())
	check("slow step does not block independent siblings", checkSlowStep())
	fmt.Printf("SUMMARY %d/%d checks passed\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
