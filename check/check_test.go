package check

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"

	"ontology/dij"
	"ontology/graph"
)

func TestMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, cfg := range [][3]int{{1, 0, 0}, {5, 8, 2}, {60, 300, 3}} {
		for tr := 0; tr < 20; tr++ {
			batch := randEdges(rng, cfg[0], cfg[1])
			got, err := dij.ShortestPath(cfg[0], batch, cfg[2])
			if err != nil || !slices.Equal(got, Reference(cfg[0], batch, cfg[2])) {
				t.Fatalf("n=%d m=%d src=%d: %v, %v", cfg[0], cfg[1], cfg[2], got, err)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		n, src int
		edge   []graph.Edge
		want   error
	}{
		{2, 0, es([3]float64{0, 1, -1}), dij.ErrNegativeEdge},
		{2, -1, nil, dij.ErrBadSrc},
		{2, 2, nil, dij.ErrBadSrc},
		{2, 0, es([3]float64{2, 1, 1}), dij.ErrBadEdge},
	}
	for i, tc := range cases {
		if _, err := dij.ShortestPath(tc.n, tc.edge, tc.src); !errors.Is(err, tc.want) {
			t.Errorf("case %d: got %v, want %v", i, err, tc.want)
		}
	}
}

func TestStaleEntries(t *testing.T) {
	got, err := dij.ShortestPath(5, staleEdges, 0)
	if err != nil || !slices.Equal(got, staleWant) {
		t.Fatalf("dij = %v, %v; want %v", got, err, staleWant)
	}
	if bad := buggy(5, staleEdges, 0); slices.Equal(bad, staleWant) {
		t.Fatalf("buggy variant unexpectedly correct: %v", bad)
	}
}

func TestEdgeCasesAndDeterminism(t *testing.T) {
	inf := math.Inf(1)
	cases := []struct {
		n, src int
		edge   []graph.Edge
		want   []float64
	}{
		{1, 0, nil, []float64{0}},
		{3, 1, nil, []float64{inf, 0, inf}},
		{4, 0, es([3]float64{0, 1, 1}, [3]float64{0, 2, 1}, [3]float64{1, 3, 1}, [3]float64{2, 3, 1}), []float64{0, 1, 1, 2}},
	}
	for i, tc := range cases {
		first, err := dij.ShortestPath(tc.n, tc.edge, tc.src)
		if err != nil || !slices.Equal(first, tc.want) {
			t.Fatalf("case %d: %v, %v", i, first, err)
		}
		if got, _ := dij.ShortestPath(tc.n, tc.edge, tc.src); !slices.Equal(got, first) {
			t.Fatalf("case %d rerun: %v != %v", i, got, first)
		}
	}
}

func TestRelaxationBound(t *testing.T) {
	n, m := 1000, 5000
	batch := randEdges(rand.New(rand.NewSource(1)), n, m)
	before := dij.Relaxations()
	_, _ = dij.ShortestPath(n, batch, 0)
	if got := dij.Relaxations() - before; got > int64(m) {
		t.Fatalf("relaxations = %d, want <= %d", got, m)
	}
}

func TestConcurrent(t *testing.T) {
	done := make(chan []float64, 8)
	for i := 0; i < 8; i++ {
		go func() {
			got, _ := dij.ShortestPath(5, staleEdges, 0)
			done <- got
		}()
	}
	for i := 0; i < 8; i++ {
		if got := <-done; !slices.Equal(got, staleWant) {
			t.Errorf("got %v", got)
		}
	}
}
