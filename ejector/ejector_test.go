package ejector

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, n int, k int64, b, capDur int64, p, wn, q int) *Ejector {
	t.Helper()
	e, err := NewEjector(n, k, b, capDur, p, wn, q)
	if err != nil {
		t.Fatalf("NewEjector(%d,%d,%d,%d,%d,%d,%d): %v", n, k, b, capDur, p, wn, q, err)
	}
	return e
}

func mustReport(t *testing.T, e *Ejector, host int, ok bool, now int64) Result {
	t.Helper()
	r, err := e.Report(host, ok, now)
	if err != nil {
		t.Fatalf("Report(%d,%v,%d) rejected: %v", host, ok, now, err)
	}
	return r
}

func wantState(t *testing.T, e *Ejector, host int, c, em, u int64, win []bool) {
	t.Helper()
	h := e.hosts[host]
	if h.c != c || h.e != em || h.u != u {
		t.Fatalf("host %d: got (c=%d,e=%d,u=%d), want (c=%d,e=%d,u=%d)",
			host, h.c, h.e, h.u, c, em, u)
	}
	if len(win) == 0 {
		if len(h.win) != 0 {
			t.Fatalf("host %d: got win=%v, want empty", host, h.win)
		}
		return
	}
	if !reflect.DeepEqual(h.win, win) {
		t.Fatalf("host %d: got win=%v, want %v", host, h.win, win)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		n    int
		k    int64
		b    int64
		capD int64
		p    int
		wn   int
		q    int
	}{
		{"N=0", 0, 3, 10, 25, 50, 4, 75},
		{"N<0", -1, 3, 10, 25, 50, 4, 75},
		{"K=0", 4, 0, 10, 25, 50, 4, 75},
		{"K<0", 4, -2, 10, 25, 50, 4, 75},
		{"B=0", 4, 3, 0, 25, 50, 4, 75},
		{"B<0", 4, 3, -1, 25, 50, 4, 75},
		{"B>1e9", 4, 3, 1_000_000_001, 2_000_000_000, 50, 4, 75},
		{"Cap<B", 4, 3, 10, 9, 50, 4, 75},
		{"Cap>1e9", 4, 3, 10, 1_000_000_001, 50, 4, 75},
		{"P<0", 4, 3, 10, 25, -1, 4, 75},
		{"P>100", 4, 3, 10, 25, 101, 4, 75},
		{"Wn=0", 4, 3, 10, 25, 50, 0, 75},
		{"Wn>64", 4, 3, 10, 25, 50, 65, 75},
		{"Q=0", 4, 3, 10, 25, 50, 4, 0},
		{"Q>100", 4, 3, 10, 25, 50, 4, 101},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEjector(tc.n, tc.k, tc.b, tc.capD, tc.p, tc.wn, tc.q); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("got err=%v, want ErrInvalidConfig", err)
			}
		})
	}
	// Boundary values that must be accepted.
	if _, err := NewEjector(1, 1, 1, 1, 0, 1, 1); err != nil {
		t.Fatalf("minimal valid config rejected: %v", err)
	}
	if _, err := NewEjector(4, 3, 1_000_000_000, 1_000_000_000, 100, 64, 100); err != nil {
		t.Fatalf("maximal valid config rejected: %v", err)
	}
}

// TestPromptExampleConsecutive walks the first prompt example:
// N=4, K=3, B=10, Cap=25, P=50, Wn=4, Q=75, host 0 fails at 1,2,3 then
// 13,14,15 then 35,36,37, giving u=13, u=35, u=62 with e=1,2,3.
func TestPromptExampleConsecutive(t *testing.T) {
	e := mustNew(t, 4, 3, 10, 25, 50, 4, 75)

	if r := mustReport(t, e, 0, false, 1); r != Recorded {
		t.Fatalf("t=1: got %v, want Recorded", r)
	}
	if r := mustReport(t, e, 0, false, 2); r != Recorded {
		t.Fatalf("t=2: got %v, want Recorded", r)
	}
	// c reaches K=3 while win length 3 < Wn=4, so only the consecutive
	// condition fires; e=1, u=3+min(25,10)=13.
	if r := mustReport(t, e, 0, false, 3); r != Ejected {
		t.Fatalf("t=3: got %v, want Ejected", r)
	}
	wantState(t, e, 0, 0, 1, 13, nil)

	// Reports while ejected are ignored and change nothing.
	if r := mustReport(t, e, 0, false, 13); r != Recorded {
		t.Fatalf("t=13: got %v, want Recorded (recovered exactly at u)", r)
	}
	if r := mustReport(t, e, 0, false, 14); r != Recorded {
		t.Fatalf("t=14: got %v, want Recorded", r)
	}
	// e=2, u=15+min(25,20)=35.
	if r := mustReport(t, e, 0, false, 15); r != Ejected {
		t.Fatalf("t=15: got %v, want Ejected", r)
	}
	wantState(t, e, 0, 0, 2, 35, nil)

	mustReport(t, e, 0, false, 35)
	mustReport(t, e, 0, false, 36)
	// e=3, u=37+min(25,30)=62: Cap truncates the linear growth.
	if r := mustReport(t, e, 0, false, 37); r != Ejected {
		t.Fatalf("t=37: got %v, want Ejected", r)
	}
	wantState(t, e, 0, 0, 3, 62, nil)
}

// TestPromptExampleWindowAndCap walks the second prompt example:
// N=4, K=3, B=10, Cap=25, P=50, Wn=4, Q=50.
func TestPromptExampleWindowAndCap(t *testing.T) {
	e := mustNew(t, 4, 3, 10, 25, 50, 4, 50)

	// Host 1: F,S,F,F at 1..4 -> win=[F,S,F,F], f=3, 300>=200 triggers
	// even though c=2 < K=3. e=1, u=4+10=14.
	mustReport(t, e, 1, false, 1)
	mustReport(t, e, 1, true, 2)
	mustReport(t, e, 1, false, 3)
	if r := mustReport(t, e, 1, false, 4); r != Ejected {
		t.Fatalf("host1 t=4: got %v, want Ejected", r)
	}
	wantState(t, e, 1, 0, 1, 14, nil)

	// Host 2: S,F,S,F at 5..8 -> win=[S,F,S,F], f=2, 200==200 triggers.
	// E=1 (host 1 still ejected), (1+1)*100==200==P*N allowed.
	mustReport(t, e, 2, true, 5)
	mustReport(t, e, 2, false, 6)
	mustReport(t, e, 2, true, 7)
	if r := mustReport(t, e, 2, false, 8); r != Ejected {
		t.Fatalf("host2 t=8: got %v, want Ejected", r)
	}
	wantState(t, e, 2, 0, 1, 18, nil)

	// Host 3: F,F,F at 9..11 -> c=3 triggers but E=2, 300>200 capped.
	mustReport(t, e, 3, false, 9)
	mustReport(t, e, 3, false, 10)
	if r := mustReport(t, e, 3, false, 11); r != Capped {
		t.Fatalf("host3 t=11: got %v, want Capped", r)
	}
	wantState(t, e, 3, 3, 0, 0, []bool{false, false, false})

	// t=12: c=4, win full with f=4, still capped; c and win retained.
	if r := mustReport(t, e, 3, false, 12); r != Capped {
		t.Fatalf("host3 t=12: got %v, want Capped", r)
	}
	wantState(t, e, 3, 4, 0, 0, []bool{false, false, false, false})

	// t=14: host 1 recovered exactly at u=14, host 2 still ejected, E=1,
	// (1+1)*100==200 allowed. Host 3 ejected: e=1, u=24, c=0, win empty.
	if r := mustReport(t, e, 3, false, 14); r != Ejected {
		t.Fatalf("host3 t=14: got %v, want Ejected", r)
	}
	wantState(t, e, 3, 0, 1, 24, nil)

	healthy, err := e.Healthy(14)
	if err != nil {
		t.Fatalf("Healthy(14): %v", err)
	}
	if !reflect.DeepEqual(healthy, []int{0, 1}) {
		t.Fatalf("Healthy(14)=%v, want [0 1] (host 2 until 18, host 3 until 24)", healthy)
	}
}

// TestPromptExampleBatch walks the batch prompt example: a fresh instance
// with Wn=4, Q=50 receives [(0,F,5),(0,F,3),(0,F,4)]; processing order is
// t=3,4,5 and only the t=5 event (input index 0) ejects.
func TestPromptExampleBatch(t *testing.T) {
	e := mustNew(t, 4, 3, 10, 25, 50, 4, 50)
	got, err := e.ReportBatch([]Event{
		{Host: 0, OK: false, Now: 5},
		{Host: 0, OK: false, Now: 3},
		{Host: 0, OK: false, Now: 4},
	})
	if err != nil {
		t.Fatalf("ReportBatch: %v", err)
	}
	want := []Result{Ejected, Recorded, Recorded}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	wantState(t, e, 0, 0, 1, 15, nil)
}

// TestRecoveredExactlyAtU: at now == u the host is healthy again and the
// failure counts into c and win.
func TestRecoveredExactlyAtU(t *testing.T) {
	e := mustNew(t, 2, 2, 10, 10, 100, 4, 50)
	mustReport(t, e, 0, false, 1)
	if r := mustReport(t, e, 0, false, 2); r != Ejected {
		t.Fatalf("t=2: got %v, want Ejected (u=12)", r)
	}
	wantState(t, e, 0, 0, 1, 12, nil)

	// One instant before u the host is still ejected: report ignored.
	if r := mustReport(t, e, 0, false, 11); r != Ignored {
		t.Fatalf("t=11: got %v, want Ignored", r)
	}
	wantState(t, e, 0, 0, 1, 12, nil)

	// Exactly at u the host has recovered; the failure is recorded.
	if r := mustReport(t, e, 0, false, 12); r != Recorded {
		t.Fatalf("t=12: got %v, want Recorded", r)
	}
	wantState(t, e, 0, 1, 1, 12, []bool{false})

	ej, err := e.Ejected(0, 12)
	if err != nil || ej {
		t.Fatalf("Ejected(0,12)=%v,%v, want false,nil", ej, err)
	}
}

// TestConsecutiveThresholdBoundary: c=K-1 does not trigger, c=K does.
func TestConsecutiveThresholdBoundary(t *testing.T) {
	e := mustNew(t, 1, 3, 10, 10, 100, 4, 100)
	if r := mustReport(t, e, 0, false, 1); r != Recorded {
		t.Fatalf("c=1: got %v, want Recorded", r)
	}
	if r := mustReport(t, e, 0, false, 2); r != Recorded {
		t.Fatalf("c=2=K-1: got %v, want Recorded", r)
	}
	if r := mustReport(t, e, 0, false, 3); r != Ejected {
		t.Fatalf("c=3=K: got %v, want Ejected", r)
	}
}

// TestWindowLengthBoundary: the window condition is only evaluated when
// the window length is exactly Wn, not Wn-1.
func TestWindowLengthBoundary(t *testing.T) {
	// K huge so only the window condition can fire; Q=100, Wn=3.
	e := mustNew(t, 1, 100, 10, 10, 100, 3, 100)
	if r := mustReport(t, e, 0, false, 1); r != Recorded {
		t.Fatalf("len=1: got %v, want Recorded", r)
	}
	if r := mustReport(t, e, 0, false, 2); r != Recorded {
		t.Fatalf("len=2=Wn-1: got %v, want Recorded (window not full)", r)
	}
	if r := mustReport(t, e, 0, false, 3); r != Ejected {
		t.Fatalf("len=3=Wn, f=3, 300>=300: got %v, want Ejected", r)
	}
}

// TestWindowRateEquality: f*100 == Q*Wn triggers, one failure less does not.
func TestWindowRateEquality(t *testing.T) {
	// Wn=4, Q=75: need f*100 >= 300, i.e. f=3. K huge.
	e := mustNew(t, 1, 100, 10, 10, 100, 4, 75)
	mustReport(t, e, 0, false, 1)
	mustReport(t, e, 0, false, 2)
	mustReport(t, e, 0, true, 3)
	// win=[F,F,S,F]: f=3, 300==300 triggers.
	if r := mustReport(t, e, 0, false, 4); r != Ejected {
		t.Fatalf("f=3: got %v, want Ejected", r)
	}

	// Same setup but only f=2: 200 < 300, no trigger.
	e2 := mustNew(t, 1, 100, 10, 10, 100, 4, 75)
	mustReport(t, e2, 0, false, 1)
	mustReport(t, e2, 0, false, 2)
	mustReport(t, e2, 0, true, 3)
	if r := mustReport(t, e2, 0, true, 4); r != Recorded {
		t.Fatalf("f=2: got %v, want Recorded", r)
	}
	wantState(t, e2, 0, 0, 0, 0, []bool{false, false, true, true})
}

// TestWindowTriggersAloneBelowK: the window condition fires on its own
// while c stays below K.
func TestWindowTriggersAloneBelowK(t *testing.T) {
	e := mustNew(t, 1, 5, 10, 10, 100, 4, 50)
	mustReport(t, e, 0, false, 1) // c=1
	mustReport(t, e, 0, true, 2)  // c=0
	mustReport(t, e, 0, false, 3) // c=1
	// c=2 < K=5, but win=[F,S,F,F], f=3, 300>=200.
	if r := mustReport(t, e, 0, false, 4); r != Ejected {
		t.Fatalf("got %v, want Ejected via window condition alone", r)
	}
}

// TestRatioCapEquality: (E+1)*100 == P*N is allowed, one more is capped.
func TestRatioCapEquality(t *testing.T) {
	// N=3, P=100: floor(3*100/100)=3 may be ejected. K=1 ejects on first
	// failure with long durations so nobody recovers.
	e := mustNew(t, 3, 1, 1000, 1000, 100, 4, 50)
	if r := mustReport(t, e, 0, false, 1); r != Ejected {
		t.Fatalf("host0: got %v, want Ejected (E=0)", r)
	}
	if r := mustReport(t, e, 1, false, 2); r != Ejected {
		t.Fatalf("host1: got %v, want Ejected (E=1)", r)
	}
	// E=2, (2+1)*100 == 300 == P*N: still allowed.
	if r := mustReport(t, e, 2, false, 3); r != Ejected {
		t.Fatalf("host2: got %v, want Ejected (E=2, equality)", r)
	}

	// N=3, P=66: floor(66*3/100)=1. First ejection allowed, second capped.
	e2 := mustNew(t, 3, 1, 1000, 1000, 66, 4, 50)
	if r := mustReport(t, e2, 0, false, 1); r != Ejected {
		t.Fatalf("host0: got %v, want Ejected", r)
	}
	// E=1, (1+1)*100=200 > 66*3=198: capped.
	if r := mustReport(t, e2, 1, false, 2); r != Capped {
		t.Fatalf("host1: got %v, want Capped", r)
	}
	wantState(t, e2, 1, 1, 0, 0, []bool{false})
}

// TestCappedRetryAfterRecovery: a capped host keeps c and win, retries on
// every later failure, and succeeds once another host recovers.
func TestCappedRetryAfterRecovery(t *testing.T) {
	// N=2, P=50: only one host may be ejected at a time. K=1, B=5.
	e := mustNew(t, 2, 1, 5, 5, 50, 2, 50)
	if r := mustReport(t, e, 0, false, 1); r != Ejected {
		t.Fatalf("host0: got %v, want Ejected (u=6)", r)
	}
	// Host 1 triggers at t=2 but E=1, (1+1)*100=200 > 100: capped.
	if r := mustReport(t, e, 1, false, 2); r != Capped {
		t.Fatalf("host1 t=2: got %v, want Capped", r)
	}
	wantState(t, e, 1, 1, 0, 0, []bool{false})
	// Still capped at t=5; c and win keep growing.
	if r := mustReport(t, e, 1, false, 5); r != Capped {
		t.Fatalf("host1 t=5: got %v, want Capped", r)
	}
	wantState(t, e, 1, 2, 0, 0, []bool{false, false})
	// t=6: host 0 recovered (u=6), E=0, retry succeeds.
	if r := mustReport(t, e, 1, false, 6); r != Ejected {
		t.Fatalf("host1 t=6: got %v, want Ejected", r)
	}
	wantState(t, e, 1, 0, 1, 11, nil)
}

// TestSuccessDecrementsE: a success reduces e by one, never below zero.
func TestSuccessDecrementsE(t *testing.T) {
	e := mustNew(t, 1, 2, 10, 100, 100, 4, 100)
	mustReport(t, e, 0, false, 1)
	if r := mustReport(t, e, 0, false, 2); r != Ejected {
		t.Fatalf("t=2: got %v, want Ejected (e=1, u=12)", r)
	}
	mustReport(t, e, 0, false, 12) // c=1
	if r := mustReport(t, e, 0, false, 13); r != Ejected {
		t.Fatalf("t=13: got %v, want Ejected (e=2, u=33)", r)
	}
	wantState(t, e, 0, 0, 2, 33, nil)
	// Success at t=33: e 2 -> 1.
	if r := mustReport(t, e, 0, true, 33); r != Recorded {
		t.Fatalf("t=33: got %v, want Recorded", r)
	}
	wantState(t, e, 0, 0, 1, 33, []bool{true})
	// Another success: e 1 -> 0.
	mustReport(t, e, 0, true, 34)
	wantState(t, e, 0, 0, 0, 33, []bool{true, true})
	// e already 0: no underflow.
	mustReport(t, e, 0, true, 35)
	wantState(t, e, 0, 0, 0, 33, []bool{true, true, true})
}

// TestIgnoredReportChangesNothing: reports to an ejected host are ignored
// and leave c, e, u, win untouched.
func TestIgnoredReportChangesNothing(t *testing.T) {
	e := mustNew(t, 1, 2, 10, 10, 100, 4, 50)
	mustReport(t, e, 0, false, 1)
	mustReport(t, e, 0, false, 2) // ejected, u=12
	mustReport(t, e, 0, false, 12)
	wantState(t, e, 0, 1, 1, 12, []bool{false})

	if r := mustReport(t, e, 0, false, 13); r != Ejected {
		t.Fatalf("t=13: got %v, want Ejected (u=23)", r)
	}
	// Both failure and success reports are ignored while ejected.
	if r := mustReport(t, e, 0, false, 14); r != Ignored {
		t.Fatalf("t=14: got %v, want Ignored", r)
	}
	if r := mustReport(t, e, 0, true, 15); r != Ignored {
		t.Fatalf("t=15: got %v, want Ignored", r)
	}
	wantState(t, e, 0, 0, 2, 23, nil)
}

// TestPZeroNeverEjects: with P=0 every trigger is capped, forever.
func TestPZeroNeverEjects(t *testing.T) {
	e := mustNew(t, 2, 1, 10, 10, 0, 4, 50)
	for now := int64(1); now <= 5; now++ {
		if r := mustReport(t, e, 0, false, now); r != Capped {
			t.Fatalf("t=%d: got %v, want Capped", now, r)
		}
	}
	wantState(t, e, 0, 5, 0, 0, []bool{false, false, false, false})
	healthy, err := e.Healthy(5)
	if err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	if !reflect.DeepEqual(healthy, []int{0, 1}) {
		t.Fatalf("Healthy(5)=%v, want [0 1]", healthy)
	}
}

// TestPHundredAlwaysAllows: with P=100 every host may be ejected at once.
func TestPHundredAlwaysAllows(t *testing.T) {
	e := mustNew(t, 3, 1, 10, 10, 100, 4, 50)
	for host := 0; host < 3; host++ {
		if r := mustReport(t, e, host, false, int64(host+1)); r != Ejected {
			t.Fatalf("host %d: got %v, want Ejected", host, r)
		}
	}
	healthy, err := e.Healthy(3)
	if err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	if len(healthy) != 0 {
		t.Fatalf("Healthy(3)=%v, want empty", healthy)
	}
}

// TestBatchStableSortSameNow: events with equal now keep input order, and
// results are returned in input order.
func TestBatchStableSortSameNow(t *testing.T) {
	// K=2: with same-now events the relative order decides which one ejects.
	e := mustNew(t, 1, 2, 10, 10, 100, 4, 50)
	got, err := e.ReportBatch([]Event{
		{Host: 0, OK: false, Now: 5}, // idx 0: processed 3rd -> ejects
		{Host: 0, OK: false, Now: 2}, // idx 1: processed 1st
		{Host: 0, OK: false, Now: 2}, // idx 2: processed 2nd -> ejects (c=2)
	})
	if err != nil {
		t.Fatalf("ReportBatch: %v", err)
	}
	// Processing order: idx1 (c=1), idx2 (c=2 -> eject, u=12), idx0 (ignored).
	want := []Result{Ignored, Recorded, Ejected}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	wantState(t, e, 0, 0, 1, 12, nil)
	if e.maxNow != 5 {
		t.Fatalf("maxNow=%d, want 5", e.maxNow)
	}
}

// TestBatchPrecheckAtomic: a batch with any invalid event is rejected as a
// whole, reporting the first problematic event, changing nothing.
func TestBatchPrecheckAtomic(t *testing.T) {
	e := mustNew(t, 2, 2, 10, 10, 100, 4, 50)
	mustReport(t, e, 0, false, 10) // maxNow=10, c=1

	// Host range is checked before time range; first bad event wins.
	_, err := e.ReportBatch([]Event{
		{Host: 0, OK: false, Now: 11},
		{Host: 5, OK: false, Now: -3}, // idx 1: host bad (and time bad)
		{Host: 0, OK: false, Now: -1}, // idx 2: time bad
	})
	if !errors.Is(err, ErrHostOutOfRange) {
		t.Fatalf("got err=%v, want ErrHostOutOfRange", err)
	}
	// First problematic event is reported: idx 0 has a bad host here.
	_, err = e.ReportBatch([]Event{
		{Host: -1, OK: true, Now: 11},
		{Host: 0, OK: true, Now: 12},
	})
	if !errors.Is(err, ErrHostOutOfRange) {
		t.Fatalf("got err=%v, want ErrHostOutOfRange", err)
	}
	// Time range error when hosts are fine.
	_, err = e.ReportBatch([]Event{
		{Host: 0, OK: true, Now: 11},
		{Host: 1, OK: true, Now: 1_000_000_000_000_001},
	})
	if !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("got err=%v, want ErrInvalidTime", err)
	}
	// Clock regression: batch minimum below maxNow.
	_, err = e.ReportBatch([]Event{
		{Host: 0, OK: true, Now: 12},
		{Host: 1, OK: true, Now: 9},
	})
	if !errors.Is(err, ErrClockRegression) {
		t.Fatalf("got err=%v, want ErrClockRegression", err)
	}
	// Nothing changed: state and maxNow intact.
	wantState(t, e, 0, 1, 0, 0, []bool{false})
	wantState(t, e, 1, 0, 0, 0, nil)
	if e.maxNow != 10 {
		t.Fatalf("maxNow=%d, want 10", e.maxNow)
	}
}

// TestEmptyBatch: an empty batch returns an empty slice and changes nothing.
func TestEmptyBatch(t *testing.T) {
	e := mustNew(t, 1, 1, 10, 10, 100, 4, 50)
	got, err := e.ReportBatch(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v,%v, want empty,nil", got, err)
	}
	if e.maxNow != 0 {
		t.Fatalf("maxNow=%d, want 0", e.maxNow)
	}
}

// TestReportRejectionOrderAndNoStateChange: rejections are checked in the
// order host, time, clock; rejected reports change nothing.
func TestReportRejectionOrderAndNoStateChange(t *testing.T) {
	e := mustNew(t, 2, 2, 10, 10, 100, 4, 50)
	mustReport(t, e, 0, false, 10) // maxNow=10

	// Host error wins over time error.
	if _, err := e.Report(9, true, -5); !errors.Is(err, ErrHostOutOfRange) {
		t.Fatalf("got %v, want ErrHostOutOfRange", err)
	}
	// Time error wins over clock regression.
	if _, err := e.Report(0, true, -5); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("got %v, want ErrInvalidTime", err)
	}
	if _, err := e.Report(0, true, 1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("got %v, want ErrInvalidTime", err)
	}
	// Clock regression.
	if _, err := e.Report(0, true, 9); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("got %v, want ErrClockRegression", err)
	}
	// Rejections changed nothing.
	wantState(t, e, 0, 1, 0, 0, []bool{false})
	if e.maxNow != 10 {
		t.Fatalf("maxNow=%d, want 10", e.maxNow)
	}
	// now == maxNow is accepted (not a regression).
	if r := mustReport(t, e, 0, true, 10); r != Recorded {
		t.Fatalf("got %v, want Recorded", r)
	}
}

// TestQueriesCheckClockWithoutAdvancing: Ejected and Healthy reject bad
// input like Report but never advance maxNow.
func TestQueriesCheckClockWithoutAdvancing(t *testing.T) {
	e := mustNew(t, 2, 1, 10, 10, 100, 4, 50)
	mustReport(t, e, 0, false, 10) // ejected until 20, maxNow=10

	if _, err := e.Ejected(2, 10); !errors.Is(err, ErrHostOutOfRange) {
		t.Fatalf("Ejected: got %v, want ErrHostOutOfRange", err)
	}
	if _, err := e.Ejected(0, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Ejected: got %v, want ErrInvalidTime", err)
	}
	if _, err := e.Ejected(0, 9); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Ejected: got %v, want ErrClockRegression", err)
	}
	if _, err := e.Healthy(-1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Healthy: got %v, want ErrInvalidTime", err)
	}
	if _, err := e.Healthy(9); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Healthy: got %v, want ErrClockRegression", err)
	}
	// Queries at valid times do not advance maxNow.
	ej, err := e.Ejected(0, 15)
	if err != nil || !ej {
		t.Fatalf("Ejected(0,15)=%v,%v, want true,nil", ej, err)
	}
	if _, err := e.Healthy(100); err != nil {
		t.Fatalf("Healthy(100): %v", err)
	}
	if e.maxNow != 10 {
		t.Fatalf("maxNow=%d, want 10 (queries must not advance it)", e.maxNow)
	}
	// A report at t=10 is still accepted, proving maxNow stayed 10.
	if r := mustReport(t, e, 1, true, 10); r != Recorded {
		t.Fatalf("got %v, want Recorded", r)
	}
}

// TestWindowEviction: success appends to the window and evicts the oldest.
func TestWindowEviction(t *testing.T) {
	e := mustNew(t, 1, 100, 10, 10, 100, 3, 100)
	mustReport(t, e, 0, false, 1)
	mustReport(t, e, 0, true, 2)
	mustReport(t, e, 0, false, 3) // win=[F,S,F], f=2, 200<300: no trigger
	wantState(t, e, 0, 1, 0, 0, []bool{false, true, false})
	// A success evicts the oldest entry (the leading failure).
	mustReport(t, e, 0, true, 4)
	wantState(t, e, 0, 0, 0, 0, []bool{true, false, true})
	// A failure evicts the oldest too; window never exceeds Wn.
	mustReport(t, e, 0, false, 5)
	wantState(t, e, 0, 1, 0, 0, []bool{false, true, false})
}
