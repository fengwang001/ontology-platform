package graph

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestValidationReportsOnlyFirstOrderedError(t *testing.T) {
	store := NewGraph()
	store.AddObject("a")
	snapshot := store.Snapshot()

	cases := []struct {
		name string
		req  TraverseRequest
		want error
	}{
		{name: "start missing", req: TraverseRequest{StartID: "missing", MaxDepth: 0, ResultLimit: 0, Directions: nil}, want: ErrStartNotFound},
		{name: "depth", req: TraverseRequest{StartID: "a", MaxDepth: 0, ResultLimit: 0, Directions: nil}, want: ErrInvalidMaxDepth},
		{name: "limit", req: TraverseRequest{StartID: "a", MaxDepth: 1, ResultLimit: 0, Directions: nil}, want: ErrInvalidResultLimit},
		{name: "directions", req: TraverseRequest{StartID: "a", MaxDepth: 1, ResultLimit: 1, Directions: nil}, want: ErrEmptyLinkDirections},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := snapshot.Traverse(tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDepthBoundaryIncludesPathAtExactHopCount(t *testing.T) {
	store := chainGraph("a", "b", "c")
	result, err := store.Snapshot().Traverse(TraverseRequest{
		StartID:     "a",
		MaxDepth:    2,
		ResultLimit: 10,
		Directions:  []Direction{Outgoing},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := pathIDs(result.Paths); !reflect.DeepEqual(got, [][]string{{"a"}, {"a", "b"}, {"a", "b", "c"}}) {
		t.Fatalf("paths = %v", got)
	}
	if len(result.Truncated) != 0 {
		t.Fatalf("complete leaf at exact depth must not be depth-truncated, got %v", reasons(result.Truncated))
	}
}

func TestResultLimitBoundaryStopsAtExactCompletion(t *testing.T) {
	store := chainGraph("a", "b", "c")
	result, err := store.Snapshot().Traverse(TraverseRequest{
		StartID:     "a",
		MaxDepth:    2,
		ResultLimit: 2,
		Directions:  []Direction{Outgoing},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := pathIDs(result.Paths); !reflect.DeepEqual(got, [][]string{{"a"}, {"a", "b"}}) {
		t.Fatalf("paths = %v", got)
	}
	if got := reasons(result.Truncated); !reflect.DeepEqual(got, []TruncationReason{LimitOnly, LimitOnly}) {
		t.Fatalf("reasons = %v", got)
	}
}

func TestAllFourTruncationReasonsAppearOnce(t *testing.T) {
	store := crossReasonGraph()
	result, err := store.Snapshot().Traverse(TraverseRequest{
		StartID:     "a",
		MaxDepth:    3,
		ResultLimit: 5,
		Directions:  []Direction{Outgoing},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := reasons(result.Truncated)
	want := []TruncationReason{DepthOnly, DepthBeforeLimit, LimitBeforeDepth, LimitOnly}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons = %v, want %v; paths=%v", got, want, pathIDs(result.Paths))
	}
}

func TestEachTruncationReasonCanAppearIndividually(t *testing.T) {
	cases := []struct {
		name     string
		depth    int
		limit    int
		expected TruncationReason
		graph    func() *Graph
	}{
		{name: "depth only", depth: 3, limit: 10, expected: DepthOnly, graph: depthOnlyGraph},
		{name: "limit only", depth: 4, limit: 2, expected: LimitOnly, graph: crossReasonGraph},
		{name: "depth before limit", depth: 3, limit: 4, expected: DepthBeforeLimit, graph: func() *Graph { return chainGraph("a", "b", "c", "d", "e") }},
		{name: "limit before depth", depth: 2, limit: 3, expected: LimitBeforeDepth, graph: limitBeforeDepthGraph},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.graph().Snapshot().Traverse(TraverseRequest{
				StartID:     "a",
				MaxDepth:    tc.depth,
				ResultLimit: tc.limit,
				Directions:  []Direction{Outgoing},
			})
			if err != nil {
				t.Fatal(err)
			}
			got := reasons(result.Truncated)
			if len(got) == 0 {
				t.Fatalf("expected truncation %s, got none; paths=%v", tc.expected, pathIDs(result.Paths))
			}
			if !containsReason(got, tc.expected) {
				t.Fatalf("reasons = %v, expected %s to appear; paths=%v", got, tc.expected, pathIDs(result.Paths))
			}
		})
	}
}

func TestRepeatedExecutionIsIdentical(t *testing.T) {
	store := crossReasonGraph()
	req := TraverseRequest{StartID: "a", MaxDepth: 3, ResultLimit: 5, Directions: []Direction{Outgoing}}

	first, err := store.Snapshot().Traverse(req)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 20; run++ {
		next, err := store.Snapshot().Traverse(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("run %d differs: %#v vs %#v", run, first, next)
		}
	}
}

func TestSnapshotIgnoresConcurrentGraphModifications(t *testing.T) {
	store := chainGraph("a", "b", "c")
	snapshot := store.Snapshot()
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 50; i++ {
				store.AddLink(Link{FromID: "a", ToID: fmt.Sprintf("new-%d-%d", worker, i), Type: "added"})
			}
		}(worker)
	}

	result, err := snapshot.Traverse(TraverseRequest{
		StartID:     "a",
		MaxDepth:    2,
		ResultLimit: 10,
		Directions:  []Direction{Outgoing},
	})
	workers.Wait()
	if err != nil {
		t.Fatal(err)
	}

	wantIDs := [][]string{{"a"}, {"a", "b"}, {"a", "b", "c"}}
	if got := pathIDs(result.Paths); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("snapshot paths = %v, want %v", got, wantIDs)
	}
}

func TestRandomGraphsMatchIndependentNaiveTraversal(t *testing.T) {
	rng := rand.New(rand.NewPCG(1666, 4242))
	for iteration := 0; iteration < 500; iteration++ {
		store := randomGraph(rng)
		snapshot := store.Snapshot()
		startID := randomStartID(rng)
		maxDepth := rng.IntN(4) + 1
		limit := rng.IntN(18) + 1
		directions := randomDirections(rng)

		got, err := snapshot.Traverse(TraverseRequest{
			StartID:     startID,
			MaxDepth:    maxDepth,
			ResultLimit: limit,
			Directions:  directions,
		})
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		wantPaths, wantTruncated := naiveTraverse(snapshot.state, startID, maxDepth, limit, normalizeDirections(directions))
		if !reflect.DeepEqual(got.Paths, wantPaths) {
			t.Fatalf("iteration %d paths differ\n got %v\nwant %v", iteration, pathIDs(got.Paths), pathIDs(wantPaths))
		}
		if !reflect.DeepEqual(got.Truncated, wantTruncated) {
			t.Fatalf("iteration %d truncation differs\n got %v\nwant %v", iteration, got.Truncated, wantTruncated)
		}
	}
}

func TestTraversalLogsLimitsPathsAndTruncationBasis(t *testing.T) {
	store := crossReasonGraph()
	var lines []string
	_, err := store.Snapshot().Traverse(TraverseRequest{
		StartID:     "a",
		MaxDepth:    3,
		ResultLimit: 5,
		Directions:  []Direction{Outgoing},
		Logger:      func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatal(err)
	}

	text := strings.Join(lines, "\n")
	for _, fragment := range []string{"max_depth=3", "result_limit=5", "returned=true", "truncation=depth_before_limit", "reason=limit_before_depth", "basis="} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("log missing %q:\n%s", fragment, text)
		}
	}
}

func BenchmarkResultCounterReachedDoesNotGrowWithLimit(b *testing.B) {
	for _, limit := range []int{1, 16, 256, 4096} {
		b.Run(fmt.Sprintf("limit=%d", limit), func(b *testing.B) {
			counter := newResultCounter(limit)
			for i := 0; i < limit; i++ {
				counter.acceptOne()
			}
			b.ReportAllocs()
			b.ResetTimer()
			var reached bool
			for i := 0; i < b.N; i++ {
				reached = counter.reached()
			}
			if !reached {
				b.Fatal("counter did not reach limit")
			}
		})
	}
}

func chainGraph(ids ...string) *Graph {
	store := NewGraph()
	for i := 0; i+1 < len(ids); i++ {
		store.AddLink(Link{FromID: ids[i], ToID: ids[i+1], Type: "next"})
	}
	return store
}

func crossReasonGraph() *Graph {
	store := NewGraph()
	for _, link := range []Link{
		{FromID: "a", ToID: "b", Type: "ab"},
		{FromID: "b", ToID: "c", Type: "bc"},
		{FromID: "c", ToID: "d1", Type: "cd"},
		{FromID: "c", ToID: "d2", Type: "cd"},
		{FromID: "d1", ToID: "e1", Type: "de"},
		{FromID: "d2", ToID: "e2", Type: "de"},
		{FromID: "c", ToID: "x", Type: "cx"},
		{FromID: "x", ToID: "y", Type: "xy"},
		{FromID: "b", ToID: "z", Type: "bz"},
	} {
		store.AddLink(link)
	}
	return store
}

func limitBeforeDepthGraph() *Graph {
	store := NewGraph()
	store.AddLink(Link{FromID: "a", ToID: "b", Type: "ab"})
	store.AddLink(Link{FromID: "b", ToID: "c1", Type: "bc"})
	store.AddLink(Link{FromID: "b", ToID: "c2", Type: "bc"})
	store.AddLink(Link{FromID: "c1", ToID: "d1", Type: "cd"})
	store.AddLink(Link{FromID: "c2", ToID: "d2", Type: "cd"})
	return store
}

func depthOnlyGraph() *Graph {
	store := chainGraph("a", "b", "c", "d", "e")
	store.AddLink(Link{FromID: "d", ToID: "blocked-by-depth", Type: "blocked"})
	return store
}

func pathIDs(paths []Path) [][]string {
	result := make([][]string, len(paths))
	for i, path := range paths {
		result[i] = append([]string(nil), path.ObjectIDs...)
	}
	return result
}

func reasons(branches []TruncatedBranch) []TruncationReason {
	result := make([]TruncationReason, len(branches))
	for i, branch := range branches {
		result[i] = branch.Reason
	}
	return result
}

func containsReason(reasons []TruncationReason, wanted TruncationReason) bool {
	for _, reason := range reasons {
		if reason == wanted {
			return true
		}
	}
	return false
}

func randomGraph(rng *rand.Rand) *Graph {
	store := NewGraph()
	for i := 0; i < 10; i++ {
		store.AddObject(string(rune('a' + i)))
	}
	for i := 0; i < 22; i++ {
		from := string(rune('a' + rng.IntN(10)))
		to := string(rune('a' + rng.IntN(10)))
		store.AddLink(Link{FromID: from, ToID: to, Type: fmt.Sprintf("t%d", rng.IntN(3))})
	}
	return store
}

func randomStartID(rng *rand.Rand) string {
	return string(rune('a' + rng.IntN(10)))
}

func randomDirections(rng *rand.Rand) []Direction {
	switch rng.IntN(3) {
	case 0:
		return []Direction{Outgoing}
	case 1:
		return []Direction{Incoming}
	default:
		return []Direction{Outgoing, Incoming}
	}
}
