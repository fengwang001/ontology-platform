package roadnet

import (
	"errors"
	"testing"
)

func newNet(t *testing.T, n, cap int) *Net {
	t.Helper()
	nt, err := NewNet(n, cap)
	if err != nil {
		t.Fatalf("NewNet(%d,%d): %v", n, cap, err)
	}
	return nt
}

func TestConstructorValidation(t *testing.T) {
	for _, tc := range [][2]int{{0, 10}, {5001, 10}, {-1, 10}, {10, 0}, {10, 100001}, {10, -5}} {
		if _, err := NewNet(tc[0], tc[1]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("NewNet(%d,%d) err = %v, want ErrInvalidParam", tc[0], tc[1], err)
		}
	}
	if _, err := NewNet(1, 1); err != nil {
		t.Fatalf("NewNet(1,1) should succeed: %v", err)
	}
	if _, err := NewNet(5000, 100000); err != nil {
		t.Fatalf("NewNet(5000,100000) should succeed: %v", err)
	}
}

func TestAddEdgeValidationAndIdConsumption(t *testing.T) {
	nt := newNet(t, 3, 2)
	good := []Segment{{0, 5}}
	badCases := []struct {
		u, v int
		p    []Segment
	}{
		{-1, 1, good},             // node out of range
		{0, 3, good},              // node out of range
		{1, 1, good},              // u == v
		{0, 1, nil},               // empty profile
		{0, 1, []Segment{{1, 5}}}, // first offset not 0
		{0, 1, []Segment{{0, 5}, {5, 3}, {5, 1}}},  // offsets not increasing
		{0, 1, []Segment{{0, 5}, {3, 3}, {2, 1}}},  // offsets decreasing
		{0, 1, []Segment{{0, 0}}},                  // cost 0
		{0, 1, []Segment{{0, -2}}},                 // cost < -1
		{0, 1, []Segment{{0, 1000001}}},            // cost too large
		{0, 1, []Segment{{0, 5}, {1000000001, 2}}}, // offset too large
		{0, 1, []Segment{{0, 5}, {-1, 2}}},         // negative offset
	}
	for i, tc := range badCases {
		if _, err := nt.AddEdge(tc.u, tc.v, tc.p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad case %d: err = %v, want ErrInvalidParam", i, err)
		}
	}
	// 33 segments exceed the limit.
	big := make([]Segment, 33)
	for i := range big {
		big[i] = Segment{int64(i), 1}
	}
	if _, err := nt.AddEdge(0, 1, big); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("33 segments: err = %v, want ErrInvalidParam", err)
	}
	// 32 segments are fine; rejected calls above consumed no ids.
	ok := make([]Segment, 32)
	for i := range ok {
		ok[i] = Segment{int64(i), 1}
	}
	id, err := nt.AddEdge(0, 1, ok)
	if err != nil || id != 1 {
		t.Fatalf("first valid AddEdge = (%d,%v), want id 1", id, err)
	}
	if id, err := nt.AddEdge(1, 2, good); err != nil || id != 2 {
		t.Fatalf("second valid AddEdge = (%d,%v), want id 2", id, err)
	}
	// Edge cap reached: parameter check still comes first.
	if _, err := nt.AddEdge(9, 9, good); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("full+invalid: err = %v, want ErrInvalidParam", err)
	}
	if _, err := nt.AddEdge(0, 2, good); !errors.Is(err, ErrEdgeLimit) {
		t.Fatalf("full: err = %v, want ErrEdgeLimit", err)
	}
	if nt.EdgeCount() != 2 || nt.Version() != 2 {
		t.Fatalf("rejected ops mutated state: edges=%d version=%d", nt.EdgeCount(), nt.Version())
	}
}

func TestAnnounceRejectionOrder(t *testing.T) {
	nt := newNet(t, 3, 5)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}}) // edge 1, version 1

	// Invalid parameter beats edge-not-found.
	if err := nt.Announce(4, 0, []Segment{{1, 5}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad profile + missing edge: err = %v, want ErrInvalidParam", err)
	}
	if err := nt.Announce(0, 0, []Segment{{0, 5}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("edge id 0: err = %v, want ErrInvalidParam", err)
	}
	if err := nt.Announce(6, 0, []Segment{{0, 5}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("edge id beyond cap: err = %v, want ErrInvalidParam", err)
	}
	if err := nt.Announce(1, -1, []Segment{{0, 5}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative eff: err = %v, want ErrInvalidParam", err)
	}
	// Edge id inside the cap but never assigned.
	if err := nt.Announce(4, 0, []Segment{{0, 5}}); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("unassigned edge: err = %v, want ErrEdgeNotFound", err)
	}
	// Retroactive beats out-of-order.
	if err := nt.Announce(1, 3, []Segment{{0, 7}}); err != nil {
		t.Fatalf("Announce(1,3,...): %v", err)
	}
	if err := nt.Advance(10); err != nil {
		t.Fatalf("Advance(10): %v", err)
	}
	if err := nt.Announce(1, 2, []Segment{{0, 7}}); !errors.Is(err, ErrRetroactive) {
		t.Fatalf("eff<now and eff<last: err = %v, want ErrRetroactive", err)
	}
	// eff == now is allowed, now-1 is rejected.
	if err := nt.Announce(1, 10, []Segment{{0, 9}}); err != nil {
		t.Fatalf("eff==now should be accepted: %v", err)
	}
	if err := nt.Announce(1, 9, []Segment{{0, 9}}); !errors.Is(err, ErrRetroactive) {
		t.Fatalf("eff==now-1: err = %v, want ErrRetroactive", err)
	}
	// Out-of-order: eff >= now but below the last record's eff.
	if err := nt.Announce(1, 15, []Segment{{0, 1}}); err != nil {
		t.Fatalf("Announce(1,15,...): %v", err)
	}
	if err := nt.Announce(1, 12, []Segment{{0, 1}}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("eff<last: err = %v, want ErrOutOfOrder", err)
	}
	// Rejected announces changed nothing.
	if nt.Version() != 4 {
		t.Fatalf("version = %d, want 4 (rejected ops must not bump)", nt.Version())
	}
}

func TestRecordLimitAndReplacement(t *testing.T) {
	nt := newNet(t, 2, 2)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}}) // 1 record
	// 63 appends -> 64 live records, the maximum.
	for eff := int64(1); eff <= 63; eff++ {
		if err := nt.Announce(1, eff, []Segment{{0, 1}}); err != nil {
			t.Fatalf("append eff=%d: %v", eff, err)
		}
	}
	// The 65th live record is rejected.
	if err := nt.Announce(1, 64, []Segment{{0, 1}}); !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("65th record: err = %v, want ErrRecordLimit", err)
	}
	// Replacing the last record never counts against the limit.
	for i := 0; i < 10; i++ {
		if err := nt.Announce(1, 63, []Segment{{0, 2}}); err != nil {
			t.Fatalf("replace eff=63 (round %d): %v", i, err)
		}
	}
	// Still full for genuinely new effective times.
	if err := nt.Announce(1, 64, []Segment{{0, 1}}); !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("65th record after replaces: err = %v, want ErrRecordLimit", err)
	}
	// The replacement is visible at the current version: cost 2 from 63 on.
	res := mustQuery(t, nt, 0, 63, 1, nil)
	if res.Arrival != 65 {
		t.Fatalf("arrival = %d, want 65 (replaced profile cost 2)", res.Arrival)
	}
	t.Logf("记录上限: 64 条后追加被拒, 替换不计数, 当前剖面生效 arrival=%d", res.Arrival)
}

func TestAdvanceRules(t *testing.T) {
	nt := newNet(t, 2, 2)
	if err := nt.Advance(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Advance(-1): err = %v, want ErrInvalidParam", err)
	}
	if err := nt.Advance(MaxTime + 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Advance(MaxTime+1): err = %v, want ErrInvalidParam", err)
	}
	if err := nt.Advance(10); err != nil {
		t.Fatalf("Advance(10): %v", err)
	}
	if err := nt.Advance(9); !errors.Is(err, ErrClockBack) {
		t.Fatalf("Advance(9): err = %v, want ErrClockBack", err)
	}
	if err := nt.Advance(10); err != nil {
		t.Fatalf("Advance(10) equal should succeed: %v", err)
	}
	if nt.Now() != 10 || nt.Version() != 0 {
		t.Fatalf("now=%d version=%d, want 10/0", nt.Now(), nt.Version())
	}
}

func TestQueryValidation(t *testing.T) {
	nt := newNet(t, 3, 4)
	mustAdd(t, nt, 0, 1, []Segment{{0, 5}})
	if _, err := nt.EarliestArrival(-1, 0, 1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad source: err = %v", err)
	}
	if _, err := nt.EarliestArrival(0, 0, 3, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad goal: err = %v", err)
	}
	if _, err := nt.EarliestArrival(0, -1, 1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad t0: err = %v", err)
	}
	if _, err := nt.EarliestArrival(0, 0, 1, verPtr(-2)); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative ver: err = %v", err)
	}
	// Version check beats unreachable.
	if _, err := nt.EarliestArrival(1, 0, 2, verPtr(99)); !errors.Is(err, ErrVersionFuture) {
		t.Fatalf("future ver: err = %v, want ErrVersionFuture", err)
	}
	if _, err := nt.EarliestArrival(1, 0, 2, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("unreachable: err = %v, want ErrUnreachable", err)
	}
	// s == g: already there.
	res := mustQuery(t, nt, 2, 7, 2, nil)
	if res.Arrival != 7 || len(res.Legs) != 0 {
		t.Fatalf("s==g: arrival=%d legs=%v, want 7/[]", res.Arrival, res.Legs)
	}
}
