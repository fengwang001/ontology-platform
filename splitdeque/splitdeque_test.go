package splitdeque

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, cap, sm, rv, f int64) *SplitDeque {
	t.Helper()
	d, err := New(cap, sm, rv, f)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d) error: %v", cap, sm, rv, f, err)
	}
	return d
}

func steal(t *testing.T, d *SplitDeque, m int64) []int64 {
	t.Helper()
	out, err := d.Steal(m)
	if err != nil {
		t.Fatalf("Steal(%d) error: %v", m, err)
	}
	return out
}

func eqSlice(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestExampleMain(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)
	for x := int64(1); x <= 6; x++ {
		if err := d.Push(x); err != nil {
			t.Fatalf("Push(%d): %v", x, err)
		}
	}
	if got := steal(t, d, 2); len(got) != 0 {
		t.Fatalf("Steal(2) = %v, want empty", got)
	}
	if !d.fl || d.ag != 0 || d.dm != 2 {
		t.Fatalf("request state = (%v,%d,%d), want (true,0,2)", d.fl, d.ag, d.dm)
	}
	t.Logf("input: Push1..6,Steal(2); output: []; basis: |S|=0 -> fl=true,dm=2")

	if err := d.Push(7); err != nil {
		t.Fatalf("Push(7): %v", err)
	}
	if d.s != 3 || d.t != 0 || d.b != 7 || d.fl {
		t.Fatalf("after release: t,s,b,fl = %d,%d,%d,%v, want 0,3,7,false", d.t, d.s, d.b, d.fl)
	}
	if got := steal(t, d, 5); !eqSlice(got, []int64{1, 2, 3}) {
		t.Fatalf("Steal(5) = %v, want [1 2 3]", got)
	}
	if !d.fl || d.dm != 2 {
		t.Fatalf("after short steal: fl=%v dm=%d, want true,2", d.fl, d.dm)
	}
	t.Logf("input: Push(7),Steal(5); output: [1 2 3]; basis: r=min(max(3,2),4,6)=3 then k=3<5 dm=2")

	if x, ok := d.Pop(); !ok || x != 7 {
		t.Fatalf("Pop = (%d,%v), want 7,true", x, ok)
	}
	if d.s != 5 || d.fl {
		t.Fatalf("after Pop release: s=%d fl=%v, want 5,false", d.s, d.fl)
	}
	if x, ok := d.Pop(); !ok || x != 6 {
		t.Fatalf("Pop = (%d,%v), want 6,true", x, ok)
	}
	if x, ok := d.Pop(); !ok || x != 5 {
		t.Fatalf("Pop = (%d,%v), want 5,true", x, ok)
	}
	if d.stats.Reclaims != 1 {
		t.Fatalf("Reclaims = %d, want 1", d.stats.Reclaims)
	}
	if x, ok := d.Pop(); !ok || x != 4 {
		t.Fatalf("Pop = (%d,%v), want 4,true", x, ok)
	}
	if _, ok := d.Pop(); ok {
		t.Fatalf("Pop = _,true, want empty")
	}
	st := d.Stats()
	if st.Pushed != st.Popped+st.Stolen+(d.b-d.t) || st.Pushed != 7 || st.Popped != 4 || st.Stolen != 3 || d.b-d.t != 0 {
		t.Fatalf("accounting wrong: %+v b-t=%d", st, d.b-d.t)
	}
	rel, rec, copies := d.MoveCounts()
	if rel != 0 || rec != 0 || copies != 3 {
		t.Fatalf("MoveCounts = (%d,%d,%d), want (0,0,3)", rel, rec, copies)
	}
	t.Logf("input: Pop x5; output: 7,6,5,4,empty; basis: release r=2 then two reclaims ceil(2/2)=1; moves=(0,0,3)")
}

func TestExampleAge(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)
	if err := d.Push(1); err != nil {
		t.Fatal(err)
	}
	if got := steal(t, d, 1); len(got) != 0 {
		t.Fatalf("Steal = %v, want empty", got)
	}
	if x, ok := d.Pop(); !ok || x != 1 || d.ag != 1 || !d.fl {
		t.Fatalf("Pop1: x=%d ok=%v fl=%v ag=%d", x, ok, d.fl, d.ag)
	}
	if _, ok := d.Pop(); ok || d.ag != 2 || !d.fl {
		t.Fatalf("Pop2: ok=%v fl=%v ag=%d", ok, d.fl, d.ag)
	}
	if _, ok := d.Pop(); ok || d.ag != 0 || d.fl || d.dm != 0 {
		t.Fatalf("Pop3: ok=%v fl=%v ag=%d dm=%d", ok, d.fl, d.ag, d.dm)
	}
	if err := d.Push(2); err != nil {
		t.Fatal(err)
	}
	if err := d.Push(3); err != nil {
		t.Fatal(err)
	}
	if d.s != 0 || d.t != 0 || d.b != 2 {
		t.Fatalf("no release after expiry: t,s,b = %d,%d,%d, want 0,0,2", d.t, d.s, d.b)
	}
	t.Logf("input: age sequence; output: 1,empty,empty; basis: r=0 ages 1,2,3=F then cleared; later pushes do not release")
}

func TestExampleDeficit(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)
	for x := int64(1); x <= 6; x++ {
		if err := d.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	if got := steal(t, d, 4); len(got) != 0 {
		t.Fatalf("Steal(4) = %v, want empty", got)
	}
	if d.dm != 4 {
		t.Fatalf("dm = %d, want 4", d.dm)
	}
	if err := d.Push(7); err != nil {
		t.Fatal(err)
	}
	if d.s != 4 {
		t.Fatalf("s = %d, want 4 (dm raised r above floor(|P|/2)=3)", d.s)
	}
	if got := steal(t, d, 4); !eqSlice(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("Steal(4) = %v, want [1 2 3 4]", got)
	}
	if d.fl {
		t.Fatalf("fl = true, want false after exact fill")
	}
	t.Logf("input: Push1..6,Steal(4),Push(7); output: shared [1..4]; basis: r=min(max(3,4),4,6)=4")
}

func TestDeficitMergeAndCaps(t *testing.T) {
	d2 := mustNew(t, 8, 2, 0, 3)
	for x := int64(1); x <= 5; x++ {
		if err := d2.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, d2, 8)
	if err := d2.Push(6); err != nil {
		t.Fatal(err)
	}
	if d2.s != 2 {
		t.Fatalf("Sm cap: s = %d, want 2", d2.s)
	}
	t.Logf("input: Cap8 Sm2 Rv0 Push1..6 with dm=8; basis: r=min(8,2,6)=2 limited by Sm-|S|")

	d3 := mustNew(t, 8, 8, 4, 3)
	for x := int64(1); x <= 5; x++ {
		if err := d3.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, d3, 8)
	if err := d3.Push(6); err != nil {
		t.Fatal(err)
	}
	if d3.s != 2 {
		t.Fatalf("Rv cap: s = %d, want 2", d3.s)
	}
	t.Logf("input: Cap8 Sm8 Rv4 Push1..6 dm=8; basis: r=min(8,8,2)=2 limited by |P|-Rv")

	d4 := mustNew(t, 8, 8, 0, 10)
	for x := int64(1); x <= 3; x++ {
		if err := d4.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, d4, 4)
	steal(t, d4, 5)
	if d4.dm != 5 || d4.ag != 0 {
		t.Fatalf("merge: dm=%d ag=%d, want 5,0 (ag reset on every short steal)", d4.dm, d4.ag)
	}
	steal(t, d4, 2)
	if d4.dm != 5 {
		t.Fatalf("merge: dm=%d, want 5", d4.dm)
	}
	t.Logf("input: short steals 4,5,2 against |S|=0; basis: dm=max chain stays 5 and ag resets to 0")
}

func TestReservationBlocksRelease(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)
	if err := d.Push(1); err != nil {
		t.Fatal(err)
	}
	steal(t, d, 1)
	if x, ok := d.Pop(); !ok || x != 1 {
		t.Fatalf("Pop = (%d,%v), want 1,true", x, ok)
	}
	if d.ag != 1 {
		t.Fatalf("ag = %d, want 1", d.ag)
	}
	t.Logf("input: Push1,Steal(1),Pop with Rv=1; basis: |P|-Rv=0 so r=0, ages instead of releasing")
}

func TestRejectedCallsDoNothing(t *testing.T) {
	d := mustNew(t, 8, 8, 0, 2)
	for x := int64(1); x <= 8; x++ {
		if err := d.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, d, 4) // |S|=0: fl=true, dm=4
	if err := d.Push(9); !errors.Is(err, ErrFull) {
		t.Fatalf("Push full = %v, want ErrFull", err)
	}
	if d.b != 8 || d.t != 0 || d.s != 0 || !d.fl || d.ag != 0 || d.dm != 4 || d.stats.Pushed != 8 {
		t.Fatalf("rejected Push changed state: t,s,b=%d,%d,%d fl=%v ag=%d dm=%d stats=%+v",
			d.t, d.s, d.b, d.fl, d.ag, d.dm, d.stats)
	}
	t.Logf("input: fill Cap2, short Steal, full Push; basis: ErrFull before append so no release check")

	if _, err := d.Steal(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Steal(0) = %v, want ErrInvalidArgument", err)
	}
	if _, err := d.Steal(9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Steal(9) = %v, want ErrInvalidArgument", err)
	}
	if d.stats.Misses != 1 || d.t != 0 {
		t.Fatalf("rejected Steal changed state: t=%d misses=%d", d.t, d.stats.Misses)
	}
	t.Logf("input: Steal(0),Steal(Cap+1); output: ErrInvalidArgument; basis: parameter check leaves state untouched")
}

func TestInvalidConfig(t *testing.T) {
	cases := []struct{ cap, sm, rv, f int64 }{
		{0, 1, 0, 1},
		{1_000_001, 1, 0, 1},
		{8, 0, 0, 1},
		{8, 9, 0, 1},
		{8, 1, -1, 1},
		{8, 1, 9, 1},
		{8, 1, 0, 0},
		{8, 1, 0, 1_000_001},
	}
	for _, c := range cases {
		if _, err := New(c.cap, c.sm, c.rv, c.f); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%d,%d,%d,%d) = %v, want ErrInvalidConfig", c.cap, c.sm, c.rv, c.f, err)
		}
	}
	t.Logf("input: 8 out-of-range configs; output: ErrInvalidConfig for all; basis: Cap/Sm/Rv/F range checks")
}
