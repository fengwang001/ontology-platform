package roadnet

import (
	"errors"
	"reflect"
	"testing"
)

func verPtr(v int64) *int64 { return &v }

func mustAdd(t *testing.T, nt *Net, u, v int, p []Segment) int {
	t.Helper()
	id, err := nt.AddEdge(u, v, p)
	if err != nil {
		t.Fatalf("AddEdge(%d,%d,%v) failed: %v", u, v, p, err)
	}
	return id
}

func mustQuery(t *testing.T, nt *Net, s int, t0 int64, g int, ver *int64) Result {
	t.Helper()
	res, err := nt.EarliestArrival(s, t0, g, ver)
	if err != nil {
		t.Fatalf("EarliestArrival(%d,%d,%d,%v) failed: %v", s, t0, g, ver, err)
	}
	return res
}

func checkResult(t *testing.T, label string, res Result, arrival int64, legs []Leg) {
	t.Helper()
	if res.Arrival != arrival || !reflect.DeepEqual(res.Legs, legs) {
		t.Fatalf("%s: got arrival=%d legs=%v, want arrival=%d legs=%v",
			label, res.Arrival, res.Legs, arrival, legs)
	}
	t.Logf("%s: arrival=%d legs=%v popped=%d (判定依据: 规格示例)", label, res.Arrival, res.Legs, res.Popped)
}

// TestSpecExample replays the worked example from the contract,
// including versioned history queries.
func TestSpecExample(t *testing.T) {
	nt, err := NewNet(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}})                    // edge 1
	mustAdd(t, nt, 1, 3, []Segment{{0, 10}, {20, 2}})          // edge 2
	mustAdd(t, nt, 0, 2, []Segment{{0, 3}})                    // edge 3
	mustAdd(t, nt, 2, 3, []Segment{{0, 4}, {10, -1}, {30, 1}}) // edge 4
	if nt.Version() != 4 {
		t.Fatalf("version = %d, want 4", nt.Version())
	}

	checkResult(t, "EA(0,0,3)", mustQuery(t, nt, 0, 0, 3, nil), 7,
		[]Leg{{3, 0, 3}, {4, 3, 7}})
	checkResult(t, "EA(0,8,3)", mustQuery(t, nt, 0, 8, 3, nil), 22,
		[]Leg{{1, 8, 13}, {2, 20, 22}})

	if id := mustAdd(t, nt, 0, 3, []Segment{{0, 7}}); id != 5 {
		t.Fatalf("new edge id = %d, want 5", id)
	}
	if nt.Version() != 5 {
		t.Fatalf("version = %d, want 5", nt.Version())
	}
	checkResult(t, "EA(0,0,3)@v5", mustQuery(t, nt, 0, 0, 3, nil), 7,
		[]Leg{{5, 0, 7}})

	if err := nt.Announce(5, 5, []Segment{{0, -1}}); err != nil {
		t.Fatalf("Announce(5,5,...) failed: %v", err)
	}
	if nt.Version() != 6 {
		t.Fatalf("version = %d, want 6", nt.Version())
	}
	checkResult(t, "EA(0,6,3)@v6", mustQuery(t, nt, 0, 6, 3, nil), 13,
		[]Leg{{3, 6, 9}, {4, 9, 13}})

	if err := nt.Advance(10); err != nil {
		t.Fatalf("Advance(10) failed: %v", err)
	}
	if nt.Version() != 6 {
		t.Fatalf("Advance changed version to %d", nt.Version())
	}
	if err := nt.Announce(3, 9, []Segment{{0, 1}}); !errors.Is(err, ErrRetroactive) {
		t.Fatalf("Announce(3,9,...) err = %v, want ErrRetroactive", err)
	}
	if err := nt.Announce(3, 10, []Segment{{0, 1}}); err != nil {
		t.Fatalf("Announce(3,10,[(0,1)]) failed: %v", err)
	}
	if err := nt.Announce(3, 10, []Segment{{0, 2}}); err != nil {
		t.Fatalf("Announce(3,10,[(0,2)]) replace failed: %v", err)
	}
	if nt.Version() != 8 {
		t.Fatalf("version = %d, want 8", nt.Version())
	}
	checkResult(t, "EA(0,12,3)@v8", mustQuery(t, nt, 0, 12, 3, nil), 22,
		[]Leg{{1, 12, 17}, {2, 20, 22}})

	checkResult(t, "EA(0,10,2)@v8", mustQuery(t, nt, 0, 10, 2, nil), 12,
		[]Leg{{3, 10, 12}})
	checkResult(t, "EA(0,10,2)@v7", mustQuery(t, nt, 0, 10, 2, verPtr(7)), 11,
		[]Leg{{3, 10, 11}})
	checkResult(t, "EA(0,10,2)@v6", mustQuery(t, nt, 0, 10, 2, verPtr(6)), 13,
		[]Leg{{3, 10, 13}})
	if _, err := nt.EarliestArrival(0, 10, 2, verPtr(9)); !errors.Is(err, ErrVersionFuture) {
		t.Fatalf("EA(0,10,2)@v9 err = %v, want ErrVersionFuture", err)
	}
}
