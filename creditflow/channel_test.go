package creditflow

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
)

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h), &buf
}

func TestNewRejectsNonPositiveParams(t *testing.T) {
	for _, tc := range [][2]int64{
		{0, 1}, {-1, 1}, {1, 0}, {1, -2}, {0, 0},
	} {
		if _, err := New(tc[0], tc[1], nil); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d,%d) err=%v, want ErrInvalidParam", tc[0], tc[1], err)
		}
	}
}

func TestProduceWithoutCreditStaysInBacklog(t *testing.T) {
	lg, _ := testLogger()
	ch, err := New(2, 5, lg)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := ch.Produce([]byte("a"))
	if err != nil || seq != 1 {
		t.Fatalf("Produce = %d,%v", seq, err)
	}
	s := ch.Snapshot()
	if s.Credit != 0 || s.Backlog != 1 || s.Buffered != 0 || s.ProducedTotal != 1 {
		t.Fatalf("unexpected snapshot %+v", s)
	}
}

func TestAdvertiseGrantsFreeSlotsAndAutoSends(t *testing.T) {
	lg, _ := testLogger()
	ch, _ := New(3, 10, lg)

	for i := 0; i < 5; i++ {
		if _, err := ch.Produce([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := ch.Snapshot().Backlog; got != 5 {
		t.Fatalf("backlog=%d want 5", got)
	}

	if granted := ch.Advertise(); granted != 3 {
		t.Fatalf("granted=%d want 3", granted)
	}
	s := ch.Snapshot()
	if s.Credit != 0 || s.InFlight != 0 || s.Buffered != 3 || s.Backlog != 2 {
		t.Fatalf("after advertise: %+v", s)
	}

	// Buffer full: free = 3 - 3 - 0 = 0, advertise grants nothing.
	if granted := ch.Advertise(); granted != 0 {
		t.Fatalf("second advertise granted=%d want 0", granted)
	}

	// Consume 2, then free = 3 - 1 - 0 = 2 and the backlog drains fully.
	if _, err := ch.Consume(1); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Consume(2); err != nil {
		t.Fatal(err)
	}
	if granted := ch.Advertise(); granted != 2 {
		t.Fatalf("third advertise granted=%d want 2", granted)
	}
	s = ch.Snapshot()
	if s.Buffered != 3 || s.Backlog != 0 || s.ProducedTotal != 5 {
		t.Fatalf("after drain: %+v", s)
	}
}

func TestProduceWhileCreditExistsSendsImmediately(t *testing.T) {
	lg, _ := testLogger()
	ch, _ := New(2, 10, lg)
	if g := ch.Advertise(); g != 2 {
		t.Fatalf("granted=%d", g)
	}
	if _, err := ch.Produce(nil); err != nil {
		t.Fatal(err)
	}
	s := ch.Snapshot()
	if s.Credit != 1 || s.Buffered != 1 || s.Backlog != 0 {
		t.Fatalf("snapshot %+v", s)
	}
	if _, err := ch.Produce(nil); err != nil {
		t.Fatal(err)
	}
	// Third message: credit is already zero, so it must queue in the backlog.
	if _, err := ch.Produce(nil); err != nil {
		t.Fatal(err)
	}
	s = ch.Snapshot()
	if s.Credit != 0 || s.Buffered != 2 || s.Backlog != 1 {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestConsumeIsOrderedAndNeverAdvertises(t *testing.T) {
	lg, logBuf := testLogger()
	ch, _ := New(2, 10, lg)
	ch.Advertise()
	for i := 0; i < 2; i++ {
		if _, err := ch.Produce([]byte{byte('a' + i)}); err != nil {
			t.Fatal(err)
		}
	}

	// Non-positive / gap / future numbers are all out of bounds.
	if _, err := ch.Consume(0); !errors.Is(err, ErrConsumeOutOfBounds) {
		t.Fatalf("Consume(0) err=%v", err)
	}
	if _, err := ch.Consume(2); !errors.Is(err, ErrConsumeOutOfBounds) {
		t.Fatalf("Consume(2) before 1 err=%v", err)
	}

	msg, err := ch.Consume(1)
	if err != nil || string(msg.Payload) != "a" {
		t.Fatalf("Consume(1)=%+v,%v", msg, err)
	}
	if _, err := ch.Consume(1); !errors.Is(err, ErrConsumeOutOfBounds) {
		t.Fatalf("duplicate Consume(1) err=%v", err)
	}
	if _, err := ch.Consume(3); !errors.Is(err, ErrConsumeOutOfBounds) {
		t.Fatalf("Consume(3) gap err=%v", err)
	}

	// Consumption never grants credit: it stays zero until an explicit advertise.
	if s := ch.Snapshot(); s.Credit != 0 || s.Buffered != 1 {
		t.Fatalf("post-consume snapshot %+v", s)
	}
	// Log lines produced by the consume call itself must never describe a
	// grant (credit accounting is reported per operation that caused it).
	for _, line := range bytes.Split(logBuf.Bytes(), []byte{'\n'}) {
		if bytes.Contains(line, []byte("op=consume")) && bytes.Contains(line, []byte("decision=grant")) {
			t.Fatal("consume must not trigger an advertisement")
		}
	}
}

func TestProbeRules(t *testing.T) {
	lg, _ := testLogger()
	ch, _ := New(2, 5, lg)

	// No backlog: probe rejected.
	if _, err := ch.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("probe with empty backlog err=%v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := ch.Produce(nil); err != nil {
			t.Fatal(err)
		}
	}
	if g, err := ch.Probe(); err != nil || g != 2 {
		t.Fatalf("probe=%d,%v want 2 granted", g, err)
	}
	if s := ch.Snapshot(); s.Credit != 0 || s.Buffered != 2 || s.Backlog != 1 {
		t.Fatalf("post-probe %+v", s)
	}

	// Zero credit but buffer full: probe is allowed but behaves exactly like
	// an advertise with no free slots — grants nothing, and is not remembered.
	if g, err := ch.Probe(); err != nil || g != 0 {
		t.Fatalf("probe while full=%d,%v", g, err)
	}

	// Consuming frees one slot; the forgotten probe is not replayed, but a
	// fresh probe grants exactly one.
	if _, err := ch.Consume(1); err != nil {
		t.Fatal(err)
	}
	if s := ch.Snapshot(); s.Credit != 0 {
		t.Fatalf("credit after consume = %d want 0", s.Credit)
	}
	if g, err := ch.Probe(); err != nil || g != 1 {
		t.Fatalf("fresh probe=%d,%v want 1", g, err)
	}
	if s := ch.Snapshot(); s.Backlog != 0 || s.Buffered != 2 {
		t.Fatalf("snapshot %+v", s)
	}

	// Drain the buffer so one free slot exists, advertise with an empty
	// backlog: the granted credit stays held (unspent) on the sender.
	if _, err := ch.Consume(2); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Consume(3); err != nil {
		t.Fatal(err)
	}
	if g := ch.Advertise(); g != 2 {
		t.Fatalf("advertise=%d want 2", g)
	}
	if _, err := ch.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("probe with credit err=%v", err)
	}
}

func TestBacklogOverflowRejectsWithoutTrace(t *testing.T) {
	lg, _ := testLogger()
	ch, _ := New(1, 2, lg)
	for i := 0; i < 2; i++ {
		if _, err := ch.Produce([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	before := ch.Snapshot()
	_, err := ch.Produce([]byte("x"))
	if !errors.Is(err, ErrBacklogOverflow) {
		t.Fatalf("err=%v want ErrBacklogOverflow", err)
	}
	after := ch.Snapshot()
	if before != after {
		t.Fatalf("state changed by rejected call: before=%+v after=%+v", before, after)
	}
}

func TestRejectionsLeaveStateUntouched(t *testing.T) {
	lg, _ := testLogger()

	// Non-positive construction parameters.
	if _, err := New(0, 1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatal("expected ErrInvalidParam")
	}

	// Consume out of bounds: nothing has been delivered yet.
	ch, _ := New(2, 4, lg)
	if _, err := ch.Produce([]byte("a")); err != nil {
		t.Fatal(err)
	}
	before := ch.Snapshot()
	if _, err := ch.Consume(1); !errors.Is(err, ErrConsumeOutOfBounds) {
		t.Fatalf("err=%v", err)
	}
	if after := ch.Snapshot(); before != after {
		t.Fatalf("state changed by rejected consume:\nbefore=%+v\nafter =%+v", before, after)
	}

	// Probe not allowed: sender still holds credit.
	ch2, _ := New(2, 4, lg)
	ch2.Advertise()
	before2 := ch2.Snapshot()
	if _, err := ch2.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("err=%v", err)
	}
	if after := ch2.Snapshot(); before2 != after {
		t.Fatalf("state changed by rejected probe:\nbefore=%+v\nafter =%+v", before2, after)
	}

	// Backlog overflow: queue is at its limit.
	ch3, _ := New(2, 2, lg)
	for i := 0; i < 2; i++ {
		if _, err := ch3.Produce([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	before3 := ch3.Snapshot()
	if _, err := ch3.Produce([]byte("overflow")); !errors.Is(err, ErrBacklogOverflow) {
		t.Fatalf("err=%v", err)
	}
	if after := ch3.Snapshot(); before3 != after {
		t.Fatalf("state changed by rejected produce:\nbefore=%+v\nafter =%+v", before3, after)
	}
}

func TestErrorClassesAreDistinct(t *testing.T) {
	errs := []error{ErrInvalidParam, ErrConsumeOutOfBounds, ErrProbeNotAllowed, ErrBacklogOverflow}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) || errors.Is(errs[j], errs[i]) {
				t.Fatalf("error classes %d and %d are not distinguishable", i, j)
			}
		}
	}
}

func TestLoggingPrintsInputsStateAndReason(t *testing.T) {
	lg, buf := testLogger()
	ch, _ := New(1, 2, lg)
	ch.Produce([]byte("hello"))
	if _, err := ch.Probe(); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"op=produce", "assigned_seq=1", "credit=0", "backlog=1", "buffered=0",
		"decision=accept", "op=probe", "decision=grant", "reason=",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("log missing %q in:\n%s", want, log)
		}
	}
}
