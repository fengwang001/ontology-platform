package erase

import (
	"errors"
	"sync"
	"testing"
)

func TestHoldDeferralReleaseRestartsSLA(t *testing.T) {
	l := New(2, 100)
	if err := l.Hold(2, 8, 5); err != nil {
		t.Fatalf("hold: %v", err)
	}
	e1, err := l.Request(1, 7, 10)
	if err != nil || e1 != 1 {
		t.Fatalf("request subject 7: id=%d err=%v", e1, err)
	}
	e2, err := l.Request(1, 8, 12)
	if err != nil || e2 != 2 {
		t.Fatalf("request subject 8: id=%d err=%v", e2, err)
	}
	if got := mustErasure(t, l, 2).Status; got != Deferred {
		t.Fatalf("held request status=%d, want Deferred", got)
	}
	if err := l.Ack(3, 2, 1, 13); !errors.Is(err, ErrState) {
		t.Fatalf("ack deferred err=%v, want ErrState", err)
	}
	if _, err := l.Request(1, 8, 14); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate deferred err=%v, want ErrDuplicate", err)
	}

	b1, err := l.Backup(3, 1, 20)
	if err != nil || b1 != 1 {
		t.Fatalf("backup: id=%d err=%v", b1, err)
	}
	if err := l.Ack(3, 1, 1, 30); err != nil {
		t.Fatalf("ack system 1: %v", err)
	}
	replay, err := l.Restore(3, 1, b1, 40)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(replay) != 1 || replay[0] != 1 {
		t.Fatalf("replay=%v, want [1]", replay)
	}
	if err := l.Read(1); !errors.Is(err, ErrRestoring) {
		t.Fatalf("read restoring err=%v, want ErrRestoring", err)
	}
	if err := l.ReapplyDone(3, 1, 1, 50); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if err := l.Read(1); err != nil {
		t.Fatalf("read after replay: %v", err)
	}

	b2, err := l.Backup(3, 1, 60)
	if err != nil || b2 != 2 {
		t.Fatalf("second backup: id=%d err=%v", b2, err)
	}
	replay, err = l.Restore(3, 1, b2, 70)
	if err != nil || len(replay) != 0 {
		t.Fatalf("restore after later backup replay=%v err=%v", replay, err)
	}
	overdue := l.Overdue(110)
	if len(overdue) != 1 || overdue[0].Erasure != 1 || len(overdue[0].PendingSystems) != 1 || overdue[0].PendingSystems[0] != 2 {
		t.Fatalf("overdue=%+v, want e1 pending {2}", overdue)
	}
	if err := l.Release(2, 8, 200); err != nil {
		t.Fatalf("release: %v", err)
	}
	e2State := mustErasure(t, l, 2)
	if e2State.Status != Active || e2State.Deadline != 300 {
		t.Fatalf("released e2=%+v, want Active deadline 300", e2State)
	}
	if err := l.Hold(2, 8, 201); err != nil {
		t.Fatalf("hold after release: %v", err)
	}
}

func TestAckEqualBackupIsNotReplayed(t *testing.T) {
	for _, ackAt := range []int{10, 11} {
		name := "equal"
		wantReplay := 0
		if ackAt == 11 {
			name = "later"
			wantReplay = 1
		}
		t.Run(name, func(t *testing.T) {
			l := New(1, 100)
			if _, err := l.Request(1, 5, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Backup(3, 1, 10); err != nil {
				t.Fatal(err)
			}
			if err := l.Ack(3, 1, 1, ackAt); err != nil {
				t.Fatal(err)
			}
			replay, err := l.Restore(3, 1, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			if len(replay) != wantReplay {
				t.Fatalf("replay=%v, want %d items", replay, wantReplay)
			}
		})
	}
}

func TestDoneAllowsNewRequestAndRestoringAcceptsNewAck(t *testing.T) {
	l := New(1, 100)
	if _, err := l.Request(1, 5, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Backup(3, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack(3, 1, 1, 11); err != nil {
		t.Fatal(err)
	}
	if got := mustErasure(t, l, 1).Status; got != Done {
		t.Fatalf("e1 status=%d, want Done", got)
	}
	if _, err := l.Request(1, 5, 20); err != nil {
		t.Fatalf("request after done: %v", err)
	}
	if err := l.Ack(3, 2, 1, 25); err != nil {
		t.Fatal(err)
	}
	replay, err := l.Restore(3, 1, 1, 26)
	if err != nil || len(replay) != 2 || replay[0] != 1 || replay[1] != 2 {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if _, err := l.Request(1, 6, 30); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack(3, 3, 1, 31); err != nil {
		t.Fatalf("new ack while restoring: %v", err)
	}
	if err := l.Read(1); !errors.Is(err, ErrRestoring) {
		t.Fatalf("read err=%v, want restoring", err)
	}
	if err := l.ReapplyDone(3, 1, 2, 40); err != nil {
		t.Fatal(err)
	}
	if err := l.Read(1); !errors.Is(err, ErrRestoring) {
		t.Fatalf("read after one reapply err=%v, want restoring", err)
	}
	if err := l.ReapplyDone(3, 1, 1, 50); err != nil {
		t.Fatal(err)
	}
	if err := l.Read(1); err != nil {
		t.Fatalf("read after replay empty: %v", err)
	}
}

func TestRejectedOperationsDoNotChangeStateOrClock(t *testing.T) {
	l := New(2, 100)
	if _, err := l.Request(1, 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Request(2, 2, 11); !errors.Is(err, ErrRole) {
		t.Fatalf("role err=%v", err)
	}
	if _, err := l.Request(1, 1, 9); !errors.Is(err, ErrClock) {
		t.Fatalf("clock err=%v", err)
	}
	if _, err := l.Request(1, 1, 10); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate err=%v", err)
	}
	if _, err := l.Backup(3, 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Backup(3, 2, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Restore(3, 2, 1, 11); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("missing backup err=%v", err)
	}
	if err := l.Ack(3, 2, 2, 11); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing erasure state err=%v", err)
	}
	if err := l.Ack(3, 1, 1, 10); err != nil {
		t.Fatalf("same timestamp ack: %v", err)
	}
	if _, err := l.Restore(3, 1, 1, 10); err != nil {
		t.Fatalf("empty restore at retained max now: %v", err)
	}
}

func TestConcurrentRequestsAreSerialized(t *testing.T) {
	l := New(1, 100)
	const workers = 32
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(subject int) {
			defer wg.Done()
			_, err := l.Request(1, subject, 1)
			results <- err
		}(i + 1)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent request: %v", err)
		}
	}
	for id := 1; id <= workers; id++ {
		e, ok := l.Erasure(id)
		if !ok || e.Status != Active {
			t.Fatalf("erasure %d ok=%v status=%d", id, ok, e.Status)
		}
	}
}

func TestAckAtZeroIsDistinguishableFromPending(t *testing.T) {
	l := New(1, 100)
	if _, err := l.Request(1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack(3, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack(3, 1, 1, 0); !errors.Is(err, ErrState) {
		t.Fatalf("duplicate ack err=%v, want ErrState", err)
	}
	if got := mustErasure(t, l, 1).Status; got != Done {
		t.Fatalf("status=%d, want Done", got)
	}
}

func mustErasure(t *testing.T, l *Ledger, id int) Erasure {
	t.Helper()
	e, ok := l.Erasure(id)
	if !ok {
		t.Fatalf("missing erasure %d", id)
	}
	return e
}
