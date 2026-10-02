package quicloss

import (
	"errors"
	"reflect"
	"testing"
)

// Loss timer takes priority even when a PTO timer would be earlier.
func TestLossPrioritizedOverPTO(t *testing.T) {
	c := mustNew(t, 0)
	if err := c.HandshakeConfirmed(0); err != nil {
		t.Fatal(err)
	}
	for pn := int64(0); pn < 5; pn++ {
		sendMust(t, c, pn, SpaceApp, pn, 10, true)
	}
	// latest=399 -> lossDelay=448. la=4: pn 0,1 lost by count;
	// pn 2,3 survive and set lt=450.
	lost, err := c.Ack(403, SpaceApp, []int64{4}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lost, []int64{0, 1}) {
		t.Fatalf("lost=%v", lost)
	}
	tm := c.Timer()
	if tm == nil || tm.Mode != ModeLoss || tm.Time != 450 || tm.Space != SpaceApp {
		t.Fatalf("timer=%+v want Loss@450 A", tm)
	}
}

// Before confirmation A-space never participates in PTO; H does.
func TestAppExcludedFromPTOBeforeConfirmation(t *testing.T) {
	c := mustNew(t, 25)
	sendMust(t, c, 0, SpaceApp, 0, 10, true)
	if c.Timer() != nil {
		t.Fatal("A-only must not schedule PTO before confirmation")
	}
	sendMust(t, c, 0, SpaceHandshake, 0, 10, true)
	tm := c.Timer()
	if tm == nil || tm.Mode != ModePTO || tm.Space != SpaceHandshake {
		t.Fatalf("H should schedule PTO: %+v", tm)
	}
	// Both spaces with an ae packet at the same time: tie picks H.
	c2 := mustNew(t, 25)
	sendMust(t, c2, 0, SpaceHandshake, 0, 10, true)
	sendMust(t, c2, 0, SpaceApp, 0, 10, true)
	tm2 := c2.Timer()
	if tm2 == nil || tm2.Space != SpaceHandshake {
		t.Fatalf("tie must pick H: %+v", tm2)
	}
}

// An ACK that acks nothing new must not reset ptoCount or alter RTT.
func TestDuplicateOnlyAckDoesNotResetPTO(t *testing.T) {
	c := mustNew(t, 25)
	sendMust(t, c, 0, SpaceHandshake, 0, 10, true)
	sendMust(t, c, 5, SpaceHandshake, 1, 10, true)
	if _, err := c.Ack(10, SpaceHandshake, []int64{0}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OnTimeout(c.Timer().Time); err != nil {
		t.Fatal(err)
	}
	if c.ptoCount != 1 {
		t.Fatalf("ptoCount=%d", c.ptoCount)
	}
	beforeSrtt := c.srtt
	lost, err := c.Ack(40, SpaceHandshake, []int64{0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 0 || c.ptoCount != 1 || c.srtt != beforeSrtt {
		t.Fatalf("dup-only ack changed state: lost=%v count=%d", lost, c.ptoCount)
	}

	sendMust(t, c, 1000, SpaceHandshake, 5, 10, true)
	lost, err = c.Ack(1001, SpaceHandshake, []int64{2, 3, 5}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.ptoCount != 0 {
		t.Fatalf("ack of newly-acked ae packet must reset count: %d", c.ptoCount)
	}
	if !reflect.DeepEqual(lost, []int64{1}) {
		t.Fatalf("lost=%v want [1]", lost)
	}
}

// ackDelay is ignored both on H and on A before confirmation.
func TestAckDelayIgnoredHandshakeOrUnconfirmed(t *testing.T) {
	// H space: delay ignored.
	c := mustNew(t, 0) // maxAckDelay=0 anyway; use H.
	sendMust(t, c, 0, SpaceHandshake, 0, 10, true)
	if _, err := c.Ack(100, SpaceHandshake, []int64{0}, 0); err != nil {
		t.Fatal(err)
	}
	sendMust(t, c, 100, SpaceHandshake, 1, 10, true)
	if _, err := c.Ack(240, SpaceHandshake, []int64{1}, 999); err != nil {
		t.Fatal(err)
	}
	// adj=140 => rttvar=floor(190/4)=47, srtt=floor(840/8)=105.
	if c.srtt != 105 || c.rttvar != 47 {
		t.Fatalf("H: srtt=%d rttvar=%d", c.srtt, c.rttvar)
	}

	// A space before confirmation: delay ignored.
	c2 := mustNew(t, 25)
	sendMust(t, c2, 0, SpaceApp, 0, 10, true)
	if _, err := c2.Ack(100, SpaceApp, []int64{0}, 0); err != nil {
		t.Fatal(err)
	}
	sendMust(t, c2, 100, SpaceApp, 1, 10, true)
	if _, err := c2.Ack(240, SpaceApp, []int64{1}, 25); err != nil {
		t.Fatal(err)
	}
	if c2.srtt != 105 || c2.rttvar != 47 {
		t.Fatalf("A pre-confirm: srtt=%d rttvar=%d", c2.srtt, c2.rttvar)
	}
}

// Handshake confirmation drops H silently and rejects later H operations.
func TestHandshakeDiscardSemantics(t *testing.T) {
	c := mustNew(t, 25)
	sendMust(t, c, 0, SpaceHandshake, 0, 10, true)
	sendMust(t, c, 5, SpaceHandshake, 1, 10, true)
	if _, err := c.OnTimeout(c.Timer().Time); err != nil {
		t.Fatal(err)
	}
	if err := c.HandshakeConfirmed(2000); err != nil {
		t.Fatal(err)
	}
	if len(c.h.packets) != 0 || !c.hDiscarded || c.ptoCount != 0 || c.h.lastAe != nil || c.h.lossTime != nil {
		t.Fatal("H was not fully reset")
	}
	wantErrIs(t, c.Send(2001, SpaceHandshake, 2, 10, true), ErrSpaceDiscarded)
	_, err := c.Ack(2001, SpaceHandshake, []int64{0}, 0)
	wantErrIs(t, err, ErrSpaceDiscarded)
	_, err = c.Detect(SpaceHandshake, 2001)
	wantErrIs(t, err, ErrSpaceDiscarded)
	if err := c.HandshakeConfirmed(2002); err != nil {
		t.Fatal(err)
	}
}

// Validation error precedence per spec.
func TestValidationAndPrecedence(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrInvalidAckDelay) {
		t.Fatalf("New(-1)=%v", err)
	}
	if _, err := New(10001); !errors.Is(err, ErrInvalidAckDelay) {
		t.Fatalf("New(10001)=%v", err)
	}
	c := mustNew(t, 25)
	wantErrIs(t, c.Send(0, "X", 0, 10, true), ErrInvalidSpace)
	wantErrIs(t, c.Send(0, SpaceApp, 0, 0, true), ErrInvalidSize)
	wantErrIs(t, c.Send(0, SpaceApp, 0, 65536, true), ErrInvalidSize)
	wantErrIs(t, c.Send(-1, SpaceApp, 0, 10, true), ErrInvalidTime)
	wantErrIs(t, c.Send(1_000_000_000_001, SpaceApp, 0, 10, true), ErrInvalidTime)
	wantErrIs(t, c.Send(0, SpaceApp, -1, 10, true), ErrInvalidPN)

	sendMust(t, c, 10, SpaceApp, 5, 10, true)
	wantErrIs(t, c.Send(9, SpaceApp, 6, 10, true), ErrClockBackward)
	wantErrIs(t, c.Send(10, SpaceApp, 5, 10, true), ErrPNNotIncreasing)

	_, err := c.Ack(11, SpaceApp, nil, 0)
	wantErrIs(t, err, ErrEmptyAck)
	_, err = c.Ack(11, SpaceApp, []int64{-2}, 0)
	wantErrIs(t, err, ErrInvalidPN)
	_, err = c.Ack(11, "X", []int64{0}, 0)
	wantErrIs(t, err, ErrInvalidSpace)
	_, err = c.Ack(11, SpaceApp, []int64{6}, 0)
	wantErrIs(t, err, ErrAckedNeverSent)
	_, err = c.Ack(9, SpaceApp, []int64{5}, 0)
	wantErrIs(t, err, ErrClockBackward)
	_, err = c.Ack(11, SpaceApp, []int64{5}, -1)
	wantErrIs(t, err, ErrInvalidAckDelay)

	// Early timeout: no timer at all.
	_, err = c.OnTimeout(12)
	wantErrIs(t, err, ErrTimeoutEarly)

	// Rejected call must not advance the clock: now=11 still accepted.
	if err := c.Send(11, SpaceApp, 6, 10, false); err != nil {
		t.Fatalf("clock must be unchanged: %v", err)
	}
}

// Early timeout with a scheduled future timer.
func TestTimeoutEarlyBeforeTimer(t *testing.T) {
	c := mustNew(t, 25)
	if err := c.HandshakeConfirmed(0); err != nil {
		t.Fatal(err)
	}
	sendMust(t, c, 1000, SpaceApp, 0, 10, true)
	tm := c.Timer()
	if tm == nil {
		t.Fatal("expected PTO timer")
	}
	_, err := c.OnTimeout(tm.Time - 1)
	wantErrIs(t, err, ErrTimeoutEarly)
	if c.ptoCount != 0 {
		t.Fatal("early timeout changed ptoCount")
	}
	// Non-ae packets schedule no PTO.
	c2 := mustNew(t, 25)
	if err := c2.HandshakeConfirmed(0); err != nil {
		t.Fatal(err)
	}
	sendMust(t, c2, 0, SpaceApp, 0, 10, false)
	if c2.Timer() != nil {
		t.Fatal("non-ae packets must not schedule PTO")
	}
}

// PTO backoff caps the exponent at 20.
func TestPTOBackoffCap(t *testing.T) {
	c := mustNew(t, 25)
	sendMust(t, c, 0, SpaceHandshake, 0, 10, true)
	for i := 0; i < 25; i++ {
		tm := c.Timer()
		if tm == nil {
			t.Fatal("timer vanished")
		}
		res, err := c.OnTimeout(tm.Time)
		if err != nil {
			t.Fatal(err)
		}
		if res.Probe == nil {
			t.Fatal("expected probe")
		}
	}
	if c.ptoCount != 25 {
		t.Fatalf("count=%d", c.ptoCount)
	}
	pto := c.srtt + max64(4*c.rttvar, 1)
	tm := c.Timer()
	if tm.Time != 0+pto*(int64(1)<<20) {
		t.Fatalf("backoff not capped: %d", tm.Time)
	}
}
