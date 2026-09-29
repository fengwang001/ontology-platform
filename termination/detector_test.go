package termination

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type testLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(strings.TrimSpace(sprintf(format, args...)) + "\n")
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func TestMessageArrivingAfterTokenPreventsFalseTermination(t *testing.T) {
	logger := &testLogger{}
	d, err := New(2, WithLogger(logger), WithActiveProcesses(0))
	if err != nil {
		t.Fatal(err)
	}

	message, err := d.Send(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}

	if _, announced, err := d.PassToken(0); err != nil || announced {
		t.Fatalf("first pass = (%v, %v), want round started", err, announced)
	}
	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	if _, announced, err := d.PassToken(1); err != nil || announced {
		t.Fatalf("token after process 1 = (%v, %v), want return to initiator", err, announced)
	}
	if _, announced, err := d.PassToken(0); err != nil || announced {
		t.Fatalf("baseline return = (%v, %v), want another round", err, announced)
	}
	if err := d.Deliver(message, 1); err != nil {
		t.Fatal(err)
	}
	if got := d.Snapshot().Round; got != 2 {
		t.Fatalf("round = %d, want 2", got)
	}

	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	passRing(t, d, 1)
	if _, announced, err := d.PassToken(0); err != nil || announced {
		t.Fatalf("black round = (%v, %v), want another round", err, announced)
	}
	passRing(t, d, 1)
	announcement, announced, err := d.PassToken(0)
	if err != nil || !announced {
		t.Fatalf("final pass = (%v, %v), want termination", err, announced)
	}
	if announcement.Round != 3 {
		t.Fatalf("announced round = %d, want 3", announcement.Round)
	}
	assertTerminated(t, d)
	assertLogBasis(t, logger.String())
}

func TestMessageAndTokenRaceIsCaughtByColor(t *testing.T) {
	logger := &testLogger{}
	d, err := New(2, WithLogger(logger), WithActiveProcesses(0, 1))
	if err != nil {
		t.Fatal(err)
	}

	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}

	message, err := d.Send(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(message, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	if _, announced, err := d.PassToken(1); err != nil || announced {
		t.Fatalf("race token transfer = (%v, %v), want return to initiator", err, announced)
	}
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	if _, announced, err := d.PassToken(0); err != nil || announced {
		t.Fatalf("baseline return = (%v, %v), want another round", err, announced)
	}
	if got := d.Snapshot().Round; got != 2 {
		t.Fatalf("round = %d, want 2", got)
	}

	passRing(t, d, 1)
	if _, announced, err := d.PassToken(0); err != nil || !announced {
		t.Fatalf("final race pass = (%v, %v), want termination", err, announced)
	}
	assertTerminated(t, d)
	assertLogBasis(t, logger.String())
}

func TestTerminatesWithinAtMostTwoRoundsAfterGlobalQuiescence(t *testing.T) {
	logger := &testLogger{}
	d, err := New(4, WithLogger(logger), WithActiveProcesses(0))
	if err != nil {
		t.Fatal(err)
	}
	message, err := d.Send(0, 3)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}

	if err := d.Deliver(message, 3); err != nil {
		t.Fatal(err)
	}
	if err := d.BecomeIdle(3); err != nil {
		t.Fatal(err)
	}

	announcement, announced := completeCurrentAndNextRound(t, d, 4)
	if !announced {
		t.Fatal("termination was not announced within two rounds after quiescence")
	}
	if announcement.Round > 3 {
		t.Fatalf("announced round = %d, want at most 3", announcement.Round)
	}
	assertTerminated(t, d)
	assertLogBasis(t, logger.String())
}

func TestSingleProcessRing(t *testing.T) {
	logger := &testLogger{}
	d, err := New(1, WithLogger(logger), WithActiveProcesses(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}

	announcement, announced, err := d.PassToken(0)
	if err != nil || !announced {
		t.Fatalf("single process pass = (%v, %v), want termination", err, announced)
	}
	if announcement.Round != 1 {
		t.Fatalf("round = %d, want 1", announcement.Round)
	}
	assertTerminated(t, d)
	assertLogBasis(t, logger.String())
}

func TestInvalidConstruction(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidRing) {
		t.Fatalf("New(0) error = %v, want %v", err, ErrInvalidRing)
	}
	_, err := New(2, WithActiveProcesses(2))
	if !errors.Is(err, ErrInvalidProcess) {
		t.Fatalf("invalid active error = %v, want %v", err, ErrInvalidProcess)
	}
}

func TestRejectedOperationsAreDistinguishableAndAtomic(t *testing.T) {
	logger := &testLogger{}
	d, err := New(2, WithLogger(logger), WithActiveProcesses(0))
	if err != nil {
		t.Fatal(err)
	}
	message, err := d.Send(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{"invalid sender", func() error { _, err := d.Send(2, 0); return err }, ErrInvalidProcess},
		{"invalid recipient", func() error { _, err := d.Send(0, -1); return err }, ErrInvalidProcess},
		{"idle sender", func() error { _, err := d.Send(1, 0); return err }, ErrIdleSender},
		{"self send", func() error { _, err := d.Send(0, 0); return err }, ErrSelfMessage},
		{"missing message", func() error { return d.Deliver(Message{ID: 99}, 1) }, ErrMessageNotFound},
		{"wrong destination", func() error { return d.Deliver(message, 0) }, ErrWrongDestination},
		{"non holder pass", func() error { _, _, err := d.PassToken(1); return err }, ErrNotTokenHolder},
		{"active holder pass", func() error { _, _, err := d.PassToken(0); return err }, ErrActiveTokenHolder},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := d.Snapshot()
			if !errors.Is(tc.run(), tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if after := d.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected operation changed state\nbefore: %+v\nafter:  %+v", before, after)
			}
		})
	}

	if err := d.Deliver(message, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(message, 1); !errors.Is(err, ErrAlreadyDelivered) {
		t.Fatalf("redelivery error = %v, want %v", err, ErrAlreadyDelivered)
	}

	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}
	_, err = d.Send(0, 1)
	if !errors.Is(err, ErrTerminated) {
		t.Fatalf("post termination send = %v, want %v", err, ErrTerminated)
	}

	if got := d.Snapshot(); got.PendingMessages != 0 {
		t.Fatalf("pending after test = %d, want 0", got.PendingMessages)
	}
}

func passRing(t *testing.T, d *Detector, start int) {
	t.Helper()
	for holder := start; holder > 0; holder-- {
		if _, announced, err := d.PassToken(holder); err != nil || announced {
			t.Fatalf("holder %d = (%v, %v), want transfer", holder, err, announced)
		}
	}
}

func completeCurrentAndNextRound(t *testing.T, d *Detector, n int) (Announcement, bool) {
	t.Helper()
	for attempt := 0; attempt < 2; attempt++ {
		for holder := n - 1; holder > 0; holder-- {
			if _, announced, err := d.PassToken(holder); err != nil || announced {
				t.Fatalf("holder %d = (%v, %v), want transfer", holder, err, announced)
			}
		}
		announcement, announced, err := d.PassToken(0)
		if err != nil {
			t.Fatal(err)
		}
		if announced {
			return announcement, true
		}
	}
	return Announcement{}, false
}

func assertTerminated(t *testing.T, d *Detector) {
	t.Helper()
	snapshot := d.Snapshot()
	if !snapshot.Announced || snapshot.PendingMessages != 0 {
		t.Fatalf("snapshot = %+v, want announced with no pending messages", snapshot)
	}
	var sum int64
	for i, process := range snapshot.Processes {
		if process.State != Idle {
			t.Fatalf("process %d state = %v, want idle", i, process.State)
		}
		sum += process.Counter
	}
	if sum != int64(snapshot.PendingMessages) {
		t.Fatalf("counter sum = %d, want pending %d", sum, snapshot.PendingMessages)
	}
}

func assertLogBasis(t *testing.T, logs string) {
	t.Helper()
	for _, want := range []string{"input", "output", "reason="} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}
}
