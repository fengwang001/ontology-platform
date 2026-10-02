package ontology

import (
	"testing"
)

func requireResult(t *testing.T, result UpdateResult, err error, version int, dChanged, pChanged []int) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != version || !sameInts(result.DChanged, dChanged) || !sameInts(result.PChanged, pChanged) {
		t.Fatalf("result=%+v version=%d D=%v P=%v", result, version, dChanged, pChanged)
	}
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if ErrorCodeOf(err) != code {
		t.Fatalf("error=%v, want code=%s", err, code)
	}
}

func TestSpecExample(t *testing.T) {
	s, err := New(4, 0, 100, 3)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.AddEdge(0, 1, 5)
	requireResult(t, result, err, 1, []int{1}, nil)
	if result.EdgeID != 1 {
		t.Fatalf("edge id=%d", result.EdgeID)
	}

	result, err = s.AddEdge(0, 2, 2)
	requireResult(t, result, err, 2, []int{2}, nil)
	result, err = s.AddEdge(2, 1, 3)
	requireResult(t, result, err, 3, nil, nil)
	result, err = s.AddEdge(1, 3, 1)
	requireResult(t, result, err, 4, []int{3}, nil)

	result, err = s.SetWeight(1, 7)
	requireResult(t, result, err, 5, nil, []int{1})
	result, err = s.SetWeight(1, 5)
	requireResult(t, result, err, 6, nil, []int{1})
	result, err = s.RemoveEdge(1)
	requireResult(t, result, err, 7, nil, []int{1})
	result, err = s.SetWeight(3, 4)
	requireResult(t, result, err, 8, []int{1, 3}, nil)

	if distance, reachable, _ := s.Dist(1); !reachable || distance != 6 {
		t.Fatalf("Dist(1)=(%d,%v)", distance, reachable)
	}
	if distance, reachable, _ := s.Dist(3); !reachable || distance != 7 {
		t.Fatalf("Dist(3)=(%d,%v)", distance, reachable)
	}

	if distance, reachable, err := s.DistAt(1, 6); err != nil || !reachable || distance != 5 {
		t.Fatalf("DistAt(1,6)=(%d,%v,%v)", distance, reachable, err)
	}
	if distance, reachable, err := s.DistAt(1, 8); err != nil || !reachable || distance != 6 {
		t.Fatalf("DistAt(1,8)=(%d,%v,%v)", distance, reachable, err)
	}
	_, _, err = s.DistAt(1, 5)
	requireCode(t, err, HistoryExpired)
	_, _, err = s.DistAt(1, 9)
	requireCode(t, err, VersionFuture)
}

func TestParentSwitchAndTightNoDistanceChange(t *testing.T) {
	s, _ := New(4, 0, 100, 10)

	result, err := s.AddEdge(0, 1, 5)
	requireResult(t, result, err, 1, []int{1}, nil)
	result, err = s.AddEdge(0, 2, 2)
	requireResult(t, result, err, 2, []int{2}, nil)

	result, err = s.AddEdge(2, 1, 4)
	requireResult(t, result, err, 3, nil, nil)

	result, err = s.AddEdge(0, 1, 5)
	requireResult(t, result, err, 4, nil, nil)

	result, err = s.AddEdge(2, 1, 0)
	requireCode(t, err, InvalidArgument)

	result, err = s.SetWeight(3, 3)
	requireResult(t, result, err, 5, nil, nil)
	if parent, has, _ := s.Parent(1); !has || parent != 1 {
		t.Fatalf("Parent(1)=%d,%v, want edge 1", parent, has)
	}

	result, err = s.SetWeight(3, 4)
	requireResult(t, result, err, 6, nil, nil)

	path, reachable, err := s.Path(1)
	if err != nil || !reachable || len(path) != 1 || path[0] != 1 {
		t.Fatalf("Path(1)=%v reachable=%v err=%v, want edge 1", path, reachable, err)
	}
}

func TestSmallerIDTightEdgeSwitchesParent(t *testing.T) {
	s, _ := New(4, 0, 100, 10)

	result, err := s.AddEdge(0, 3, 2)
	requireResult(t, result, err, 1, []int{3}, nil)
	result, err = s.AddEdge(3, 1, 3)
	requireResult(t, result, err, 2, []int{1}, nil)
	result, err = s.AddEdge(0, 2, 6)
	requireResult(t, result, err, 3, []int{2}, nil)
	result, err = s.AddEdge(2, 1, 4)
	requireResult(t, result, err, 4, nil, nil)

	if parent, has, _ := s.Parent(1); !has || parent != 2 {
		t.Fatalf("Parent(1)=%d,%v, want edge 2", parent, has)
	}
	if distance, reachable, _ := s.Dist(1); !reachable || distance != 5 {
		t.Fatalf("Dist(1)=(%d,%v), want 5", distance, reachable)
	}
}

func TestSuccessorHasAlternativeTightIn(t *testing.T) {
	s, _ := New(5, 0, 100, 10)

	updates := [][3]int{{0, 1, 1}, {1, 2, 1}, {0, 3, 1}, {3, 2, 1}, {2, 4, 1}}
	for i, update := range updates {
		result, err := s.AddEdge(update[0], update[1], update[2])
		expectedD := []int(nil)
		if i < 3 || i == 4 {
			expectedD = []int{update[1]}
		}
		requireResult(t, result, err, i+1, expectedD, nil)
	}

	result, err := s.RemoveEdge(1)
	requireResult(t, result, err, 6, []int{1}, []int{2})

	if parent, has, _ := s.Parent(2); !has || parent != 4 {
		t.Fatalf("Parent(2)=%d,%v", parent, has)
	}
	if distance, reachable, _ := s.Dist(4); !reachable || distance != 3 {
		t.Fatalf("Dist(4)=(%d,%v)", distance, reachable)
	}
}

func TestIncreaseCascadeAndDeleteUnreachable(t *testing.T) {
	s, _ := New(4, 0, 100, 10)

	for i, update := range [][3]int{{0, 1, 1}, {1, 2, 1}, {2, 3, 1}, {0, 3, 100}} {
		result, err := s.AddEdge(update[0], update[1], update[2])
		expectedD := []int{update[1]}
		if i == 3 {
			expectedD = nil
		}
		requireResult(t, result, err, i+1, expectedD, nil)
	}

	result, err := s.SetWeight(1, 90)
	requireResult(t, result, err, 5, []int{1, 2, 3}, nil)

	result, err = s.RemoveEdge(1)
	requireResult(t, result, err, 6, []int{1, 2, 3}, nil)
	_, reachable, _ := s.Dist(1)
	if reachable {
		t.Fatal("node 1 should be unreachable")
	}
}

func TestSetSameWeightAndRejection(t *testing.T) {
	s, err := New(2, 0, 1, 10)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.AddEdge(0, 1, 3)
	requireResult(t, result, err, 1, []int{1}, nil)
	firstID := result.EdgeID

	result, err = s.SetWeight(firstID, 3)
	requireResult(t, result, err, 2, nil, nil)

	_, err = s.AddEdge(1, 0, 1)
	requireCode(t, err, CapacityFull)

	_, err = s.AddEdge(0, 0, 1)
	requireCode(t, err, InvalidArgument)

	_, err = s.SetWeight(2, 1)
	requireCode(t, err, EdgeNotFound)

	_, err = s.RemoveEdge(2)
	requireCode(t, err, EdgeNotFound)

	if s.CurrentVersion() != 2 {
		t.Fatalf("version=%d", s.CurrentVersion())
	}

	result, err = s.RemoveEdge(firstID)
	requireResult(t, result, err, 3, []int{1}, nil)

	result, err = s.AddEdge(0, 1, 4)
	requireResult(t, result, err, 4, []int{1}, nil)
	if result.EdgeID != 2 {
		t.Fatalf("reused id=%d", result.EdgeID)
	}
}

func TestParallelEdges(t *testing.T) {
	s, _ := New(2, 0, 10, 10)

	first, err := s.AddEdge(0, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddEdge(0, 1, 5)
	if err != nil {
		t.Fatal(err)
	}

	if parent, has, _ := s.Parent(1); !has || parent != first.EdgeID {
		t.Fatalf("Parent(1)=%d,%v", parent, has)
	}

	result, err := s.RemoveEdge(first.EdgeID)
	requireResult(t, result, err, 3, nil, []int{1})
	if parent, has, _ := s.Parent(1); !has || parent != second.EdgeID {
		t.Fatalf("Parent(1)=%d,%v", parent, has)
	}

	result, err = s.SetWeight(second.EdgeID, 6)
	requireResult(t, result, err, 4, []int{1}, nil)
}

func TestHistoryOldestBoundary(t *testing.T) {
	s, _ := New(2, 0, 10, 3)

	for i := 0; i < 5; i++ {
		_, err := s.AddEdge(0, 1, i+1)
		if err != nil {
			t.Fatal(err)
		}
	}

	if distance, reachable, err := s.DistAt(1, 3); err != nil || !reachable || distance != 1 {
		t.Fatalf("oldest=(%d,%v,%v)", distance, reachable, err)
	}
	_, _, err := s.DistAt(1, 2)
	requireCode(t, err, HistoryExpired)
	_, _, err = s.DistAt(4, 3)
	requireCode(t, err, InvalidArgument)
}

func TestChainLocality(t *testing.T) {
	const n = 1000
	s, _ := New(n, 0, n, 64)

	for i := 0; i+1 < n; i++ {
		if _, err := s.AddEdge(i, i+1, 1); err != nil {
			t.Fatal(err)
		}
	}

	alternativeResult, err := s.AddEdge(n-3, n-1, 2)
	if err != nil {
		t.Fatal(err)
	}

	alternativeChanged := append([]int{}, alternativeResult.DChanged...)
	alternativeChanged = append(alternativeChanged, alternativeResult.PChanged...)
	examinedAfterAlternative := s.Examined()
	if limit := examinedLimit(s, alternativeChanged); examinedAfterAlternative > limit {
		t.Fatalf("alternative examined=%d limit=%d", examinedAfterAlternative, limit)
	}

	result, err := s.RemoveEdge(n - 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DChanged) != 0 || len(result.PChanged) != 1 || result.PChanged[0] != n-1 {
		t.Fatalf("result D=%v P=%v", result.DChanged, result.PChanged)
	}
	examinedAfterRemove := s.Examined()
	removeChanged := append([]int{}, result.DChanged...)
	removeChanged = append(removeChanged, result.PChanged...)
	if limit := examinedLimit(s, removeChanged); examinedAfterRemove > limit {
		t.Fatalf("remove examined=%d limit=%d", examinedAfterRemove, limit)
	}

	difference := uint64(0)
	if examinedAfterAlternative > examinedAfterRemove {
		difference = examinedAfterAlternative - examinedAfterRemove
	} else {
		difference = examinedAfterRemove - examinedAfterAlternative
	}
	if difference > 8 {
		t.Fatalf("examined difference=%d", difference)
	}
}

func examinedLimit(s *Service, changed []int) uint64 {
	changedSet := make(map[int]struct{}, len(changed))
	incidentEdges := make(map[int]struct{})
	otherEnds := make(map[int]struct{})
	for _, v := range changed {
		changedSet[v] = struct{}{}
	}
	for v := range changedSet {
		for _, id := range s.in[v] {
			incidentEdges[id] = struct{}{}
			otherEnds[s.edges[id].from] = struct{}{}
		}
		for _, id := range s.out[v] {
			incidentEdges[id] = struct{}{}
			otherEnds[s.edges[id].to] = struct{}{}
		}
	}
	h := 0
	for v := range otherEnds {
		h += len(s.in[v])
	}
	return 4 * uint64(len(incidentEdges)+h+2)
}
