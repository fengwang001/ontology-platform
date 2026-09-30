package register

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestFailedCASMustObserveDifferentValue(t *testing.T) {
	c := NewChecker()
	writeID := mustBegin(t, c, "writer", Op{Kind: KindWrite, Value: 1}, 1)
	casID := mustBegin(t, c, "cas", Op{Kind: KindCAS, Expected: 0, New: 2}, 1)
	mustEnd(t, c, casID, 2, &EndResult{CASSucceeded: false})

	result := logCheck(t, c, "failed cas observes pending write")
	if !result.Linearizable {
		t.Fatalf("expected linearizable history: %s", result.Reason)
	}
	if !reflect.DeepEqual(result.Witness, []int{writeID, casID}) {
		t.Fatalf("witness = %v, want [%d %d]", result.Witness, writeID, casID)
	}
}

func TestEqualInvokeAndReturnTimesAreConcurrent(t *testing.T) {
	c := NewChecker()
	writeID := mustBegin(t, c, "writer", Op{Kind: KindWrite, Value: 7}, 1)
	mustEnd(t, c, writeID, 1, nil)
	readID := mustBegin(t, c, "reader", Op{Kind: KindRead}, 1)
	mustEnd(t, c, readID, 2, &EndResult{ReadValue: 7})

	result := logCheck(t, c, "equal return and invoke times are concurrent")
	if !result.Linearizable {
		t.Fatalf("expected equal times to permit reordering: %s", result.Reason)
	}
	if !reflect.DeepEqual(result.Witness, []int{writeID, readID}) {
		t.Fatalf("witness = %v, want [%d %d]", result.Witness, writeID, readID)
	}
}

func TestPendingWriteExplainsRead(t *testing.T) {
	c := NewChecker()
	writeID := mustBegin(t, c, "writer", Op{Kind: KindWrite, Value: 9}, 3)
	readID := mustBegin(t, c, "reader", Op{Kind: KindRead}, 3)
	mustEnd(t, c, readID, 4, &EndResult{ReadValue: 9})

	result := logCheck(t, c, "pending write explains read")
	if !result.Linearizable || !reflect.DeepEqual(result.Witness, []int{writeID, readID}) {
		t.Fatalf("result = %+v, want linearizable with pending write before read", result)
	}
}

func TestPendingCASOnlyAppliesWhenExpectedMatches(t *testing.T) {
	t.Run("matches", func(t *testing.T) {
		c := NewChecker()
		writeID := mustBegin(t, c, "writer", Op{Kind: KindWrite, Value: 1}, 0)
		mustEnd(t, c, writeID, 1, nil)
		casID := mustBegin(t, c, "cas", Op{Kind: KindCAS, Expected: 1, New: 2}, 2)
		readID := mustBegin(t, c, "reader", Op{Kind: KindRead}, 2)
		mustEnd(t, c, readID, 3, &EndResult{ReadValue: 2})

		result := logCheck(t, c, "pending cas matches current value")
		if !result.Linearizable || !reflect.DeepEqual(result.Witness, []int{writeID, casID, readID}) {
			t.Fatalf("result = %+v, want pending successful CAS in witness", result)
		}
	})

	t.Run("does not match", func(t *testing.T) {
		c := NewChecker()
		casID := mustBegin(t, c, "cas", Op{Kind: KindCAS, Expected: 1, New: 2}, 1)
		readID := mustBegin(t, c, "reader", Op{Kind: KindRead}, 1)
		mustEnd(t, c, readID, 2, &EndResult{ReadValue: 2})

		result := logCheck(t, c, "pending cas does not match current value")
		if result.Linearizable {
			t.Fatalf("pending CAS %d was incorrectly applied against value 0: %+v", casID, result)
		}
	})
}

func TestRejectedRequestsDoNotChangeState(t *testing.T) {
	c := NewChecker()
	first := mustBegin(t, c, "client", Op{Kind: KindWrite, Value: 1}, 0)

	if _, err := c.Begin("client", Op{Kind: KindRead}, 1); !errors.Is(err, ErrClientBusy) {
		t.Fatalf("busy begin error = %v, want %v", err, ErrClientBusy)
	}
	mustEnd(t, c, first, 5, nil)

	if _, err := c.Begin("client", Op{Kind: KindRead}, 4); !errors.Is(err, ErrInvalidStartTime) {
		t.Fatalf("early begin error = %v, want %v", err, ErrInvalidStartTime)
	}

	limitChecker := NewChecker()
	for i := 0; i < 20; i++ {
		id := mustBegin(t, limitChecker, fmt.Sprintf("client-%d", i), Op{Kind: KindRead}, i)
		mustEnd(t, limitChecker, id, i, &EndResult{ReadValue: 0})
	}
	if _, err := limitChecker.Begin("extra", Op{Kind: KindRead}, 20); !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("limit error = %v, want %v", err, ErrHistoryLimit)
	}

	if err := c.End(99, 6, nil); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("unknown end error = %v, want %v", err, ErrUnknownOperation)
	}
	if err := c.End(first, 6, nil); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("duplicate end error = %v, want %v", err, ErrAlreadyCompleted)
	}

	early := mustBegin(t, c, "early", Op{Kind: KindWrite, Value: 2}, 5)
	if err := c.End(early, 4, nil); !errors.Is(err, ErrInvalidReturnTime) {
		t.Fatalf("early return error = %v, want %v", err, ErrInvalidReturnTime)
	}

	writeResult := mustBegin(t, c, "write-result", Op{Kind: KindWrite, Value: 3}, 6)
	if err := c.End(writeResult, 7, &EndResult{}); !errors.Is(err, ErrResultMismatch) {
		t.Fatalf("write result error = %v, want %v", err, ErrResultMismatch)
	}

	readNoResult := mustBegin(t, c, "read-no-result", Op{Kind: KindRead}, 6)
	if err := c.End(readNoResult, 7, nil); !errors.Is(err, ErrResultMismatch) {
		t.Fatalf("read missing result error = %v, want %v", err, ErrResultMismatch)
	}

	casNoResult := mustBegin(t, c, "cas-no-result", Op{Kind: KindCAS, Expected: 0, New: 1}, 6)
	if err := c.End(casNoResult, 7, nil); !errors.Is(err, ErrResultMismatch) {
		t.Fatalf("cas missing result error = %v, want %v", err, ErrResultMismatch)
	}

	if got := len(c.Snapshot()); got != 5 {
		t.Fatalf("snapshot length = %d, want rejected requests to leave 5 accepted operations", got)
	}
	if len(limitChecker.Snapshot()) != 20 {
		t.Fatalf("limit checker snapshot length = %d, want 20", len(limitChecker.Snapshot()))
	}
}

func mustBegin(t *testing.T, c *Checker, client string, op Op, invokeTime int) int {
	t.Helper()
	id, err := c.Begin(client, op, invokeTime)
	if err != nil {
		t.Fatalf("Begin(%s, %+v, %d): %v", client, op, invokeTime, err)
	}
	return id
}

func mustEnd(t *testing.T, c *Checker, id, returnTime int, result *EndResult) {
	t.Helper()
	if err := c.End(id, returnTime, result); err != nil {
		t.Fatalf("End(%d, %d, %+v): %v", id, returnTime, result, err)
	}
}

func logCheck(t *testing.T, c *Checker, name string) CheckResult {
	t.Helper()
	snapshot := c.Snapshot()
	result := c.Check()
	t.Logf("case=%s\ninput=%v\noutput=%+v", name, snapshot, result)
	return result
}
