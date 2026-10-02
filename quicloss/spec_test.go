package quicloss

import (
	"errors"
	"reflect"
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

func sendMust(t *testing.T, c *Controller, now int64, sp Space, pn int64, size int, ae bool) {
	t.Helper()
	if err := c.Send(now, sp, pn, size, ae); err != nil {
		t.Fatalf("Send(%s,%d): %v", sp, pn, err)
	}
}

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

// Spec loss example: A-space pn 1..5 sent at 0,10,20,399,400, all
// ack-eliciting; ack {5} at 420 detects losses 1,2,3 and sets lt=421.
func TestSpecLossExample(t *testing.T) {
	c := mustNew(t, 25)
	sends := []int64{0, 10, 20, 399, 400}
	for i, ts := range sends {
		sendMust(t, c, ts, SpaceApp, int64(i+1), 100, true)
	}
	lost, err := c.Ack(420, SpaceApp, []int64{5}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lost, []int64{1, 2, 3}) {
		t.Fatalf("lost = %v, want [1 2 3]", lost)
	}
	if c.latest != 20 || c.srtt != 20 || c.rttvar != 10 || !c.hasSample {
		t.Fatalf("rtt state latest=%d srtt=%d rttvar=%d", c.latest, c.srtt, c.rttvar)
	}
	tm := c.Timer()
	if tm == nil || tm.Time != 421 || tm.Mode != ModeLoss || tm.Space != SpaceApp {
		t.Fatalf("timer = %+v, want (421 Loss A)", tm)
	}

	// Fire at 421: 399 <= 421-22 (equality), so pn 4 is now lost.
	res, err := c.OnTimeout(421)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeLoss || !reflect.DeepEqual(res.LostPN, []int64{4}) {
		t.Fatalf("timeout = %+v", res)
	}
}

// Spec RTT example: first sample 100, then confirmed samples 140 and 105.
func TestSpecRTTExample(t *testing.T) {
	c := mustNew(t, 25)
	sendMust(t, c, 0, SpaceHandshake, 0, 10, true)
	if _, err := c.Ack(100, SpaceHandshake, []int64{0}, 999); err != nil {
		t.Fatal(err)
	}
	if c.srtt != 100 || c.rttvar != 50 || c.latest != 100 || *c.minRtt != 100 {
		t.Fatalf("first sample: srtt=%d rttvar=%d latest=%d min=%v", c.srtt, c.rttvar, c.latest, c.minRtt)
	}

	if err := c.HandshakeConfirmed(200); err != nil {
		t.Fatal(err)
	}

	// latest=140, ackDelay=10 -> adj=130: rttvar=45, srtt=103.
	sendMust(t, c, 900, SpaceApp, 0, 10, true)
	if _, err := c.Ack(1040, SpaceApp, []int64{0}, 10); err != nil {
		t.Fatal(err)
	}
	if c.latest != 140 || c.srtt != 103 || c.rttvar != 45 {
		t.Fatalf("second sample: latest=%d srtt=%d rttvar=%d", c.latest, c.srtt, c.rttvar)
	}

	// latest=105 < minRtt(100)+ad(10): no delay subtraction.
	sendMust(t, c, 2000, SpaceApp, 1, 10, true)
	if _, err := c.Ack(2105, SpaceApp, []int64{1}, 10); err != nil {
		t.Fatal(err)
	}
	if c.latest != 105 || c.srtt != 103 || c.rttvar != 34 {
		t.Fatalf("third sample: latest=%d srtt=%d rttvar=%d", c.latest, c.srtt, c.rttvar)
	}
}

// Spec PTO example: pto=264, timer 1264; doubling after fire gives 1792.
func TestSpecPTOExample(t *testing.T) {
	c := mustNew(t, 25)
	if err := c.HandshakeConfirmed(0); err != nil {
		t.Fatal(err)
	}
	// Build srtt=103, rttvar=34 with the same sequence as the RTT example.
	sendMust(t, c, 0, SpaceApp, 0, 10, true)
	if _, err := c.Ack(100, SpaceApp, []int64{0}, 0); err != nil {
		t.Fatal(err)
	}
	sendMust(t, c, 100, SpaceApp, 1, 10, true)
	if _, err := c.Ack(240, SpaceApp, []int64{1}, 10); err != nil {
		t.Fatal(err)
	}
	sendMust(t, c, 300, SpaceApp, 2, 10, true)
	if _, err := c.Ack(405, SpaceApp, []int64{2}, 10); err != nil {
		t.Fatal(err)
	}
	if c.srtt != 103 || c.rttvar != 34 {
		t.Fatalf("setup srtt=%d rttvar=%d", c.srtt, c.rttvar)
	}

	sendMust(t, c, 1000, SpaceApp, 3, 10, true)
	tm := c.Timer()
	if tm == nil || tm.Time != 1264 || tm.Mode != ModePTO {
		t.Fatalf("timer = %+v, want 1264 PTO", tm)
	}

	res, err := c.OnTimeout(1264)
	if err != nil {
		t.Fatal(err)
	}
	if res.Probe == nil || res.Probe.Count != 1 || res.Probe.Space != SpaceApp {
		t.Fatalf("probe = %+v", res.Probe)
	}

	sendMust(t, c, 1264, SpaceApp, 4, 10, true)
	tm = c.Timer()
	if tm == nil || tm.Time != 1792 || tm.Mode != ModePTO {
		t.Fatalf("timer = %+v, want 1792 PTO", tm)
	}
}
