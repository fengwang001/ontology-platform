package pbft

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func logObserve(t *testing.T, log *Log, msg Message) error {
	t.Helper()
	err := log.Observe(msg)
	t.Logf("input=%#v output-error=%v decision=%s", msg, err, observeDecision(err))
	return err
}

func observeDecision(err error) string {
	if err == nil {
		return "accepted"
	}
	return "rejected: " + err.Error()
}

func logExecute(t *testing.T, log *Log) []Execution {
	t.Helper()
	result := log.Execute()
	t.Logf("input=Execute output=%#v decision=%s", result, executeDecision(result))
	return result
}

func executeDecision(result []Execution) string {
	if len(result) == 0 {
		return "no contiguous committed-local sequence"
	}
	return "executed strictly increasing sequence numbers"
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func mustNewLog(t *testing.T, f int, view int64, windowSize int) *Log {
	t.Helper()
	log, err := New(f, view, windowSize)
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func completeSequence(t *testing.T, log *Log, f int, seq int64, digest string) {
	t.Helper()
	if err := logObserve(t, log, Message{PrePrepare, log.view, seq, digest, log.primary}); err != nil {
		t.Fatal(err)
	}
	for from := 1; from <= 2*f; from++ {
		if err := logObserve(t, log, Message{Prepare, log.view, seq, digest, from}); err != nil {
			t.Fatal(err)
		}
	}
	for from := 0; from <= 2*f; from++ {
		if err := logObserve(t, log, Message{Commit, log.view, seq, digest, from}); err != nil {
			t.Fatal(err)
		}
	}
}

func wantExecution(t *testing.T, got, want []Execution, n int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execution = %#v, want %#v (N=%d)", got, want, n)
	}
	if cap(got) > len(got) {
		t.Fatalf("Execute returned a slice with spare capacity: len=%d cap=%d", len(got), cap(got))
	}
}

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		name       string
		f          int
		windowSize int
	}{
		{"f zero", 0, 1},
		{"f negative", -1, 1},
		{"window zero", 1, 0},
		{"window negative", 1, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.f, 0, tc.windowSize)
			t.Logf("input=f=%d,L=%d output-error=%v decision=constructor validation", tc.f, tc.windowSize, err)
			requireErrorIs(t, err, ErrInvalidParameters)
		})
	}
}

func TestPreparedThresholdPrimaryAndDigestRules(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 1)

			if err := logObserve(t, log, Message{PrePrepare, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			for from := 0; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Commit, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}

			for from := 1; from <= 2*f-1; from++ {
				if err := logObserve(t, log, Message{Prepare, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("with %d prepares result = %#v, want no execution", 2*f-1, result)
			}

			requireErrorIs(t, logObserve(t, log, Message{Prepare, 0, 1, "d", 0}), ErrPrepareFromPrimary)
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("primary prepare changed certificate state: %#v", result)
			}

			if err := logObserve(t, log, Message{Prepare, 0, 1, "d", 2 * f}); err != nil {
				t.Fatal(err)
			}
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)
		})
	}
}

func TestPrepareDigestAndDuplicateCounting(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("digest-f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 1)
			if err := logObserve(t, log, Message{PrePrepare, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			for from := 0; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Commit, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}

			if err := logObserve(t, log, Message{Prepare, 0, 1, "other", 1}); err != nil {
				t.Fatal(err)
			}
			for from := 2; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Prepare, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("digest-mismatching prepare was counted: %#v", result)
			}
			if err := logObserve(t, log, Message{Prepare, 0, 1, "d", 2*f + 1}); err != nil {
				t.Fatal(err)
			}
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)
		})

		t.Run(fmt.Sprintf("duplicate-f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 1)
			if err := logObserve(t, log, Message{PrePrepare, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			for from := 0; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Commit, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			if err := logObserve(t, log, Message{Prepare, 0, 1, "d", 1}); err != nil {
				t.Fatal(err)
			}
			if err := logObserve(t, log, Message{Prepare, 0, 1, "d", 1}); err != nil {
				t.Fatal(err)
			}
			for from := 2; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Prepare, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)
		})
	}
}

func TestCommittedLocalThreshold(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 1)
			if err := logObserve(t, log, Message{PrePrepare, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			for from := 1; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Prepare, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}

			for from := 0; from <= 2*f-1; from++ {
				if err := logObserve(t, log, Message{Commit, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("with %d commits result = %#v", 2*f, result)
			}

			if err := logObserve(t, log, Message{Commit, 0, 1, "d", 2 * f}); err != nil {
				t.Fatal(err)
			}
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)
		})
	}
}

func TestCommitsBeforePrepareAndPrePrepare(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 1)
			for from := 0; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Commit, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("commits alone executed sequence: %#v", result)
			}

			if err := logObserve(t, log, Message{PrePrepare, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("pre-prepare with old commits executed without prepares: %#v", result)
			}

			for from := 1; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Prepare, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)
		})
	}
}

func TestWindowAdvancesAfterExecution(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 2)
			completeSequence(t, log, f, 1, "d")
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)

			if err := logObserve(t, log, Message{Commit, 0, 3, "d", 0}); err != nil {
				t.Fatalf("sequence executed+L was rejected: %v", err)
			}
			requireErrorIs(t, logObserve(t, log, Message{Commit, 0, 4, "d", 0}), ErrSeqOutOfWindow)
			requireErrorIs(t, logObserve(t, log, Message{Commit, 0, 1, "d", 3 * f}), ErrSeqOutOfWindow)
		})
	}
}

func TestStrictlyOrderedExecution(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 2)
			completeSequence(t, log, f, 2, "d2")
			if result := logExecute(t, log); len(result) != 0 {
				t.Fatalf("sequence 2 executed before sequence 1: %#v", result)
			}

			completeSequence(t, log, f, 1, "d1")
			wantExecution(t, logExecute(t, log), []Execution{{1, "d1"}, {2, "d2"}}, 3*f+1)
		})
	}
}

func TestRejectionPriorityAndConflictsDoNotMutate(t *testing.T) {
	log := mustNewLog(t, 1, 0, 1)
	completeSequence(t, log, 1, 1, "d")
	wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 4)

	cases := []struct {
		msg  Message
		want error
	}{
		{Message{PrePrepare, 0, 1, "d", -1}, ErrSenderOutOfRange},
		{Message{Prepare, 0, 1, "", 1}, ErrEmptyDigest},
		{Message{Commit, 1, 1, "d", 0}, ErrWrongView},
		{Message{Commit, 0, 3, "d", 0}, ErrSeqOutOfWindow},
	}
	for _, tc := range cases {
		requireErrorIs(t, logObserve(t, log, tc.msg), tc.want)
	}

	log = mustNewLog(t, 1, 0, 2)
	requireErrorIs(t, logObserve(t, log, Message{PrePrepare, 0, 1, "d", 1}), ErrPrePrepareFromBackup)
	requireErrorIs(t, logObserve(t, log, Message{Prepare, 0, 1, "d", 0}), ErrPrepareFromPrimary)
	requireErrorIs(t, logObserve(t, log, Message{MessageKind(99), 0, 1, "d", 0}), ErrUnknownMessageKind)

	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("conflicts-f=%d", f), func(t *testing.T) {
			log := mustNewLog(t, f, 0, 1)
			if err := logObserve(t, log, Message{PrePrepare, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			requireErrorIs(t, logObserve(t, log, Message{PrePrepare, 0, 1, "other", 0}), ErrPrePrepareDigestClash)

			if err := logObserve(t, log, Message{Prepare, 0, 1, "d", 1}); err != nil {
				t.Fatal(err)
			}
			requireErrorIs(t, logObserve(t, log, Message{Prepare, 0, 1, "other", 1}), ErrPrepareDigestClash)

			if err := logObserve(t, log, Message{Commit, 0, 1, "d", 0}); err != nil {
				t.Fatal(err)
			}
			requireErrorIs(t, logObserve(t, log, Message{Commit, 0, 1, "other", 0}), ErrCommitDigestClash)

			for from := 2; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Prepare, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			for from := 1; from <= 2*f; from++ {
				if err := logObserve(t, log, Message{Commit, 0, 1, "d", from}); err != nil {
					t.Fatal(err)
				}
			}
			wantExecution(t, logExecute(t, log), []Execution{{1, "d"}}, 3*f+1)
		})
	}
}

func TestExecuteReturnIsNotAliased(t *testing.T) {
	log := mustNewLog(t, 1, 0, 1)
	completeSequence(t, log, 1, 1, "d")
	result := logExecute(t, log)
	result[0].Seq = 999
	result[0].Digest = "changed"
	if log.executed != 1 {
		t.Fatalf("internal executed = %d, want 1", log.executed)
	}
}

func TestConcurrentObservesAndExecutions(t *testing.T) {
	log := mustNewLog(t, 1, 0, 4)
	var observeWG sync.WaitGroup
	var executeWG sync.WaitGroup
	var executeMu sync.Mutex
	var executed []Execution
	stop := make(chan struct{})

	for worker := 0; worker < 4; worker++ {
		executeWG.Add(1)
		go func() {
			defer executeWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					result := log.Execute()
					if len(result) > 0 {
						executeMu.Lock()
						executed = append(executed, result...)
						executeMu.Unlock()
					}
				}
			}
		}()
	}

	for seq := int64(1); seq <= 4; seq++ {
		messages := []Message{
			{PrePrepare, 0, seq, "d", 0},
			{Prepare, 0, seq, "d", 2},
			{Prepare, 0, seq, "d", 1},
			{Commit, 0, seq, "d", 0},
			{Commit, 0, seq, "d", 1},
			{Commit, 0, seq, "d", 2},
		}
		for _, msg := range messages {
			observeWG.Add(1)
			go func(msg Message) {
				defer observeWG.Done()
				if err := log.Observe(msg); err != nil {
					t.Errorf("Observe(%#v): %v", msg, err)
				}
			}(msg)
		}
	}

	observeWG.Wait()
	close(stop)
	executeWG.Wait()

	for worker := 0; worker < 8; worker++ {
		executeWG.Add(1)
		go func() {
			defer executeWG.Done()
			result := log.Execute()
			if len(result) > 0 {
				executeMu.Lock()
				executed = append(executed, result...)
				executeMu.Unlock()
			}
		}()
	}

	executeMu.Lock()
	executed = append(executed, log.Execute()...)
	executeMu.Unlock()

	want := []Execution{{1, "d"}, {2, "d"}, {3, "d"}, {4, "d"}}
	if !reflect.DeepEqual(executed, want) {
		t.Fatalf("concurrent execution = %#v, want %#v", executed, want)
	}
}
