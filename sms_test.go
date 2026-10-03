package ontology

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func mustMeter(t *testing.T, P, T0, p1, p2, MI int64) *Meter {
	t.Helper()
	m, err := NewMeter(P, T0, p1, p2, MI)
	if err != nil {
		t.Fatalf("NewMeter: %v", err)
	}
	return m
}

func rep(s string, n int) string { return strings.Repeat(s, n) }

func deposit(t *testing.T, m *Meter, a string, x int64) {
	t.Helper()
	if err := m.Deposit(a, x); err != nil {
		t.Fatalf("Deposit(%s,%d): %v", a, x, err)
	}
}

func assertCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", want)
	}
	if !AsError(err, want) {
		t.Fatalf("want error %s, got %v", want, err)
	}
}

func TestConstructionValidation(t *testing.T) {
	cases := []struct{ P, T0, p1, p2, MI int64 }{
		{0, 3, 10, 6, 200},
		{1_000_000_001, 3, 10, 6, 200},
		{100, -1, 10, 6, 200},
		{100, 1_000_001, 10, 6, 200},
		{100, 3, -1, 6, 200},
		{100, 3, 10, 1_000_001, 200},
		{100, 3, 10, 6, 99},
		{100, 3, 10, 6, 1001},
	}
	for _, c := range cases {
		if _, err := NewMeter(c.P, c.T0, c.p1, c.p2, c.MI); !AsError(err, ErrInvalidArgument) {
			t.Fatalf("case %+v: want invalid_argument, got %v", c, err)
		}
	}
}

func TestGSMExact160And161(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	r, err := m.Split(rep("a", 160))
	if err != nil || r.Encoding != EncodingGSM || len(r.Segments) != 1 {
		t.Fatalf("160 units: %+v %v", r, err)
	}
	r, err = m.Split(rep("a", 161))
	if err != nil || r.Encoding != EncodingGSM || len(r.Segments) != 2 {
		t.Fatalf("161 units: %+v %v", r, err)
	}
	if r.Segments[0] != (Segment{0, 153}) || r.Segments[1] != (Segment{153, 161}) {
		t.Fatalf("161 units segments: %+v", r.Segments)
	}
}

func TestExtendedCharPushesOver160(t *testing.T) {
	// 159 'a' + '{' = 161 GSM units.
	text := rep("a", 159) + "{"
	m := mustMeter(t, 100, 3, 10, 6, 200)
	r, err := m.Split(text)
	if err != nil || r.Encoding != EncodingGSM || len(r.Segments) != 2 {
		t.Fatalf("got %+v %v", r, err)
	}
	if r.Segments[0] != (Segment{0, 153}) || r.Segments[1] != (Segment{153, 160}) {
		t.Fatalf("segments: %+v", r.Segments)
	}
}

func TestNonGSMForcesUCS2AndXCostsOne(t *testing.T) {
	// In GSM, 69 'a' + '{' + 1 Chinese char -> UCS2; '{' costs 1 unit: 71.
	text := rep("a", 69) + "{汉"
	m := mustMeter(t, 100, 3, 10, 6, 200)
	r, err := m.Split(text)
	if err != nil || r.Encoding != EncodingUCS2 || len(r.Segments) != 2 {
		t.Fatalf("got %+v %v", r, err)
	}
	if r.Segments[0] != (Segment{0, 67}) || r.Segments[1] != (Segment{67, 71}) {
		t.Fatalf("segments: %+v", r.Segments)
	}
}

func TestExact306And307(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	if r, _ := m.Split(rep("a", 306)); r == nil || len(r.Segments) != 2 {
		t.Fatalf("306: %+v", r)
	}
	if r, _ := m.Split(rep("a", 307)); r == nil || len(r.Segments) != 3 {
		t.Fatalf("307: %+v", r)
	}
}

func TestDoubleWidthShiftAtBoundary(t *testing.T) {
	// Spec example: 152 'a' + '{' + 10 'b' = 164 units.
	text := rep("a", 152) + "{" + rep("b", 10)
	m := mustMeter(t, 100, 3, 10, 6, 200)
	r, err := m.Split(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{{0, 152}, {152, 163}}
	if len(r.Segments) != 2 || r.Segments[0] != want[0] || r.Segments[1] != want[1] {
		t.Fatalf("got %+v, want %+v", r.Segments, want)
	}
}

func TestRepeatedDoubleWidthShifts(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	text := rep("{", 60) + rep("a", 60) // 120 + 60 = 180 units
	r, err := m.Split(text)
	if err != nil || len(r.Segments) != 2 {
		t.Fatalf("got %+v %v", r, err)
	}
	// 153 fits 76 '{' (152 units); the 77th '{' shifts to segment two,
	// leaving 1 unused unit at the end of segment one.
	if r.Segments[0] != (Segment{0, 93}) || r.Segments[1] != (Segment{93, 120}) {
		t.Fatalf("repeated shift segments: %+v", r.Segments)
	}
	for _, seg := range r.Segments {
		units := 0
		for _, ch := range text[seg.Start:seg.End] {
			if ch == '{' {
				units += 2
			} else {
				units++
			}
		}
		if units > 153 {
			t.Fatalf("segment %+v uses %d units > 153", seg, units)
		}
	}
}

func TestSurrogatePairNotSplit(t *testing.T) {
	// Spec UCS2 example: 66 Chinese + U+1F600 + 5 Chinese = 73 units.
	text := rep("汉", 66) + "😀" + rep("字", 5)
	m := mustMeter(t, 100, 3, 10, 6, 200)
	r, err := m.Split(text)
	if err != nil || r.Encoding != EncodingUCS2 || len(r.Segments) != 2 {
		t.Fatalf("got %+v %v", r, err)
	}
	want := []Segment{{0, 66}, {66, 72}}
	if r.Segments[0] != want[0] || r.Segments[1] != want[1] {
		t.Fatalf("got %+v, want %+v", r.Segments, want)
	}
}

func TestSpecWalkthrough(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	deposit(t, m, "a", 1000)
	r1, err := m.Send("a", rep("a", 161), false, 5)
	if err != nil || r1.Fee != 20 || r1.Segments != 2 || r1.Encoding != EncodingGSM {
		t.Fatalf("r1: %+v %v", r1, err)
	}
	r2, err := m.Send("a", rep("a", 152)+"{"+rep("b", 10), true, 7)
	if err != nil || r2.Fee != 32 || r2.Segments != 2 {
		t.Fatalf("r2: %+v %v", r2, err)
	}
	r3, err := m.Send("a", "x", false, 120)
	if err != nil || r3.Fee != 10 {
		t.Fatalf("r3: %+v %v", r3, err)
	}
	if bal := m.accounts["a"].balance; bal != 938 {
		t.Fatalf("balance: got %d want 938", bal)
	}
}

func TestCarryoverAdjacentAndSkipped(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	deposit(t, m, "a", 1000)
	m.Send("a", rep("a", 161), false, 5) // u=2
	m.Send("a", rep("a", 164), false, 6) // 2 segments, u=4
	res, err := m.Send("a", "x", false, 120)
	if err != nil || res.Fee != 10 {
		t.Fatalf("adjacent T=4: %+v %v", res, err)
	}

	m2 := mustMeter(t, 100, 3, 10, 6, 200)
	deposit(t, m2, "a", 1000)
	m2.Send("a", rep("a", 153), false, 5)
	m2.Send("a", rep("a", 153), false, 6)
	m2.Send("a", rep("a", 153), false, 7)
	m2.Send("a", rep("a", 153), false, 8) // u=4 in k=0
	// k'=3 skips empty periods: previous used counts 0, T=3. Sending 3
	// segments prices indices 1..3 at p1 = 30.
	res2, err := m2.Send("a", rep("a", 153*3), false, 350)
	if err != nil || res2.Fee != 30 || res2.Segments != 3 {
		t.Fatalf("skipped T=3: %+v %v", res2, err)
	}

	m3 := mustMeter(t, 100, 3, 10, 6, 200)
	deposit(t, m3, "a", 1000)
	m3.Send("a", rep("a", 153), false, 1)
	m3.Send("a", rep("a", 153), false, 2)
	m3.Send("a", rep("a", 153), false, 3)                  // u=3
	res3, err := m3.Send("a", rep("a", 153*3), false, 105) // T=3+0, 3 segs at p1
	if err != nil || res3.Fee != 30 || res3.Segments != 3 {
		t.Fatalf("prev u=3: %+v %v", res3, err)
	}
}

func TestTierBoundaryTAndTPlusOne(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	deposit(t, m, "a", 1000)
	m.Send("a", rep("a", 153), false, 1)
	m.Send("a", rep("a", 153), false, 2) // u=2
	res, err := m.Send("a", rep("a", 161), false, 3)
	if err != nil || res.Fee != 16 { // index 3 -> p1, index 4 -> p2
		t.Fatalf("tier boundary: %+v %v", res, err)
	}
}

func TestInternationalRoundingOnce(t *testing.T) {
	// MI=101: per-segment ceil gives 11+7=18, whole-message ceil gives 17.
	m := mustMeter(t, 100, 3, 10, 6, 101)
	deposit(t, m, "a", 1000)
	m.Send("a", rep("a", 153), false, 1)
	m.Send("a", rep("a", 153), false, 2)
	res, err := m.Send("a", rep("a", 161), true, 3)
	if err != nil || res.Fee != 17 {
		t.Fatalf("international rounding: %+v %v", res, err)
	}
}

func TestExactly10And11Segments(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	if r, err := m.Split(rep("a", 10*153)); err != nil || len(r.Segments) != 10 {
		t.Fatalf("10 segments: %+v %v", r, err)
	}
	_, err := m.Split(rep("a", 10*153+1))
	assertCode(t, err, ErrTooManySegments)
}

func TestFeeExactlyBalance(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 100)
	deposit(t, m, "a", 20)
	res, err := m.Send("a", rep("a", 161), false, 1)
	if err != nil || res.Fee != 20 {
		t.Fatalf("exact balance: %+v %v", res, err)
	}
	if m.accounts["a"].balance != 0 {
		t.Fatalf("balance: %d", m.accounts["a"].balance)
	}
	_, err = m.Send("a", "a", false, 2)
	assertCode(t, err, ErrInsufficientBalance)
}

func TestQuoteEqualsSendAndIsReadOnly(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 150)
	deposit(t, m, "a", 1000)
	before := *m.accounts["a"]
	q, qerr := m.Quote("a", rep("a", 152)+"{"+rep("b", 10), true, 7)
	if qerr != nil {
		t.Fatal(qerr)
	}
	if *m.accounts["a"] != before {
		t.Fatalf("Quote mutated account: %+v vs %+v", *m.accounts["a"], before)
	}
	s, serr := m.Send("a", rep("a", 152)+"{"+rep("b", 10), true, 7)
	if serr != nil || *q != *s {
		t.Fatalf("Send %+v %v != Quote %+v", s, serr, q)
	}
}

func TestRejectionDoesNotMutate(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	if _, err := m.Send("ghost", "a", false, 1); !AsError(err, ErrAccountNotFound) {
		t.Fatal(err)
	}
	if _, ok := m.accounts["ghost"]; ok {
		t.Fatal("failed Send created account")
	}

	deposit(t, m, "a", 100)
	m.Send("a", rep("a", 153), false, 5)
	snap := *m.accounts["a"]
	_, err := m.Send("a", "a", false, 4)
	assertCode(t, err, ErrClockSkew)

	// Insufficient balance at a period boundary must not roll the period.
	m.accounts["a"].balance = 5
	_, err = m.Send("a", rep("a", 153), false, 150)
	assertCode(t, err, ErrInsufficientBalance)
	if m.accounts["a"].k != snap.k || m.accounts["a"].u != snap.u ||
		m.accounts["a"].t != snap.t || m.maxNow != 5 {
		t.Fatalf("rejected send mutated state: %+v vs %+v (maxNow=%d)",
			*m.accounts["a"], snap, m.maxNow)
	}
}

func TestDepositValidation(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	assertCode(t, m.Deposit("", 1), ErrInvalidArgument)
	assertCode(t, m.Deposit("a", 0), ErrInvalidArgument)
	assertCode(t, m.Deposit("a", 1_000_000_000_001), ErrInvalidArgument)
	deposit(t, m, "a", 1_000_000_000_000)
	for i := 0; i < 999; i++ {
		deposit(t, m, "a", 1_000_000_000_000)
	}
	assertCode(t, m.Deposit("a", 1), ErrInvalidArgument)
	if m.accounts["a"].balance != 1_000_000_000_000_000 {
		t.Fatal("rejected over-cap deposit changed balance")
	}
}

func TestInvalidTextAndNow(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	deposit(t, m, "a", 1000)
	for _, bad := range []string{"", "abc\xffdef", "a\x80"} {
		if _, err := m.Split(bad); !AsError(err, ErrInvalidArgument) {
			t.Fatalf("Split(%q): %v", bad, err)
		}
		if _, err := m.Send("a", bad, false, 1); !AsError(err, ErrInvalidArgument) {
			t.Fatalf("Send(%q): %v", bad, err)
		}
	}
	_, errNeg := m.Send("a", "a", false, -1)
	assertCode(t, errNeg, ErrInvalidArgument)
	_, errBig := m.Send("a", "a", false, 1_000_000_000_001)
	assertCode(t, errBig, ErrInvalidArgument)
}

func TestErrorPriority(t *testing.T) {
	m := mustMeter(t, 100, 3, 10, 6, 200)
	_, err := m.Send("", "", false, -1)
	assertCode(t, err, ErrInvalidArgument)

	m.Send("a", "x", false, 10)
	_, err = m.Send("ghost", rep("a", 1000), false, 1)
	assertCode(t, err, ErrAccountNotFound)

	deposit(t, m, "b", 1000)
	if _, err := m.Send("b", "x", false, 10); err != nil {
		t.Fatal(err)
	}
	_, err = m.Send("b", rep("a", 1000), false, 1)
	assertCode(t, err, ErrClockSkew)

	_, err = m.Send("b", rep("a", 10*153+1), false, 11)
	assertCode(t, err, ErrTooManySegments)
	// The same too-long text at an earlier now reports clock skew first.
	_, err = m.Send("b", rep("a", 10*153+1), false, 1)
	assertCode(t, err, ErrClockSkew)
}

func TestConcurrentSafetyAndInvariant(t *testing.T) {
	m := mustMeter(t, 10_000, 3, 3, 2, 200)
	const goroutines = 16
	const depositAmount = int64(100_000_000)

	// All goroutines share a monotonic clock source, so skew rejections are
	// legitimate serialization outcomes rather than races.
	var clock int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := "u" + string(rune('a'+i))
			if err := m.Deposit(a, depositAmount); err != nil {
				t.Errorf("deposit: %v", err)
				return
			}
			for j := 0; j < 50; j++ {
				now := atomic.AddInt64(&clock, 1)
				_, _ = m.Send(a, rep("a", 161), j%3 == 0, now)
			}
		}(i)
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	totalBalance, totalDeposited := int64(0), int64(goroutines)*depositAmount
	for name, acc := range m.accounts {
		totalBalance += acc.balance
		if acc.balance < 0 {
			t.Fatalf("account %s has negative balance %d", name, acc.balance)
		}
		// u equals the segments of accepted sends in the current period.
		// With P=10000 every now stays in period 0; recompute from fee?
		// State is opaque here, so check bounds instead.
		if acc.u < 0 || acc.u > 100 {
			t.Fatalf("account %s implausible u=%d", name, acc.u)
		}
	}
	if totalBalance > totalDeposited {
		t.Fatalf("balances %d exceed total deposits %d", totalBalance, totalDeposited)
	}
}
