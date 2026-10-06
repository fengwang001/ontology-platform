package occupancy

import "testing"

// 路网：a/b/c 各 2 车道（a 绕行 c，用于三冲突构造；b 绕行 c），
// d 属走廊 L，另有若干单车道辅助道路。
func testConfig() Config {
	return Config{
		Roads: []Road{
			{ID: "a", Lanes: 2, Corridor: "K", Detour: []string{"b"}},
			{ID: "b", Lanes: 2, Corridor: "K", Detour: []string{"c"}},
			{ID: "c", Lanes: 2, Corridor: "K"},
			{ID: "k", Lanes: 2, Corridor: "K", Detour: []string{"c"}},
			{ID: "m", Lanes: 2, Corridor: "K", Detour: []string{"c"}},
			{ID: "n", Lanes: 3, Corridor: "K", Detour: []string{"b"}},
			{ID: "e", Lanes: 1, Corridor: "K", Detour: []string{"c"}},
			{ID: "f", Lanes: 1, Corridor: "K", Detour: []string{"c"}},
			{ID: "g", Lanes: 1, Corridor: "K"},
			{ID: "h", Lanes: 1, Corridor: "K", Detour: []string{"c"}},
			{ID: "j", Lanes: 1, Corridor: "K", Detour: []string{"c"}},
			{ID: "d", Lanes: 2, Corridor: "L", Detour: []string{"a"}},
		},
		CorridorCap: map[string]int{"K": 2, "L": 2},
	}
}

func mustService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(testConfig())
	if !err.None() {
		t.Fatalf("config: %v", err)
	}
	return s
}

func acceptOK(t *testing.T, r ApplyResult) {
	t.Helper()
	if !r.Reason.None() {
		t.Fatalf("expected accept, got %v", r.Reason)
	}
}

func rejectCode(t *testing.T, r ApplyResult, want ErrorCode) {
	t.Helper()
	if r.Reason.None() || r.Reason.Code != want {
		t.Fatalf("expected reject %s, got %v", want, r.Reason)
	}
}

func TestSameRoadExactLanes(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 10, End: 20}))
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 15, End: 25}))
	rejectCode(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 15, End: 16}), ErrSameRoadConflict)
}

func TestAdjacentIntervals(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 0, End: 10}))
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 10, End: 20}))
	q, _ := s.Query(QueryRequest{Road: "a", At: 10})
	if q.ClosedLanes != 2 || len(q.Active) != 1 {
		t.Fatalf("at 10 want 2/1, got %+v", q)
	}
	q, _ = s.Query(QueryRequest{Road: "a", At: 9})
	if q.ClosedLanes != 2 || len(q.Active) != 1 {
		t.Fatalf("at 9 want 2/1, got %+v", q)
	}
}

func TestDetourForward(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 100, End: 200}))
	rejectCode(t, s.Apply(ApplyRequest{OpTime: 0, Road: "b", Lanes: 1, Start: 150, End: 160}), ErrDetourConflict)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "b", Lanes: 1, Start: 200, End: 210}))
}

func TestDetourBackward(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "b", Lanes: 1, Start: 100, End: 200}))
	rejectCode(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 150, End: 160}), ErrDetourConflict)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 200, End: 210}))
}

func TestCorridorExactCap(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 0, End: 10}))
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "b", Lanes: 1, Start: 0, End: 10}))
	rejectCode(t, s.Apply(ApplyRequest{OpTime: 0, Road: "c", Lanes: 1, Start: 5, End: 6}), ErrCorridorCap)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "c", Lanes: 1, Start: 10, End: 20}))
}
