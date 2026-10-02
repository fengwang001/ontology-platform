package watermark

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, S, D int64, p Policy, AL, Cap int64) *Merger {
	t.Helper()
	m, err := New(S, D, p, AL, Cap)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d): %v", S, D, p, AL, Cap, err)
	}
	return m
}

func mustAdd(t *testing.T, m *Merger, ts, val int64) []LatePane {
	t.Helper()
	lates, err := m.Add(ts, val)
	if err != nil {
		t.Fatalf("Add(%d,%d): %v", ts, val, err)
	}
	return lates
}

func mustAdvance(t *testing.T, m *Merger, I2 int64) []OnTimePane {
	t.Helper()
	out, err := m.Advance(I2)
	if err != nil {
		t.Fatalf("Advance(%d): %v", I2, err)
	}
	return out
}

func TestSpecExample(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 5, 3)

	if lates := mustAdd(t, m, 3, 1); len(lates) != 0 {
		t.Fatalf("Add(3,1) lates=%v", lates)
	}
	want := []PaneInfo{
		{WS: -5, Count: 1, Sum: 1, MinTS: 3, MaxTS: 3, Hold: 3},
		{WS: 0, Count: 1, Sum: 1, MinTS: 3, MaxTS: 3, Hold: 3},
	}
	if got := m.Panes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Panes=%+v want %+v", got, want)
	}

	if out := mustAdvance(t, m, 4); len(out) != 0 {
		t.Fatalf("Advance(4) out=%v", out)
	}
	if got := m.Output(); got != 3 {
		t.Fatalf("O=%d want 3", got)
	}

	mustAdd(t, m, 2, 2)
	for _, p := range m.Panes() {
		if p.Hold != 3 {
			t.Fatalf("ws=%d hold=%d want 3 (clamped to O)", p.WS, p.Hold)
		}
	}

	out := mustAdvance(t, m, 7)
	wantOut := []OnTimePane{{WS: -5, Count: 2, Sum: 3, TS: 3}}
	if !reflect.DeepEqual(out, wantOut) {
		t.Fatalf("Advance(7)=%+v want %+v", out, wantOut)
	}
	if got := m.Output(); got != 3 {
		t.Fatalf("O=%d want 3", got)
	}

	mustAdd(t, m, 6, 4)
	want = []PaneInfo{
		{WS: 0, Count: 3, Sum: 7, MinTS: 2, MaxTS: 6, Hold: 3},
		{WS: 5, Count: 1, Sum: 4, MinTS: 6, MaxTS: 6, Hold: 6},
	}
	if got := m.Panes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Panes=%+v want %+v", got, want)
	}

	out = mustAdvance(t, m, 12)
	wantOut = []OnTimePane{{WS: 0, Count: 3, Sum: 7, TS: 3}}
	if !reflect.DeepEqual(out, wantOut) {
		t.Fatalf("Advance(12)=%+v want %+v", out, wantOut)
	}
	if got := m.Output(); got != 6 {
		t.Fatalf("O=%d want 6", got)
	}

	lates := mustAdd(t, m, 9, 8)
	wantLates := []LatePane{{WS: 0, Count: 1, Sum: 8, TS: 9}}
	if !reflect.DeepEqual(lates, wantLates) {
		t.Fatalf("Add(9,8) lates=%+v want %+v", lates, wantLates)
	}

	out = mustAdvance(t, m, 15)
	wantOut = []OnTimePane{{WS: 5, Count: 2, Sum: 12, TS: 6}}
	if !reflect.DeepEqual(out, wantOut) {
		t.Fatalf("Advance(15)=%+v want %+v", out, wantOut)
	}
	if got := m.Output(); got != 15 {
		t.Fatalf("O=%d want 15", got)
	}

	lates = mustAdd(t, m, 11, 16)
	wantLates = []LatePane{{WS: 5, Count: 1, Sum: 16, TS: 15}}
	if !reflect.DeepEqual(lates, wantLates) {
		t.Fatalf("Add(11,16) lates=%+v want %+v", lates, wantLates)
	}

	out = mustAdvance(t, m, 20)
	wantOut = []OnTimePane{{WS: 10, Count: 1, Sum: 16, TS: 15}}
	if !reflect.DeepEqual(out, wantOut) {
		t.Fatalf("Advance(20)=%+v want %+v", out, wantOut)
	}
	if got := m.Output(); got != 20 {
		t.Fatalf("O=%d want 20", got)
	}

	if lates := mustAdd(t, m, 1, 1); len(lates) != 0 {
		t.Fatalf("Add(1,1) lates=%v", lates)
	}
	if got := m.Dropped(); got != 2 {
		t.Fatalf("Dropped=%d want 2", got)
	}
}

// bruteWindows computes the window starts containing ts by exhaustive scan.
func bruteWindows(ts, S, D int64) []int64 {
	var out []int64
	for k := -(S/D + 2); k <= ts/D+1; k++ {
		ws := k * D
		if ws <= ts && ts < ws+S {
			out = append(out, ws)
		}
	}
	return out
}

func TestNegativeWindowRounding(t *testing.T) {
	// S=10, D=5: ts=3 must land in [-5,5) and [0,10); truncating
	// (ts-S+1)/D=-6/5 toward zero is fine here, but floor((ts-S+1)/D)
	// would wrongly add ws=-10, and truncating floor(ts/D) for
	// negative numerators would drop ws=-5 for other shapes.
	m := mustNew(t, 10, 5, Earliest, 0, 100)
	mustAdd(t, m, 3, 1)
	want := []PaneInfo{
		{WS: -5, Count: 1, Sum: 1, MinTS: 3, MaxTS: 3, Hold: 3},
		{WS: 0, Count: 1, Sum: 1, MinTS: 3, MaxTS: 3, Hold: 3},
	}
	if got := m.Panes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Panes=%+v want %+v", got, want)
	}

	// Exhaustive cross-check over many shapes and timestamps.
	for _, sd := range [][2]int64{{10, 5}, {16, 1}, {7, 3}, {1, 1}, {32, 2}, {9, 9}} {
		S, D := sd[0], sd[1]
		for ts := int64(0); ts < 4*S+3; ts++ {
			mm := mustNew(t, S, D, Earliest, 0, 1000)
			mustAdd(t, mm, ts, 1)
			var gotWS []int64
			for _, p := range mm.Panes() {
				gotWS = append(gotWS, p.WS)
			}
			wantWS := bruteWindows(ts, S, D)
			if !reflect.DeepEqual(gotWS, wantWS) {
				t.Fatalf("S=%d D=%d ts=%d: ws=%v want %v", S, D, ts, gotWS, wantWS)
			}
			for _, ws := range wantWS {
				if !(ws <= ts && ts < ws+S) {
					t.Fatalf("window [%d,%d) does not contain %d", ws, ws+S, ts)
				}
			}
		}
	}
}

func TestMixedClassesSameElement(t *testing.T) {
	// S=16, D=1, AL=2, I=110, ts=100: ends run 101..116.
	// dropped: end+2 <= 110 -> ends 101..108 (ws 85..92)
	// late:    end <= 110 < end+2 -> ends 109,110 (ws 93,94)
	// buffered: end > 110 -> ends 111..116 (ws 95..100)
	m := mustNew(t, 16, 1, Earliest, 2, 100)
	mustAdvance(t, m, 110)
	lates := mustAdd(t, m, 100, 7)
	wantLates := []LatePane{
		{WS: 93, Count: 1, Sum: 7, TS: 110},
		{WS: 94, Count: 1, Sum: 7, TS: 110},
	}
	if !reflect.DeepEqual(lates, wantLates) {
		t.Fatalf("lates=%+v want %+v", lates, wantLates)
	}
	if got := m.Dropped(); got != 8 {
		t.Fatalf("Dropped=%d want 8", got)
	}
	panes := m.Panes()
	if len(panes) != 6 || panes[0].WS != 95 || panes[5].WS != 100 {
		t.Fatalf("panes=%+v", panes)
	}
}

func TestBoundaryLateVsBuffered(t *testing.T) {
	// S=D=1: window for ts=5 is [5,6), end=6.
	// I == end-1 -> buffered.
	m := mustNew(t, 1, 1, Earliest, 10, 10)
	mustAdvance(t, m, 5)
	if lates := mustAdd(t, m, 5, 1); len(lates) != 0 {
		t.Fatalf("I=end-1: lates=%v want none", lates)
	}
	if n := len(m.Panes()); n != 1 {
		t.Fatalf("I=end-1: panes=%d want 1", n)
	}
	// I == end -> late.
	m2 := mustNew(t, 1, 1, Earliest, 10, 10)
	mustAdvance(t, m2, 6)
	lates := mustAdd(t, m2, 5, 1)
	if len(lates) != 1 || lates[0].WS != 5 {
		t.Fatalf("I=end: lates=%v want one late pane ws=5", lates)
	}
	if n := len(m2.Panes()); n != 0 {
		t.Fatalf("I=end: panes=%d want 0", n)
	}
}

func TestBoundaryDropVsLate(t *testing.T) {
	// S=D=1, AL=10: window for ts=5 has end=6, end+AL=16.
	// I == end+AL-1 -> late.
	m := mustNew(t, 1, 1, Earliest, 10, 10)
	mustAdvance(t, m, 15)
	lates := mustAdd(t, m, 5, 1)
	if len(lates) != 1 || lates[0].WS != 5 {
		t.Fatalf("I=end+AL-1: lates=%v want one late pane", lates)
	}
	if got := m.Dropped(); got != 0 {
		t.Fatalf("I=end+AL-1: dropped=%d want 0", got)
	}
	// I == end+AL -> dropped.
	m2 := mustNew(t, 1, 1, Earliest, 10, 10)
	mustAdvance(t, m2, 16)
	if lates := mustAdd(t, m2, 5, 1); len(lates) != 0 {
		t.Fatalf("I=end+AL: lates=%v want none", lates)
	}
	if got := m2.Dropped(); got != 1 {
		t.Fatalf("I=end+AL: dropped=%d want 1", got)
	}
}

func TestALZeroNoLate(t *testing.T) {
	m := mustNew(t, 4, 2, Earliest, 0, 10)
	mustAdvance(t, m, 100)
	lates := mustAdd(t, m, 3, 5)
	if len(lates) != 0 {
		t.Fatalf("AL=0: lates=%v want none", lates)
	}
	// ts=3 belongs to [0,4) and [2,6): both ends <= 100 = end+0.
	if got := m.Dropped(); got != 2 {
		t.Fatalf("AL=0: dropped=%d want 2", got)
	}
}

func TestHoldClampedToO(t *testing.T) {
	// EARLIEST: an element with smaller ts must not lower hold below O.
	m := mustNew(t, 10, 5, Earliest, 0, 10)
	mustAdd(t, m, 9, 1) // windows [0,10),[5,15): hold 9
	mustAdvance(t, m, 5)
	if got := m.Output(); got != 5 {
		t.Fatalf("O=%d want 5", got)
	}
	mustAdd(t, m, 2, 1) // joins [0,10) (and [-5,5)); raw=2 < O=5
	for _, p := range m.Panes() {
		if p.WS == 0 && p.Hold != 5 {
			t.Fatalf("hold=%d want 5 (clamped to O)", p.Hold)
		}
		if p.Hold < m.Output() {
			t.Fatalf("hold %d < O %d", p.Hold, m.Output())
		}
	}
}

func TestEndPolicyHold(t *testing.T) {
	m := mustNew(t, 10, 5, End, 0, 10)
	mustAdd(t, m, 3, 1)
	mustAdd(t, m, 7, 1)
	mustAdd(t, m, 1, 1)
	for _, p := range m.Panes() {
		want := p.WS + 10 - 1
		if p.Hold != want {
			t.Fatalf("ws=%d hold=%d want end-1=%d", p.WS, p.Hold, want)
		}
	}
}

func TestLatestPolicyHoldRisesAndEqualI(t *testing.T) {
	// LATEST: hold follows max ts; Advance with I' == I can still raise O.
	m := mustNew(t, 10, 5, Latest, 0, 10)
	mustAdd(t, m, 3, 1) // windows [-5,5),[0,10): hold 3
	out := mustAdvance(t, m, 7)
	if len(out) != 1 || out[0].WS != -5 {
		t.Fatalf("Advance(7)=%+v", out)
	}
	if got := m.Output(); got != 3 {
		t.Fatalf("O=%d want 3", got)
	}
	mustAdd(t, m, 9, 1) // windows [0,10),[5,15): hold=max(9,3)=9
	for _, p := range m.Panes() {
		if p.Hold != 9 {
			t.Fatalf("ws=%d hold=%d want 9", p.WS, p.Hold)
		}
	}
	// I' == I == 7: no new expiry, but O rises to min(7, 9).
	if out := mustAdvance(t, m, 7); len(out) != 0 {
		t.Fatalf("Advance(7) again: out=%v", out)
	}
	if got := m.Output(); got != 7 {
		t.Fatalf("O=%d want 7 after equal-I advance", got)
	}
}

func TestNoRemainingPanesTakesI(t *testing.T) {
	m := mustNew(t, 4, 2, Earliest, 0, 10)
	mustAdvance(t, m, 42)
	if got := m.Output(); got != 42 {
		t.Fatalf("empty table: O=%d want 42", got)
	}
	mustAdd(t, m, 100, 1) // windows [100,104),[98,102): buffered
	out := mustAdvance(t, m, 200)
	if len(out) != 2 {
		t.Fatalf("Advance(200) out=%+v want 2 panes", out)
	}
	if got := m.Output(); got != 200 {
		t.Fatalf("drained table: O=%d want 200", got)
	}
}

func TestLateOutputTimestampMaxWithO(t *testing.T) {
	// END policy: f = end-1 = 9 < O = 12 -> late ts must be 12.
	m := mustNew(t, 10, 5, End, 10, 10)
	mustAdd(t, m, 7, 1) // windows [0,10) hold 9, [5,15) hold 14
	out := mustAdvance(t, m, 12)
	if len(out) != 1 || out[0].WS != 0 || out[0].TS != 9 {
		t.Fatalf("Advance(12)=%+v", out)
	}
	if got := m.Output(); got != 12 {
		t.Fatalf("O=%d want 12", got)
	}
	lates := mustAdd(t, m, 8, 3) // [0,10) late (end 10 <= I=12 < 20)
	if len(lates) != 1 || lates[0].WS != 0 || lates[0].TS != 12 {
		t.Fatalf("lates=%+v want one pane ts=12=max(9,O)", lates)
	}
}

func TestCapacityRejectionIsAtomic(t *testing.T) {
	// S=25, D=5, AL=5, I=16, ts=6: windows ws=-15,-10,-5,0,5,
	// ends 10,15,20,25,30 -> 1 drop, 1 late, 3 buffered-new.
	// Cap=2 rejects the whole Add: no lates, no drops, no state change.
	m := mustNew(t, 25, 5, Earliest, 5, 2)
	mustAdvance(t, m, 16)
	before := m.Panes()
	lates, err := m.Add(6, 9)
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("err=%v want ErrCapacity", err)
	}
	if lates != nil {
		t.Fatalf("lates=%v want nil on rejection", lates)
	}
	if got := m.Dropped(); got != 0 {
		t.Fatalf("dropped=%d want 0 after rejection", got)
	}
	if got := m.Panes(); !reflect.DeepEqual(got, before) {
		t.Fatalf("panes changed after rejection: %+v", got)
	}
	if got := m.Output(); got != 16 {
		t.Fatalf("O=%d want 16 after rejection", got)
	}

	// Same shape with enough capacity: drop, late and buffer all apply.
	m2 := mustNew(t, 25, 5, Earliest, 5, 10)
	mustAdvance(t, m2, 16)
	lates, err = m2.Add(6, 9)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	wantLates := []LatePane{{WS: -10, Count: 1, Sum: 9, TS: 16}}
	if !reflect.DeepEqual(lates, wantLates) {
		t.Fatalf("lates=%+v want %+v", lates, wantLates)
	}
	if got := m2.Dropped(); got != 1 {
		t.Fatalf("dropped=%d want 1", got)
	}
	if n := len(m2.Panes()); n != 3 {
		t.Fatalf("panes=%d want 3", n)
	}
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 5, 2)
	mustAdd(t, m, 3, 1) // fills Cap=2 with windows [-5,5) and [0,10)
	if _, err := m.Add(20, 1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("err=%v want ErrCapacity (table full)", err)
	}

	snap := func() (int64, int64, []PaneInfo) {
		return m.Output(), m.Dropped(), m.Panes()
	}

	// Invalid params on Add.
	for _, tv := range [][2]int64{{-1, 0}, {maxTS + 1, 0}, {0, -1_000_000_001}, {0, 1_000_000_001}} {
		if _, err := m.Add(tv[0], tv[1]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Add%v err=%v want ErrInvalidParam", tv, err)
		}
	}
	// Invalid then regression on Advance; precedence: invalid first.
	mustAdvance(t, m, 10)
	o0, d0, p0 := snap()
	if _, err := m.Advance(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Advance(-1) err=%v want ErrInvalidParam", err)
	}
	if _, err := m.Advance(maxTS + 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Advance(maxTS+1) err=%v want ErrInvalidParam", err)
	}
	if _, err := m.Advance(9); !errors.Is(err, ErrRegression) {
		t.Fatalf("Advance(9) err=%v want ErrRegression", err)
	}
	// Add precedence: invalid param beats capacity.
	if _, err := m.Add(-5, 1_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Add invalid err=%v want ErrInvalidParam", err)
	}
	// Advance precedence: invalid beats regression.
	if _, err := m.Advance(-100); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Advance(-100) err=%v want ErrInvalidParam", err)
	}

	o1, d1, p1 := snap()
	if o1 != 10 || d1 != d0 || !reflect.DeepEqual(p1, p0) {
		t.Fatalf("state changed by rejected ops: O %d->%d dropped %d->%d panes %+v->%+v",
			o0, o1, d0, d1, p0, p1)
	}
	_ = o0
}

func TestConstructorValidation(t *testing.T) {
	bad := [][5]int64{
		{0, 1, 0, 0, 1},              // S < 1
		{10, 0, 0, 0, 1},             // D < 1
		{5, 10, 0, 0, 1},             // D > S
		{17, 1, 0, 0, 1},             // S > 16*D
		{1_000_000_001, 1, 0, 0, 1},  // S > 1e9... but S<=16D fails first; use D big
		{10, 5, 0, -1, 1},            // AL < 0
		{10, 5, 0, 1_000_000_001, 1}, // AL > 1e9
		{10, 5, 0, 0, 0},             // Cap < 1
		{10, 5, 0, 0, 1_000_001},     // Cap > 1e6
	}
	for _, c := range bad {
		if _, err := New(c[0], c[1], Earliest, c[3], c[4]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New%v err=%v want ErrInvalidParam", c, err)
		}
	}
	if _, err := New(1_000_000_001, 100_000_000, Earliest, 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("S>1e9 with valid D: err=%v", err)
	}
	if _, err := New(10, 5, Policy(3), 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad policy: err=%v", err)
	}
	if _, err := New(16, 1, Latest, 1_000_000_000, 1_000_000); err != nil {
		t.Fatalf("boundary-valid New: %v", err)
	}
}

func TestHoldProbesIndependentOfTableSize(t *testing.T) {
	// S=D=1: each element creates exactly one pane, one hold entry,
	// no updates, no expiries before the probe -> identical probe cost.
	build := func(n int) *Merger {
		m := mustNew(t, 1, 1, Earliest, 0, 1_000_000)
		for i := 0; i < n; i++ {
			mustAdd(t, m, int64(i), 1)
		}
		return m
	}
	small, large := build(10), build(10000)

	p0 := small.holdProbes
	if out := mustAdvance(t, small, 0); len(out) != 0 {
		t.Fatalf("small: unexpected emission %v", out)
	}
	deltaSmall := small.holdProbes - p0

	p0 = large.holdProbes
	if out := mustAdvance(t, large, 0); len(out) != 0 {
		t.Fatalf("large: unexpected emission %v", out)
	}
	deltaLarge := large.holdProbes - p0

	if deltaSmall != deltaLarge {
		t.Fatalf("holdProbes delta differs: 10 panes=%d, 10000 panes=%d", deltaSmall, deltaLarge)
	}
	t.Logf("holdProbes delta with 10 panes: %d; with 10000 panes: %d (must be equal)",
		deltaSmall, deltaLarge)
}

func TestHoldProbesBound(t *testing.T) {
	// Every Advance must satisfy:
	//   probes <= emitted + 2 + staleDiscarded.
	// Build stale entries via hold updates (LATEST) and expiries.
	m := mustNew(t, 4, 1, Latest, 0, 1000)
	for ts := int64(0); ts < 40; ts++ {
		mustAdd(t, m, ts, 1) // repeated updates to existing panes -> stale entries
	}
	for I2 := int64(1); I2 <= 60; I2++ {
		p0, s0 := m.holdProbes, m.holdStale
		out := mustAdvance(t, m, I2)
		probes := m.holdProbes - p0
		stale := m.holdStale - s0
		if probes > int64(len(out))+2+stale {
			t.Fatalf("I'=%d: probes=%d > emitted=%d + 2 + stale=%d", I2, probes, len(out), stale)
		}
	}
}
