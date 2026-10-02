package rwlock

import "testing"

func TestSpecExample(t *testing.T) {
	co := New(100)
	s1, s2, s3, s4, s5 := co.Open(), co.Open(), co.Open(), co.Open(), co.Open()

	r1, err := co.Create(s1, "L", Write)
	mustOK(t, err)
	assertResult(t, "s1 W0", r1, Result{Seq: 0, ZXID: 1, Held: true, Watching: -1})

	r2, err := co.Create(s2, "L", Read)
	mustOK(t, err)
	assertResult(t, "s2 R1", r2, Result{Seq: 1, ZXID: 2, Watching: 0, WS: 1})

	r3, err := co.Create(s3, "L", Read)
	mustOK(t, err)
	assertResult(t, "s3 R2", r3, Result{Seq: 2, ZXID: 3, Watching: 0, WS: 2})

	r4, err := co.Create(s4, "L", Write)
	mustOK(t, err)
	// W watches the immediate predecessor of any kind: seq 2 (an R).
	assertResult(t, "s4 W3", r4, Result{Seq: 3, ZXID: 4, Watching: 2, WS: 3})

	r5, err := co.Create(s5, "L", Read)
	mustOK(t, err)
	// R watches the nearest preceding W: seq 3.
	assertResult(t, "s5 R4", r5, Result{Seq: 4, ZXID: 5, Watching: 3, WS: 4})

	// s4 cancels its waiting W3 (zxid 6). R4 re-points to W0 with ws 5.
	ev, err := co.Release(s4, "L", Write, 3)
	mustOK(t, err)
	if len(ev) != 0 {
		t.Fatalf("cancel of waiting W3 should grant nothing, got %v", ev)
	}
	r5b := co.Children("L")[3] // live: 0,1,2,4
	if r5b.Seq != 4 || r5b.Held {
		t.Fatalf("R4 still waiting, got %+v", r5b)
	}

	// s1 releases held W0 (zxid 7). ws order 1,2,5 -> seq 1,2,4 granted.
	ev, err = co.Release(s1, "L", Write, 0)
	mustOK(t, err)
	wantEv := []Grant{
		{Lock: "L", Seq: 1, Kind: Read, Session: s2, DeleteZX: 7},
		{Lock: "L", Seq: 2, Kind: Read, Session: s3, DeleteZX: 7},
		{Lock: "L", Seq: 4, Kind: Read, Session: s5, DeleteZX: 7},
	}
	assertGrants(t, ev, wantEv)

	// Sequences are never reused: next create gets seq 5, not live count.
	r6, err := co.Create(s2, "L", Write)
	mustOK(t, err)
	if r6.Seq != 5 || r6.ZXID != 8 {
		t.Fatalf("expected seq 5 zxid 8, got %+v", r6)
	}

	h := co.Holders("L")
	if len(h) != 3 || h[0].Seq != 1 || h[1].Seq != 2 || h[2].Seq != 4 {
		t.Fatalf("holders = %+v", h)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertResult(t *testing.T, name string, got, want Result) {
	t.Helper()
	got.Events = nil
	if got.Seq != want.Seq || got.ZXID != want.ZXID || got.Held != want.Held ||
		got.Watching != want.Watching || got.WS != want.WS {
		t.Fatalf("%s: got %+v want %+v", name, got, want)
	}
}

func assertGrants(t *testing.T, got, want []Grant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("grants len: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("grant[%d]: got %+v want %+v", i, got[i], want[i])
		}
	}
}
