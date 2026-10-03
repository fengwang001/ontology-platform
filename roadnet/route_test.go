package roadnet

import (
	"errors"
	"reflect"
	"testing"
)

func expectLegs(t *testing.T, label string, res Result, arrival int64, legs []Leg) {
	t.Helper()
	if res.Arrival != arrival || !reflect.DeepEqual(res.Legs, legs) {
		t.Fatalf("%s: got arrival=%d legs=%v, want arrival=%d legs=%v",
			label, res.Arrival, res.Legs, arrival, legs)
	}
	t.Logf("%s: arrival=%d legs=%v popped=%d", label, res.Arrival, res.Legs, res.Popped)
}

// Waiting until a later segment start beats departing at once.
func TestWaitForLaterSegment(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 10}, {20, 2}})
	// t=13: leave now -> 23, wait for the cheap segment at 20 -> 22.
	expectLegs(t, "wait wins", mustQuery(t, nt, 0, 13, 1, nil), 22, []Leg{{1, 20, 22}})
	// t=0: leave now -> 10, waiting gives 22; immediate wins.
	expectLegs(t, "immediate wins", mustQuery(t, nt, 0, 0, 1, nil), 10, []Leg{{1, 0, 10}})
}

// The current segment is closed; only waiting for a later open
// segment works. Departure exactly at a segment start vs one before.
func TestClosedSegmentWaiting(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, -1}, {10, 3}, {20, -1}, {30, 1}})
	expectLegs(t, "inside closed", mustQuery(t, nt, 0, 4, 1, nil), 13, []Leg{{1, 10, 13}})
	expectLegs(t, "at open start", mustQuery(t, nt, 0, 10, 1, nil), 13, []Leg{{1, 10, 13}})
	expectLegs(t, "one before open", mustQuery(t, nt, 0, 9, 1, nil), 13, []Leg{{1, 10, 13}})
	expectLegs(t, "at closed start", mustQuery(t, nt, 0, 20, 1, nil), 31, []Leg{{1, 30, 31}})
	expectLegs(t, "one before closed", mustQuery(t, nt, 0, 19, 1, nil), 22, []Leg{{1, 19, 22}})
}

// Every reachable segment closed -> the edge is unusable.
func TestAllLaterSegmentsClosed(t *testing.T) {
	nt := newNet(t, 3, 3)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}, {10, -1}}) // open only before 10
	mustAdd(t, nt, 1, 2, []Segment{{0, 1}})
	if _, err := nt.EarliestArrival(0, 10, 2, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("t0=10: err = %v, want ErrUnreachable", err)
	}
	if _, err := nt.EarliestArrival(0, 11, 2, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("t0=11: err = %v, want ErrUnreachable", err)
	}
	expectLegs(t, "last open moment", mustQuery(t, nt, 0, 9, 2, nil), 15,
		[]Leg{{1, 9, 14}, {2, 14, 15}})
}

// Equal arrival times: fewest edges first, then the lexicographically
// smallest edge id sequence.
func TestRouteTieBreaking(t *testing.T) {
	nt := newNet(t, 4, 8)
	mustAdd(t, nt, 0, 1, []Segment{{0, 2}}) // edge 1
	mustAdd(t, nt, 1, 3, []Segment{{0, 3}}) // edge 2: path [1,2] arrives 5
	mustAdd(t, nt, 0, 3, []Segment{{0, 5}}) // edge 3: 1 hop, arrives 5
	expectLegs(t, "fewer hops wins", mustQuery(t, nt, 0, 0, 3, nil), 5, []Leg{{3, 0, 5}})

	// Same-hop ties break by edge id sequence on a separate network.
	nt2 := newNet(t, 4, 8)
	mustAdd(t, nt2, 0, 1, []Segment{{0, 2}}) // edge 1
	mustAdd(t, nt2, 1, 3, []Segment{{0, 3}}) // edge 2: path [1,2] arrives 5
	mustAdd(t, nt2, 0, 2, []Segment{{0, 1}}) // edge 3
	mustAdd(t, nt2, 2, 3, []Segment{{0, 4}}) // edge 4: path [3,4] arrives 5
	// [1,2] and [3,4] both have 2 hops; [1,2] is lexicographically smaller.
	expectLegs(t, "lexicographic wins", mustQuery(t, nt2, 0, 0, 3, nil), 5,
		[]Leg{{1, 0, 2}, {2, 2, 5}})
}

// When departing at d(u) and waiting both give the same arrival, the
// leg departs at the smallest achieving integer.
func TestLegDepartureIsMinimal(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 8}, {5, 3}})
	// t=0 -> 8, t=5 -> 8: the minimum departure is 0.
	expectLegs(t, "minimal departure", mustQuery(t, nt, 0, 0, 1, nil), 8, []Leg{{1, 0, 8}})
	// t=2 -> 10, t=5 -> 8: only waiting achieves 8, depart at 5.
	expectLegs(t, "forced wait", mustQuery(t, nt, 0, 2, 1, nil), 8, []Leg{{1, 5, 8}})
}

// Replace (same eff) changes the profile at that effective time;
// append (larger eff) adds a new record after the old one.
func TestReplaceVsAppend(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}})                       // v1
	if err := nt.Announce(1, 10, []Segment{{0, 1}}); err != nil { // v2: append
		t.Fatal(err)
	}
	expectLegs(t, "appended record", mustQuery(t, nt, 0, 10, 1, nil), 11, []Leg{{1, 10, 11}})
	expectLegs(t, "before append", mustQuery(t, nt, 0, 4, 1, nil), 9, []Leg{{1, 4, 9}})
	if err := nt.Announce(1, 10, []Segment{{0, 3}}); err != nil { // v3: replace
		t.Fatal(err)
	}
	expectLegs(t, "replaced record", mustQuery(t, nt, 0, 10, 1, nil), 13, []Leg{{1, 10, 13}})
	// History: v2 still sees the appended (pre-replace) profile.
	expectLegs(t, "pre-replace visible@v2", mustQuery(t, nt, 0, 10, 1, verPtr(2)), 11, []Leg{{1, 10, 11}})
	// v1 predates the record entirely.
	expectLegs(t, "record absent@v1", mustQuery(t, nt, 0, 10, 1, verPtr(1)), 15, []Leg{{1, 10, 15}})
}

// Version visibility: a record is visible exactly from its
// registration version until its replacement version; an edge exists
// only from its registration version on.
func TestVersionVisibilityBoundaries(t *testing.T) {
	nt := newNet(t, 3, 4)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}})                      // edge 1 @v1
	if err := nt.Announce(1, 4, []Segment{{0, 2}}); err != nil { // rec @v2
		t.Fatal(err)
	}
	if err := nt.Announce(1, 4, []Segment{{0, 9}}); err != nil { // replaces @v3
		t.Fatal(err)
	}
	mustAdd(t, nt, 1, 2, []Segment{{0, 1}}) // edge 2 @v4

	// Record registered at v2: visible at v2, not at v1.
	expectLegs(t, "rec@v2", mustQuery(t, nt, 0, 4, 1, verPtr(2)), 6, []Leg{{1, 4, 6}})
	expectLegs(t, "rec@v1", mustQuery(t, nt, 0, 4, 1, verPtr(1)), 9, []Leg{{1, 4, 9}})
	// Replaced at v3: old record visible at v2, replacement at v3.
	expectLegs(t, "replacement@v3", mustQuery(t, nt, 0, 4, 1, verPtr(3)), 13, []Leg{{1, 4, 13}})
	// Edge 2 registered at v4: usable at v4, absent at v3.
	expectLegs(t, "edge2@v4", mustQuery(t, nt, 0, 0, 2, verPtr(4)), 6,
		[]Leg{{1, 0, 5}, {2, 5, 6}})
	if _, err := nt.EarliestArrival(0, 0, 2, verPtr(3)); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("edge2@v3: err = %v, want ErrUnreachable", err)
	}
	// Queries at past versions ignore everything that happens later.
	if err := nt.Announce(1, 7, []Segment{{0, 1}}); err != nil { // v5
		t.Fatal(err)
	}
	if err := nt.Advance(100); err != nil {
		t.Fatal(err)
	}
	expectLegs(t, "v3 stable after later ops", mustQuery(t, nt, 0, 4, 1, verPtr(3)), 13, []Leg{{1, 4, 13}})
}

// A departure time before now still evaluates against all records
// effective up to that time.
func TestQueryBeforeNow(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}})
	if err := nt.Announce(1, 10, []Segment{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := nt.Advance(50); err != nil {
		t.Fatal(err)
	}
	// t0 = 8 < now: leaving now costs 13, the record at eff 10 still
	// applies and waiting for it arrives at 11.
	expectLegs(t, "t0<now waits across record", mustQuery(t, nt, 0, 8, 1, nil), 11, []Leg{{1, 10, 11}})
	expectLegs(t, "t0<now immediate", mustQuery(t, nt, 0, 0, 1, nil), 5, []Leg{{1, 0, 5}})
}

// Waiting across a record boundary: the current record is closed, a
// later record opens the edge again.
func TestWaitAcrossRecordBoundary(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 2}, {5, -1}})              // closed from 5
	if err := nt.Announce(1, 20, []Segment{{0, 1}}); err != nil { // reopens at 20
		t.Fatal(err)
	}
	expectLegs(t, "wait for next record", mustQuery(t, nt, 0, 6, 1, nil), 21, []Leg{{1, 20, 21}})
	expectLegs(t, "before closure", mustQuery(t, nt, 0, 3, 1, nil), 5, []Leg{{1, 3, 5}})
}

// A record's segment is truncated by the next record's effective
// time; the optimum may sit exactly at the boundary.
func TestSegmentTruncatedAtRecordBoundary(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}})                       // would cost 5 forever
	if err := nt.Announce(1, 10, []Segment{{0, 1}}); err != nil { // cheap from 10
		t.Fatal(err)
	}
	// t=8: leave now -> 13, wait for the boundary at 10 -> 11.
	expectLegs(t, "boundary wait", mustQuery(t, nt, 0, 8, 1, nil), 11, []Leg{{1, 10, 11}})
	// t=4: leave now -> 9 beats waiting (11).
	expectLegs(t, "boundary not worth it", mustQuery(t, nt, 0, 4, 1, nil), 9, []Leg{{1, 4, 9}})
}

// Parallel edges between the same nodes are independent.
func TestParallelEdges(t *testing.T) {
	nt := newNet(t, 2, 4)
	mustAdd(t, nt, 0, 1, []Segment{{0, 9}})           // edge 1
	mustAdd(t, nt, 0, 1, []Segment{{0, 3}, {10, 1}})  // edge 2
	mustAdd(t, nt, 0, 1, []Segment{{0, -1}, {12, 2}}) // edge 3
	expectLegs(t, "parallel t0=0", mustQuery(t, nt, 0, 0, 1, nil), 3, []Leg{{2, 0, 3}})
	expectLegs(t, "parallel t0=5", mustQuery(t, nt, 0, 5, 1, nil), 8, []Leg{{2, 5, 8}})
	expectLegs(t, "parallel t0=11", mustQuery(t, nt, 0, 11, 1, nil), 12, []Leg{{2, 11, 12}})
	expectLegs(t, "parallel t0=12", mustQuery(t, nt, 0, 12, 1, nil), 13, []Leg{{2, 12, 13}})
}
