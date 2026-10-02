package dynamicforest

import (
	"errors"
	"reflect"
	"testing"
)

func addForTest(t *testing.T, service *Service, u, v int, weight int64) (int, *UpdateResult) {
	t.Helper()
	id, result, err := service.AddEdge(u, v, weight)
	if err != nil {
		t.Fatalf("AddEdge(%d,%d,%d): %v", u, v, weight, err)
	}
	return id, result
}

func setForTest(t *testing.T, service *Service, id int, weight int64) *UpdateResult {
	t.Helper()
	result, err := service.SetWeight(id, weight)
	if err != nil {
		t.Fatalf("SetWeight(%d,%d): %v", id, weight, err)
	}
	return result
}

func assertInForest(t *testing.T, service *Service, ids ...int) {
	t.Helper()
	for _, id := range ids {
		ok, err := service.InForest(id)
		if err != nil || !ok {
			t.Fatalf("edge %d inForest=%v err=%v", id, ok, err)
		}
	}
}

func assertNotInForest(t *testing.T, service *Service, ids ...int) {
	t.Helper()
	for _, id := range ids {
		ok, err := service.InForest(id)
		if err != nil || ok {
			t.Fatalf("edge %d inForest=%v err=%v", id, ok, err)
		}
	}
}

func TestAddCycleMinAndMax(t *testing.T) {
	service, _ := NewService(4, 20)
	t.Log("input AddEdge(0,1,4); output enters edge 1; basis forest is acyclic")
	addForTest(t, service, 0, 1, 4)
	t.Log("input AddEdge(1,2,5); output enters edge 2; basis joins components")
	addForTest(t, service, 1, 2, 5)
	t.Log("input AddEdge(0,2,6); output no change; basis new edge is larger than path maximum (5,2)")
	_, result := addForTest(t, service, 0, 2, 6)
	if len(result.Entered) != 0 || len(result.Left) != 0 || result.Weight != 9 {
		t.Fatalf("new edge largest: %+v", result)
	}
	assertNotInForest(t, service, 3)

	t.Log("input AddEdge(0,2,3); output entered [4], left [2]; basis (3,4) is smaller than path maximum (5,2)")
	_, result = addForTest(t, service, 0, 2, 3)
	requireResult(t, result, &UpdateResult{4, []int{4}, []int{2}, 7, 2})
	assertInForest(t, service, 1, 4)
	assertNotInForest(t, service, 2, 3)
}

func TestEqualWeightsUseIDAndNonForestChanges(t *testing.T) {
	service, _ := NewService(3, 20)
	addForTest(t, service, 0, 1, 5)
	addForTest(t, service, 1, 2, 5)

	t.Log("input AddEdge(0,2,5); output no change; basis (5,3) is greater than path maximum (5,2)")
	_, result := addForTest(t, service, 0, 2, 5)
	if len(result.Entered) != 0 || len(result.Left) != 0 {
		t.Fatalf("equal-weight new edge: %+v", result)
	}

	t.Log("input SetWeight(3,5); output version increments without forest change; basis same order remains")
	result = setForTest(t, service, 3, 5)
	requireResult(t, result, &UpdateResult{4, []int{}, []int{}, 10, 1})

	t.Log("input AddEdge(0,2,4); output edge 4 enters; basis strictly smaller than path max")
	id, result := addForTest(t, service, 0, 2, 4)
	if id != 4 || !reflect.DeepEqual(result.Entered, []int{4}) || !reflect.DeepEqual(result.Left, []int{2}) {
		t.Fatalf("small non-tree edge: %+v", result)
	}

	t.Log("input SetWeight(3,4); output entered [3], left [4]; basis equal weights use smaller edge ID")
	result = setForTest(t, service, 3, 4)
	requireResult(t, result, &UpdateResult{6, []int{3}, []int{4}, 9, 1})

	t.Log("input SetWeight(3,10); output entered [4], left [3]; basis forest edge increased and smaller parallel edge replaces it")
	result = setForTest(t, service, 3, 10)
	requireResult(t, result, &UpdateResult{7, []int{4}, []int{3}, 9, 1})
	assertInForest(t, service, 1, 4)
}

func TestForestWeightChangesAndParallelReplacement(t *testing.T) {
	service, _ := NewService(2, 20)
	first, _ := addForTest(t, service, 0, 1, 4)
	parallel, _ := addForTest(t, service, 0, 1, 7)
	if first != 1 || parallel != 2 {
		t.Fatalf("unexpected ids %d,%d", first, parallel)
	}

	t.Log("input SetWeight(1,2); output no entered/left, total drops to 2; basis forest edge decreases")
	result := setForTest(t, service, 1, 2)
	requireResult(t, result, &UpdateResult{3, []int{}, []int{}, 2, 1})
	since, err := service.TreeSince(1)
	if err != nil || since != 1 {
		t.Fatalf("TreeSince after decrease=%d,%v", since, err)
	}

	t.Log("input SetWeight(1,9); output entered [2], left [1]; basis parallel non-tree edge (7,2) is smaller")
	result = setForTest(t, service, 1, 9)
	requireResult(t, result, &UpdateResult{4, []int{2}, []int{1}, 7, 1})

	t.Log("input SetWeight(2,20); output entered [1], left [2]; basis non-tree edge (9,1) becomes smaller")
	result = setForTest(t, service, 2, 20)
	requireResult(t, result, &UpdateResult{5, []int{1}, []int{2}, 9, 1})

	t.Log("input SetWeight(1,1); output no change; basis forest edge decreases and is its own replacement")
	result = setForTest(t, service, 1, 1)
	requireResult(t, result, &UpdateResult{6, []int{}, []int{}, 1, 1})
	since, _ = service.TreeSince(1)
	if since != 5 {
		t.Fatalf("TreeSince self replacement=%d", since)
	}
}

func TestSmallestCandidateAcrossCut(t *testing.T) {
	service, _ := NewService(4, 20)
	addForTest(t, service, 0, 1, 1)
	addForTest(t, service, 1, 2, 10)
	addForTest(t, service, 2, 3, 1)

	addForTest(t, service, 0, 2, 16)
	addForTest(t, service, 1, 3, 15)
	t.Log("input RemoveEdge(2); output entered [5], left [2]; basis candidates (16,4),(15,5), minimum is 5")
	result, err := service.RemoveEdge(2)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{6, []int{5}, []int{2}, 17, 1})
}

func TestQueryAndConstructionErrorsAreDistinguishable(t *testing.T) {
	if _, err := NewService(0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("N=0 error=%v", err)
	}
	if _, err := NewService(1, 500_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("E limit error=%v", err)
	}
	service, _ := NewService(2, 1)
	if _, _, err := service.AddEdge(2, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("node error=%v", err)
	}
	if ok, err := service.Connected(0, 2); ok || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Connected=(%v,%v)", ok, err)
	}
	if _, err := service.InForest(9); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("InForest missing=%v", err)
	}
	if _, err := service.TreeSince(9); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("TreeSince missing=%v", err)
	}
}
