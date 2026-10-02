package bbr

import (
	"errors"
	"testing"
)

func mustAck(t *testing.T, b *BBR, now, n, rtt, ds, inflight uint64, appLimited bool) {
	t.Helper()
	if err := b.OnAck(now, n, rtt, ds, inflight, appLimited); err != nil {
		t.Fatalf("OnAck(now=%d n=%d rtt=%d ds=%d inflight=%d appLimited=%v): %v",
			now, n, rtt, ds, inflight, appLimited, err)
	}
}

func newBBR(t *testing.T, mss uint64) *BBR {
	t.Helper()
	b, err := New(mss)
	if err != nil {
		t.Fatalf("New(%d): %v", mss, err)
	}
	return b
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	b := newBBR(t, 1000)

	if got := b.Cwnd(); got != 10*1000 {
		t.Fatalf("initial Cwnd = %d, want 10000", got)
	}
	if got := b.Pacing(); got != 0 {
		t.Fatalf("initial Pacing = %d, want 0", got)
	}

	mustAck(t, b, 0, 1000, 100, 0, 5000, false)
	if b.Round() != 1 {
		t.Fatalf("R = %d, want 1", b.Round())
	}
	if b.MaxBw() != 10000 {
		t.Fatalf("maxBw = %d, want 10000", b.MaxBw())
	}
	if rtt, ok := b.MinRtt(); !ok || rtt != 100 {
		t.Fatalf("minRtt = %d,%v, want 100,true", rtt, ok)
	}
	if b.fullBw != 10000 {
		t.Fatalf("fullBw = %d, want 10000", b.fullBw)
	}
	if got := b.Pacing(); got != 28900 {
		t.Fatalf("Pacing = %d, want 28900", got)
	}
	if got := b.Cwnd(); got != 4000 {
		t.Fatalf("Cwnd = %d, want 4000 (BDP=1000, 2890 < 4*mss)", got)
	}

	mustAck(t, b, 50, 1000, 100, 0, 6000, false)
	if b.MaxBw() != 20000 {
		t.Fatalf("maxBw = %d, want 20000 (still round 1)", b.MaxBw())
	}
	if b.Round() != 1 {
		t.Fatalf("R = %d, want 1", b.Round())
	}

	mustAck(t, b, 100, 1000, 100, 1000, 6000, false)
	if b.Round() != 2 {
		t.Fatalf("R = %d, want 2 (ds=1000 >= N=1000)", b.Round())
	}
	if b.fullBw != 20000 {
		t.Fatalf("fullBw = %d, want 20000 (2000000 >= 1250000)", b.fullBw)
	}
}

// maxBw*100 >= fullBw*125 counts as growth exactly at equality.
func TestFullPipeGrowthBoundary(t *testing.T) {
	setup := func() *BBR {
		b := newBBR(t, 1000)
		mustAck(t, b, 0, 1000, 100, 0, 5000, false)
		mustAck(t, b, 50, 1000, 100, 0, 6000, false)
		mustAck(t, b, 100, 1000, 100, 1000, 6000, false) // fullBw = 20000
		return b
	}

	// Equality: 25000*100 == 20000*125 == 2500000 -> growth.
	b := setup()
	mustAck(t, b, 150, 2500, 100, 3000, 6000, false) // s = 25000
	if b.fullBw != 25000 {
		t.Fatalf("equality case: fullBw = %d, want 25000", b.fullBw)
	}
	if b.fullCnt != 0 {
		t.Fatalf("equality case: fullCnt = %d, want 0", b.fullCnt)
	}

	// Just below: 24999*100 = 2499900 < 2500000 -> no growth.
	b = setup()
	mustAck(t, b, 150, 2499, 100, 3000, 6000, false) // s = 24990
	if b.fullBw != 20000 {
		t.Fatalf("below-boundary case: fullBw = %d, want 20000 (unchanged)", b.fullBw)
	}
	if b.fullCnt != 1 {
		t.Fatalf("below-boundary case: fullCnt = %d, want 1", b.fullCnt)
	}

	// Three consecutive non-growth rounds fill the pipe -> Drain.
	mustAck(t, b, 200, 1000, 100, 5499, 6000, false) // s = 10000, fullCnt = 2
	mustAck(t, b, 250, 1000, 100, 6499, 6000, false) // s = 10000, fullCnt = 3
	if !b.filled {
		t.Fatalf("filled = false, want true after 3 non-growth rounds")
	}
	if b.State() != Drain {
		t.Fatalf("state = %v, want Drain (inflight 6000 > BDP 2400)", b.State())
	}
}

// An app-limited sample below the window max is dropped; an equal one is
// accepted (and re-tagged with the current round).
func TestAppLimitedFilter(t *testing.T) {
	b := newBBR(t, 1000)
	mustAck(t, b, 0, 1000, 100, 0, 5000, false)  // maxBw = 10000
	mustAck(t, b, 50, 1000, 100, 0, 6000, false) // maxBw = 20000, round 1

	opsBefore := b.filterOps
	mustAck(t, b, 100, 1000, 400, 0, 6000, true) // s = 7500 < 20000, app-limited
	if b.MaxBw() != 20000 {
		t.Fatalf("maxBw = %d, want 20000 (small app-limited sample dropped)", b.MaxBw())
	}
	if b.filterOps != opsBefore {
		t.Fatalf("filterOps changed by %d, want 0 (dropped sample must not touch the deque)",
			b.filterOps-opsBefore)
	}
	if len(b.filter) != 1 {
		t.Fatalf("filter len = %d, want 1", len(b.filter))
	}

	opsBefore = b.filterOps
	mustAck(t, b, 150, 1000, 200, 0, 6000, true) // s = 20000 == max, app-limited
	if b.MaxBw() != 20000 {
		t.Fatalf("maxBw = %d, want 20000", b.MaxBw())
	}
	if b.filterOps != opsBefore+2 {
		t.Fatalf("filterOps changed by %d, want 2 (pop + push for equal sample)",
			b.filterOps-opsBefore)
	}
	if len(b.filter) != 1 || b.filter[0].round != 1 {
		t.Fatalf("filter = %+v, want one sample re-tagged at round 1", b.filter)
	}
}

// A sample recorded at round r is valid while R-r < 10 and expires exactly
// at R-r == 10.
func TestFilterWindowExpiry(t *testing.T) {
	b := newBBR(t, 1000)
	mustAck(t, b, 0, 1000, 100, 0, 5000, false)    // R=1, s=10000
	mustAck(t, b, 1, 1000, 200, 1000, 5000, false) // R=2, s=5000
	mustAck(t, b, 2, 1000, 400, 2000, 5000, false) // R=3, s=2500
	if len(b.filter) != 3 {
		t.Fatalf("filter len = %d, want 3 (decreasing samples kept)", len(b.filter))
	}

	// Advance rounds with tiny app-limited samples that are never recorded.
	d := uint64(3000)
	ackRound := func(k uint64) {
		mustAck(t, b, k-1, 1, 100, d, 5000, true) // ds = D opens a new round
		d++
		if b.Round() != k {
			t.Fatalf("R = %d, want %d", b.Round(), k)
		}
	}
	for k := uint64(4); k <= 10; k++ {
		ackRound(k)
	}
	if got := b.MaxBw(); got != 10000 {
		t.Fatalf("R=10: maxBw = %d, want 10000 (R-r=9 still valid)", got)
	}
	ackRound(11)
	if got := b.MaxBw(); got != 5000 {
		t.Fatalf("R=11: maxBw = %d, want 5000 (round-1 sample expired at R-r=10)", got)
	}
	ackRound(12)
	if got := b.MaxBw(); got != 2500 {
		t.Fatalf("R=12: maxBw = %d, want 2500 (round-2 sample expired)", got)
	}
}

// Equal RTT refreshes the stamp; exactly 10000ms later the sample expires
// and is replaced even by a larger RTT.
func TestMinRttRefreshAndExpiry(t *testing.T) {
	b := newBBR(t, 1000)
	mustAck(t, b, 0, 1000, 100, 0, 5000, false)
	mustAck(t, b, 5000, 1000, 100, 0, 5000, false) // equal rtt refreshes stamp
	if b.stamp != 5000 {
		t.Fatalf("stamp = %d, want 5000 (equal rtt refreshes)", b.stamp)
	}

	mustAck(t, b, 14999, 1000, 150, 0, 5000, false) // 9999 < 10000: not expired
	if rtt, _ := b.MinRtt(); rtt != 100 {
		t.Fatalf("minRtt = %d, want 100 (larger rtt, not expired)", rtt)
	}
	if b.stamp != 5000 {
		t.Fatalf("stamp = %d, want 5000", b.stamp)
	}

	mustAck(t, b, 15000, 1000, 150, 0, 5000, false) // 10000 >= 10000: expired
	if rtt, _ := b.MinRtt(); rtt != 150 {
		t.Fatalf("minRtt = %d, want 150 (expired, replaced by larger rtt)", rtt)
	}
	if b.stamp != 15000 {
		t.Fatalf("stamp = %d, want 15000", b.stamp)
	}
	if b.State() != ProbeRTT {
		t.Fatalf("state = %v, want ProbeRTT (expired)", b.State())
	}
	if b.pd != 0 {
		t.Fatalf("pd = %d, want 0 (inflight 5000 > 4*mss)", b.pd)
	}
	if got := b.Cwnd(); got != 4000 {
		t.Fatalf("ProbeRTT Cwnd = %d, want 4000 (4*mss)", got)
	}

	// rtt=200 > minRtt keeps the stamp untouched so the exit path sets it.
	mustAck(t, b, 15050, 1000, 200, 0, 4000, false) // inflight <= 4*mss
	if b.pd != 15250 {
		t.Fatalf("pd = %d, want 15250 (now+200)", b.pd)
	}
	mustAck(t, b, 15249, 1000, 200, 0, 4000, false)
	if b.State() != ProbeRTT {
		t.Fatalf("state = %v, want ProbeRTT (now < pd)", b.State())
	}
	mustAck(t, b, 15250, 1000, 200, 0, 4000, false) // exactly at pd -> exit
	if b.State() != Startup {
		t.Fatalf("state = %v, want Startup (filled=false)", b.State())
	}
	if b.stamp != 15250 {
		t.Fatalf("stamp = %d, want 15250 (exit refreshes stamp)", b.stamp)
	}
	if b.pd != 0 {
		t.Fatalf("pd = %d, want 0 after exit", b.pd)
	}
}

// setupProbeBW drives a controller from Startup through Drain into ProbeBW
// within a single ack: the 4th ack makes fullCnt reach 3 (filled -> Drain)
// and inflight <= BDP immediately triggers Drain -> ProbeBW.
// Returns a controller in ProbeBW with ci=2, cs=150, minRtt=100,
// maxBw=10000, BDP=1000, D=4000.
func setupProbeBW(t *testing.T) *BBR {
	t.Helper()
	b := newBBR(t, 1000)
	mustAck(t, b, 0, 1000, 100, 0, 5000, false)     // R=1, fullBw=10000
	mustAck(t, b, 50, 1000, 100, 1000, 5000, false) // R=2, fullCnt=1
	mustAck(t, b, 100, 1000, 100, 2000, 5000, false)
	if b.fullCnt != 2 {
		t.Fatalf("fullCnt = %d, want 2", b.fullCnt)
	}
	mustAck(t, b, 150, 1000, 100, 3000, 1000, false) // fullCnt=3, inflight=BDP=1000
	if !b.filled {
		t.Fatalf("filled = false, want true")
	}
	if b.State() != ProbeBW {
		t.Fatalf("state = %v, want ProbeBW (Startup->Drain->ProbeBW in one ack)", b.State())
	}
	if b.ci != 2 || b.cs != 150 {
		t.Fatalf("ci=%d cs=%d, want ci=2 cs=150", b.ci, b.cs)
	}
	if got := b.Pacing(); got != 10000 {
		t.Fatalf("Pacing = %d, want 10000 (ProbeBW g=100)", got)
	}
	if got := b.Cwnd(); got != 4000 {
		t.Fatalf("Cwnd = %d, want 4000 (BDP*200/100=2000 < 4*mss)", got)
	}
	return b
}

func TestStartupDrainProbeBWSameAck(t *testing.T) {
	setupProbeBW(t)
}

// ProbeBW gain-cycle advance rules for g in {100, 125, 75}, and at most one
// step per ack.
func TestProbeBWGainCycle(t *testing.T) {
	b := setupProbeBW(t)
	ack := func(now, inflight uint64) {
		// ds=0 opens no new round (R stays 4, no filter expiry); rtt=1000
		// gives s = D ~= 4001 < 10000 so maxBw and BDP stay put, and
		// rtt > minRtt keeps minRtt = 100 for the el comparisons.
		mustAck(t, b, now, 1, 1000, 0, inflight, false)
	}

	// g=100 (ci=2): needs el >= minRtt=100.
	ack(200, 5000) // el=50 < 100
	if b.ci != 2 {
		t.Fatalf("ci = %d, want 2 (el < minRtt)", b.ci)
	}
	ack(1260, 5000) // el=1110 >= 100: exactly one step despite huge el
	if b.ci != 3 || b.cs != 1260 {
		t.Fatalf("ci=%d cs=%d, want ci=3 cs=1260 (at most one step per ack)", b.ci, b.cs)
	}

	// g=100 for ci=3..7: advance one per ack with el>=100.
	for _, now := range []uint64{1360, 1460, 1560, 1660, 1760} {
		ack(now, 5000)
	}
	if b.ci != 0 || b.cs != 1760 {
		t.Fatalf("ci=%d cs=%d, want ci=0 cs=1760", b.ci, b.cs)
	}

	// g=125 (ci=0): needs el>=minRtt AND inflight >= floor(BDP*125/100)=1250.
	ack(1860, 1249) // el ok, inflight 1249 < 1250
	if b.ci != 0 {
		t.Fatalf("ci = %d, want 0 (inflight below 125%% BDP)", b.ci)
	}
	ack(1960, 1250) // inflight exactly 1250
	if b.ci != 1 || b.cs != 1960 {
		t.Fatalf("ci=%d cs=%d, want ci=1 cs=1960", b.ci, b.cs)
	}

	// g=75 (ci=1): needs el>=minRtt OR inflight <= BDP=1000.
	ack(2000, 1001) // el=40 < 100, inflight 1001 > 1000
	if b.ci != 1 {
		t.Fatalf("ci = %d, want 1 (neither condition met)", b.ci)
	}
	ack(2040, 1000) // el=40 < 100, inflight 1000 <= 1000
	if b.ci != 2 || b.cs != 2040 {
		t.Fatalf("ci=%d cs=%d, want ci=2 cs=2040 (inflight <= BDP)", b.ci, b.cs)
	}

	// g=75 advancing on el alone (inflight > BDP): craft state directly.
	b2 := &BBR{mss: 1000, state: ProbeBW, ci: 1, cs: 0,
		minRtt: 100, hasMinRtt: true, hasAck: true, filled: true,
		filter: []sample{{round: 0, bw: 10000}}}
	if err := b2.OnAck(100, 1000, 100, 0, 5000, false); err != nil {
		t.Fatalf("OnAck: %v", err)
	}
	if b2.ci != 2 {
		t.Fatalf("ci = %d, want 2 (el >= minRtt alone advances g=75)", b2.ci)
	}
}

// ProbeRTT: the entering ack can already fix the exit deadline, and the
// controller exits exactly at pd, back to ProbeBW when filled.
func TestProbeRTTFromProbeBW(t *testing.T) {
	b := setupProbeBW(t) // filled, ProbeBW, stamp=150 (rtt==minRtt refreshes)

	// rtt=200 > minRtt: no refresh; now-stamp = 10000 -> expired.
	mustAck(t, b, 10150, 1000, 200, 4000, 4000, false)
	if b.State() != ProbeRTT {
		t.Fatalf("state = %v, want ProbeRTT", b.State())
	}
	if b.pd != 10350 {
		t.Fatalf("pd = %d, want 10350 (set on the entering ack itself)", b.pd)
	}
	if got := b.Cwnd(); got != 4000 {
		t.Fatalf("ProbeRTT Cwnd = %d, want 4000", got)
	}
	if got := b.Pacing(); got != 10000 {
		t.Fatalf("ProbeRTT Pacing = %d, want 10000 (pg=100)", got)
	}

	mustAck(t, b, 10349, 1000, 200, 5000, 4000, false)
	if b.State() != ProbeRTT || b.pd != 10350 {
		t.Fatalf("state=%v pd=%d, want ProbeRTT pd=10350 (no re-entry reset)", b.State(), b.pd)
	}

	mustAck(t, b, 10350, 1000, 200, 6000, 4000, false) // exactly at pd
	if b.State() != ProbeBW {
		t.Fatalf("state = %v, want ProbeBW (filled)", b.State())
	}
	if b.ci != 2 || b.cs != 10350 {
		t.Fatalf("ci=%d cs=%d, want ci=2 cs=10350", b.ci, b.cs)
	}
	if b.stamp != 10350 {
		t.Fatalf("stamp = %d, want 10350", b.stamp)
	}
	if b.pd != 0 {
		t.Fatalf("pd = %d, want 0", b.pd)
	}
}

// snapshot captures every internal field for change detection.
type snapshot struct {
	delivered, round, nextRound uint64
	filter                      []sample
	filterOps                   uint64
	minRtt, stamp               uint64
	hasMinRtt                   bool
	state                       State
	filled                      bool
	fullBw, fullCnt             uint64
	ci                          int
	cs, pd, lastNow             uint64
	hasAck                      bool
}

func takeSnapshot(b *BBR) snapshot {
	f := make([]sample, len(b.filter))
	copy(f, b.filter)
	return snapshot{
		delivered: b.delivered, round: b.round, nextRound: b.nextRound,
		filter: f, filterOps: b.filterOps,
		minRtt: b.minRtt, stamp: b.stamp, hasMinRtt: b.hasMinRtt,
		state: b.state, filled: b.filled, fullBw: b.fullBw, fullCnt: b.fullCnt,
		ci: b.ci, cs: b.cs, pd: b.pd, lastNow: b.lastNow, hasAck: b.hasAck,
	}
}

func snapshotsEqual(a, b snapshot) bool {
	if a.delivered != b.delivered || a.round != b.round || a.nextRound != b.nextRound ||
		a.filterOps != b.filterOps || a.minRtt != b.minRtt || a.stamp != b.stamp ||
		a.hasMinRtt != b.hasMinRtt || a.state != b.state || a.filled != b.filled ||
		a.fullBw != b.fullBw || a.fullCnt != b.fullCnt || a.ci != b.ci ||
		a.cs != b.cs || a.pd != b.pd || a.lastNow != b.lastNow || a.hasAck != b.hasAck {
		return false
	}
	if len(a.filter) != len(b.filter) {
		return false
	}
	for i := range a.filter {
		if a.filter[i] != b.filter[i] {
			return false
		}
	}
	return true
}

// Rejection reasons are distinguishable, checked in the order param ->
// clock -> sample, and a rejected ack changes nothing.
func TestValidation(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New(0) = %v, want ErrInvalidParam", err)
	}
	if _, err := New(65536); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New(65536) = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1) = %v", err)
	}
	if _, err := New(65535); err != nil {
		t.Fatalf("New(65535) = %v", err)
	}

	b := newBBR(t, 1000)
	mustAck(t, b, 100, 1000, 100, 0, 5000, false)
	before := takeSnapshot(b)

	cases := []struct {
		name                      string
		now, n, rtt, ds, inflight uint64
		want                      error
	}{
		{"now too large", 1e12 + 1, 1000, 100, 0, 5000, ErrInvalidParam},
		{"n zero", 100, 0, 100, 0, 5000, ErrInvalidParam},
		{"n too large", 100, 1e6 + 1, 100, 0, 5000, ErrInvalidParam},
		{"rtt zero", 100, 1000, 0, 0, 5000, ErrInvalidParam},
		{"rtt too large", 100, 1000, 1e5 + 1, 0, 5000, ErrInvalidParam},
		{"inflight too large", 100, 1000, 100, 0, 1e12 + 1, ErrInvalidParam},
		// Param beats clock: n=0 is invalid and now=99 rewinds.
		{"param before clock", 99, 0, 100, 0, 5000, ErrInvalidParam},
		{"clock rewind", 99, 1000, 100, 0, 5000, ErrClockRewind},
		// Clock beats sample: ds=2000 > D=1000 and now=99 rewinds.
		{"clock before sample", 99, 1000, 100, 2000, 5000, ErrClockRewind},
		{"sample ds > D", 100, 1000, 100, 2000, 5000, ErrInvalidSample},
	}
	for _, c := range cases {
		err := b.OnAck(c.now, c.n, c.rtt, c.ds, c.inflight, false)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if after := takeSnapshot(b); !snapshotsEqual(before, after) {
			t.Fatalf("%s: rejected ack mutated state: %+v -> %+v", c.name, before, after)
		}
	}

	// Boundary values are accepted; equal timestamps are allowed.
	mustAck(t, b, 100, 1, 1, 0, 0, false)
	mustAck(t, b, 1e12, 1e6, 1e5, 0, 1e12, true)
}
