package endpointshard

import (
	"fmt"
	"testing"
)

func assertQuery(t *testing.T, m *Manager, name, region string, wantIDs []string, wantFallback bool) {
	t.Helper()
	res, err := m.Query(name, region)
	if err != nil {
		t.Fatalf("Query(%q, %q) failed: %v", name, region, err)
	}
	got := endpointIDs(res.Endpoints)
	if fmt.Sprint(got) != fmt.Sprint(wantIDs) || res.Fallback != wantFallback {
		t.Fatalf("Query(%q, %q) = (%v, fallback=%v), want (%v, fallback=%v)",
			name, region, got, res.Fallback, wantIDs, wantFallback)
	}
	t.Logf("判定依据: Query(%q, %q) = (%v, fallback=%v) == expected", name, region, got, res.Fallback)
}

// Ready endpoints are served; same-region endpoints come first and
// each group is ordered by endpoint ID.
func TestQueryReadyAndRegionPriority(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	in := []Endpoint{
		live("e1", "r1"),
		live("e2", "r2"),
		ep("e3", "r1", true, true),   // terminating: not ready
		ep("e4", "r2", false, false), // unhealthy: not ready
	}
	mustSync(t, m, "s", in)
	t.Logf("输入: Sync(s, %+v)", in)

	assertQuery(t, m, "s", "r1", []string{"e1", "e2"}, false)
	assertQuery(t, m, "s", "r2", []string{"e2", "e1"}, false)
	// A region no endpoint belongs to: pure ID order.
	assertQuery(t, m, "s", "r9", []string{"e1", "e2"}, false)
}

// With no ready endpoint the query falls back to servable and
// terminating endpoints; with none of those either it is empty.
func TestQueryFallback(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	in := []Endpoint{
		ep("e1", "r1", true, true),
		ep("e2", "r2", true, true),
		ep("e3", "r1", false, false), // unhealthy: excluded everywhere
		ep("e4", "r2", false, true),  // unhealthy: not servable
	}
	mustSync(t, m, "s", in)
	t.Logf("输入: Sync(s, %+v)", in)

	// No ready endpoint -> fallback to servable && terminating.
	assertQuery(t, m, "s", "r1", []string{"e1", "e2"}, true)
	assertQuery(t, m, "s", "r2", []string{"e2", "e1"}, true)

	// No ready and no servable-terminating endpoint -> empty result,
	// still flagged as a fallback answer.
	mustSync(t, m, "s", []Endpoint{ep("e3", "r1", false, false)})
	assertQuery(t, m, "s", "r1", []string{}, true)

	// Empty service: empty result, fallback rule engaged.
	mustSync(t, m, "s", nil)
	assertQuery(t, m, "s", "r1", []string{}, true)
}

// Status-bit flips move endpoints between the ready and fallback
// selections without moving them between shards.
func TestQueryReflectsStatusBitChanges(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	mustSync(t, m, "s", []Endpoint{live("a", "r1"), live("b", "r1")})
	assertQuery(t, m, "s", "r1", []string{"a", "b"}, false)

	// a starts terminating: only b stays ready.
	mustSync(t, m, "s", []Endpoint{ep("a", "r1", true, true), live("b", "r1")})
	assertQuery(t, m, "s", "r1", []string{"b"}, false)

	// b starts terminating too: nothing ready, both in fallback.
	mustSync(t, m, "s", []Endpoint{ep("a", "r1", true, true), ep("b", "r1", true, true)})
	assertQuery(t, m, "s", "r1", []string{"a", "b"}, true)

	// a recovers: ready again.
	mustSync(t, m, "s", []Endpoint{live("a", "r1"), ep("b", "r1", true, true)})
	assertQuery(t, m, "s", "r1", []string{"a"}, false)
}
