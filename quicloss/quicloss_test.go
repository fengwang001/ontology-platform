package quicloss

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, mad int64) *Controller {
	t.Helper()
	c, err := New(mad)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func equalInt64(a, b []int64) bool {
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

// TestSpecLossExample reproduces the spec's packet-number + time-threshold
// example, including the loss-delay equality at now == 421.
func TestSpecLossExample(t *testing.T) {
	c := mustNew(t, 25)
	sent := []int64{0, 10, 20, 399, 400}
	for i, at := range sent {
		must(t, c.Send(at, SpaceA, int64(i+1), 100, true))
	}

	lost, err := c.Ack(420, SpaceA, []int64{5}, 0)
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if want := []int64{1, 2, 3}; !equalInt64(lost, want) {
		t.Fatalf("lost = %v, want %v", lost, want)
	}
	rtt := c.RTT()
	if rtt.Latest != 20 || rtt.SRTT != 20 || rtt.MinRTT != 20 || rtt.RTTVar != 10 {
		t.Fatalf("rtt snapshot = %+v, want latest/srtt/min=20 rttvar=10", rtt)
	}

	info, ok := c.Timer()
	if !ok || info != (TimerInfo{Time: 421, Space: SpaceA, Mode: ModeLoss}) {
		t.Fatalf("timer = %+v ok=%v, want (421, A, Loss)", info, ok)
	}

	res, err := c.OnTimeout(421)
	if err != nil {
		t.Fatalf("OnTimeout: %v", err)
	}
	if res.Probe != nil || !equalInt64(res.Loss, []int64{4}) {
		t.Fatalf("timeout result = %+v, want loss [4]", res)
	}
}

// TestSpecRTTExample covers the exact SRTT/RTTVAR arithmetic sequence.
func TestSpecRTTExample(t *testing.T) {
	c := mustNew(t, 25)

	// First sample latest=100: srtt=100, rttvar=floor(100/2)=50.
	must(t, c.Send(0, SpaceA, 1, 10, true))
	if _, err := c.Ack(100, SpaceA, []int64{1}, 0); err != nil {
		t.Fatal(err)
	}
	if s := c.RTT(); s.SRTT != 100 || s.RTTVar != 50 {
		t.Fatalf("after sample 1: %+v, want srtt=100 rttvar=50", s)
	}

	must(t, c.HandshakeConfirmed(100))

	// Second sample latest=140, ackDelay=10: adj=130,
	// rttvar=floor((150+30)/4)=45, srtt=floor((700+130)/8)=103.
	must(t, c.Send(100, SpaceA, 2, 10, true))
	if _, err := c.Ack(240, SpaceA, []int64{2}, 10); err != nil {
		t.Fatal(err)
	}
	if s := c.RTT(); s.Latest != 140 || s.RTTVar != 45 || s.SRTT != 103 {
		t.Fatalf("after sample 2: %+v, want latest=140 rttvar=45 srtt=103", s)
	}

	// Third sample latest=105: 105 < minRtt(100)+10, so adj=105;
	// rttvar=floor((135+2)/4)=34, srtt=floor((721+105)/8)=103.
	must(t, c.Send(240, SpaceA, 3, 10, true))
	if _, err := c.Ack(345, SpaceA, []int64{3}, 10); err != nil {
		t.Fatal(err)
	}
	if s := c.RTT(); s.Latest != 105 || s.RTTVar != 34 || s.SRTT != 103 {
		t.Fatalf("after sample 3: %+v, want latest=105 rttvar=34 srtt=103", s)
	}
}

// TestSpecPTOExample verifies base PTO, exponential backoff and ptoCount.
func TestSpecPTOExample(t *testing.T) {
	c := mustNew(t, 25)
	must(t, c.Send(0, SpaceA, 1, 10, true))
	if _, err := c.Ack(100, SpaceA, []int64{1}, 0); err != nil {
		t.Fatal(err)
	}
	must(t, c.HandshakeConfirmed(100))
	must(t, c.Send(100, SpaceA, 2, 10, true))
	if _, err := c.Ack(240, SpaceA, []int64{2}, 10); err != nil {
		t.Fatal(err)
	}
	must(t, c.Send(240, SpaceA, 3, 10, true))
	if _, err := c.Ack(345, SpaceA, []int64{3}, 10); err != nil {
		t.Fatal(err)
	}

	must(t, c.Send(1000, SpaceA, 4, 10, true))
	info, ok := c.Timer()
	want := TimerInfo{Time: 1264, Space: SpaceA, Mode: ModePTO} // 1000 + (103+136+25)
	if !ok || info != want {
		t.Fatalf("timer = %+v ok=%v, want %+v", info, ok, want)
	}

	res, err := c.OnTimeout(1264)
	if err != nil || res.Probe == nil || res.Probe.Space != SpaceA {
		t.Fatalf("probe = %+v err=%v", res, err)
	}
	if s := c.RTT(); s.PTOCount != 1 {
		t.Fatalf("ptoCount = %d, want 1", s.PTOCount)
	}

	// Another ack-eliciting packet at the firing time: next PTO doubles to
	// 1264 + 264*2 = 1792.
	must(t, c.Send(1264, SpaceA, 5, 10, true))
	info, ok = c.Timer()
	want = TimerInfo{Time: 1792, Space: SpaceA, Mode: ModePTO}
	if !ok || info != want {
		t.Fatalf("timer after backoff = %+v ok=%v, want %+v", info, ok, want)
	}
}

// TestLossBeatsPTO requires a pending loss time to always win over PTO.
func TestLossBeatsPTO(t *testing.T) {
	c := mustNew(t, 25)
	must(t, c.HandshakeConfirmed(0))
	must(t, c.Send(0, SpaceA, 1, 10, true))
	must(t, c.Send(1, SpaceA, 2, 10, true))
	must(t, c.Send(2, SpaceA, 3, 10, true))
	must(t, c.Send(10, SpaceA, 4, 10, true))
	lost, err := c.Ack(400, SpaceA, []int64{4}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Only pn1 meets la-pn >= 3 here; the 390ms sample makes the time
	// threshold 438ms, so pn2/pn3 survive and populate lt.
	if !equalInt64(lost, []int64{1}) {
		t.Fatalf("lost = %v, want [1]", lost)
	}
	info, ok := c.Timer()
	if !ok || info.Mode != ModeLoss {
		t.Fatalf("timer = %+v ok=%v, want Loss", info, ok)
	}
	// H tie handling: arm an equally early H loss time; H must win.
	// Fabricate an equally early H loss time directly for the tie check.
	c.mu.Lock()
	c.h.hasLT = true
	c.h.lossTime = info.Time
	c.a.lossTime = info.Time
	c.mu.Unlock()
	tied, ok := c.Timer()
	if !ok || tied.Space != SpaceH || tied.Mode != ModeLoss {
		t.Fatalf("tie timer = %+v ok=%v, want H Loss", tied, ok)
	}
}

// TestApplicationExcludedBeforeConfirmation checks A has no PTO before
// confirmation and that ack delay is ignored in that phase.
func TestApplicationExcludedBeforeConfirmation(t *testing.T) {
	c := mustNew(t, 25)
	must(t, c.Send(0, SpaceA, 1, 10, true))
	if _, ok := c.Timer(); ok {
		t.Fatal("application space must not arm PTO before confirmation")
	}
	if _, err := c.Ack(100, SpaceA, []int64{1}, 9999); err != nil {
		t.Fatal(err)
	}
	if s := c.RTT(); s.SRTT != 100 {
		t.Fatalf("ack delay should be ignored pre-confirmation, srtt=%d", s.SRTT)
	}
}

// TestDuplicateAckKeepsPTOCount checks a newly-empty ACK neither resets the
// PTO counter nor changes RTT state (only the accepted clock advances).
func TestDuplicateAckKeepsPTOCount(t *testing.T) {
	c := mustNew(t, 25)
	must(t, c.HandshakeConfirmed(0))
	must(t, c.Send(0, SpaceA, 1, 10, true))
	must(t, c.Send(10, SpaceA, 2, 10, true))
	if _, err := c.Ack(100, SpaceA, []int64{1}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OnTimeout(1000); err != nil {
		t.Fatal(err)
	}
	before := c.RTT()
	lost, err := c.Ack(1001, SpaceA, []int64{1}, 0)
	if err != nil || len(lost) != 0 {
		t.Fatalf("duplicate ack: lost=%v err=%v", lost, err)
	}
	after := c.RTT()
	if after.PTOCount != before.PTOCount || after.PTOCount != 1 {
		t.Fatalf("ptoCount changed: before=%d after=%d", before.PTOCount, after.PTOCount)
	}
	if after.SRTT != before.SRTT || after.Latest != before.Latest {
		t.Fatalf("rtt changed on duplicate-only ack: %+v vs %+v", before, after)
	}
}

// TestHandshakeAckDelayIgnored checks ackDelay on H is always ignored.
func TestHandshakeAckDelayIgnored(t *testing.T) {
	c := mustNew(t, 25)
	must(t, c.Send(0, SpaceH, 0, 10, true))
	if _, err := c.Ack(100, SpaceH, []int64{0}, 9999); err != nil {
		t.Fatal(err)
	}
	if s := c.RTT(); s.SRTT != 100 || s.Latest != 100 {
		t.Fatalf("H ack delay leaked: %+v", s)
	}
}

// TestHandshakeDiscard checks H is emptied without losses and then rejected.
func TestHandshakeDiscard(t *testing.T) {
	c := mustNew(t, 25)
	must(t, c.Send(0, SpaceH, 1, 10, true))
	must(t, c.Send(0, SpaceA, 1, 10, true))
	must(t, c.HandshakeConfirmed(1))
	if _, err := c.Outstanding(SpaceH); !errors.Is(err, ErrSpaceDiscarded) {
		t.Fatalf("Outstanding H err=%v, want ErrSpaceDiscarded", err)
	}
	if err := c.Send(2, SpaceH, 2, 10, true); !errors.Is(err, ErrSpaceDiscarded) {
		t.Fatalf("Send H err=%v, want ErrSpaceDiscarded", err)
	}
	if _, err := c.Ack(2, SpaceH, []int64{1}, 0); !errors.Is(err, ErrSpaceDiscarded) {
		t.Fatalf("Ack H err=%v, want ErrSpaceDiscarded", err)
	}
	if out, err := c.Outstanding(SpaceA); err != nil || len(out) != 1 {
		t.Fatalf("A space after discard: %v %v", out, err)
	}
}

// TestRejections verifies first-error precedence and that rejected calls do
// not advance the accepted clock.
func TestRejections(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrInvalidMaxAckDelay) {
		t.Fatalf("New(-1) = %v", err)
	}
	if _, err := New(10001); !errors.Is(err, ErrInvalidMaxAckDelay) {
		t.Fatalf("New(10001) = %v", err)
	}

	c := mustNew(t, 25)
	must(t, c.Send(10, SpaceA, 5, 10, false))

	checks := []struct {
		name string
		err  error
		call func() error
	}{
		{"invalid space", ErrInvalidSpace, func() error { return c.Send(11, 'X', 6, 10, false) }},
		{"invalid size", ErrInvalidSize, func() error { return c.Send(11, SpaceA, 6, 0, false) }},
		{"invalid time", ErrInvalidTime, func() error { return c.Send(-1, SpaceA, 6, 10, false) }},
		{"negative pn", ErrNegativePN, func() error { return c.Send(11, SpaceA, -1, 10, false) }},
		{"clock backwards", ErrClockBackwards, func() error { return c.Send(9, SpaceA, 6, 10, false) }},
		{"pn not increasing", ErrPNNotIncreasing, func() error { return c.Send(11, SpaceA, 5, 10, false) }},
	}
	for _, tc := range checks {
		if err := tc.call(); !errors.Is(err, tc.err) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.err)
		}
	}

	if _, err := c.Ack(-1, SpaceA, nil, 0); !errors.Is(err, ErrInvalidTime) {
		t.Errorf("ack invalid time: %v", err)
	}
	if _, err := c.Ack(11, SpaceA, nil, 0); !errors.Is(err, ErrEmptyAck) {
		t.Errorf("ack empty: %v", err)
	}
	if _, err := c.Ack(11, SpaceA, []int64{-1}, 0); !errors.Is(err, ErrNegativePN) {
		t.Errorf("ack negative pn: %v", err)
	}
	if _, err := c.Ack(11, SpaceA, []int64{99}, 0); !errors.Is(err, ErrPNNeverSent) {
		t.Errorf("ack never sent: %v", err)
	}

	// Rejected call did not advance the clock past 10.
	must(t, c.Send(11, SpaceA, 6, 10, false))

	if _, err := c.OnTimeout(12); !errors.Is(err, ErrNoTimer) {
		t.Errorf("OnTimeout with no timer: %v", err)
	}
	must(t, c.Send(12, SpaceA, 7, 10, true))
	must(t, c.HandshakeConfirmed(13))
	info, ok := c.Timer()
	if !ok {
		t.Fatal("expected PTO timer")
	}
	if _, err := c.OnTimeout(info.Time - 1); !errors.Is(err, ErrTimeoutEarly) {
		t.Errorf("early timeout: %v", err)
	}
}
