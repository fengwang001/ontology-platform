package ontology

import (
	"errors"
	"os"
	"testing"
)

func newLoggedBank(t *testing.T) (*Bank, *LogRecorder) {
	t.Helper()
	recorder := NewLogRecorder(NewStandardLogger(os.Stdout))
	bank := NewBank()
	bank.SetLogger(recorder)
	return bank, recorder
}

func mustCreateAccount(t *testing.T, bank *Bank, id string, initial, lower, upper, limit int64) {
	t.Helper()
	if err := bank.CreateAccount(id, initial, lower, upper, limit); err != nil {
		t.Fatalf("CreateAccount(%q) error = %v", id, err)
	}
}

func mustBegin(t *testing.T, bank *Bank, id int64) {
	t.Helper()
	if err := bank.BeginTransaction(id); err != nil {
		t.Fatalf("BeginTransaction(%d) error = %v", id, err)
	}
}

func requireError(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func TestIndependentPositiveAndNegativeSides(t *testing.T) {
	bank, logs := newLoggedBank(t)
	mustCreateAccount(t, bank, "cash", 50, 0, 100, 10)
	mustBegin(t, bank, 1)

	if err := bank.Reserve(1, "cash", -50); err != nil {
		t.Fatalf("Reserve(-50) error = %v", err)
	}
	if err := bank.Reserve(1, "cash", 50); err != nil {
		t.Fatalf("Reserve(+50) error = %v", err)
	}
	requireError(t, bank.Reserve(1, "cash", -1), ErrLowerBoundInsufficient)

	result, err := bank.Read("cash")
	if err != nil {
		t.Fatalf("Read error = %v", err)
	}
	if result.Certain || result.Lower != 0 || result.Upper != 100 {
		t.Fatalf("Read = %+v, want uncertain [0,100]", result)
	}

	if err := bank.Abort(1); err != nil {
		t.Fatalf("Abort error = %v", err)
	}
	result, err = bank.Read("cash")
	if err != nil {
		t.Fatalf("Read after abort error = %v", err)
	}
	if !result.Certain || result.Balance != 50 {
		t.Fatalf("Read after abort = %+v, want certain 50", result)
	}

	lines := logs.Lines()
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("日志中没有输入、输出和判定依据")
	}
}

func TestBoundsCheckOnlyChangedSide(t *testing.T) {
	bank, _ := newLoggedBank(t)
	mustCreateAccount(t, bank, "positive-side", 90, 0, 100, 10)
	mustBegin(t, bank, 1)
	if err := bank.Reserve(1, "positive-side", -90); err != nil {
		t.Fatalf("Reserve(-90) error = %v", err)
	}
	if err := bank.Reserve(1, "positive-side", 10); err != nil {
		t.Fatalf("正增量到达上界不应检查另一侧: %v", err)
	}

	mustCreateAccount(t, bank, "negative-side", 10, 0, 100, 10)
	mustBegin(t, bank, 2)
	if err := bank.Reserve(2, "negative-side", 90); err != nil {
		t.Fatalf("Reserve(+90) error = %v", err)
	}
	if err := bank.Reserve(2, "negative-side", -10); err != nil {
		t.Fatalf("负增量到达下界不应检查另一侧: %v", err)
	}
}

func TestPendingLimitExact(t *testing.T) {
	bank, _ := newLoggedBank(t)
	mustCreateAccount(t, bank, "cash", 50, 0, 100, 2)
	mustBegin(t, bank, 1)

	if err := bank.Reserve(1, "cash", 1); err != nil {
		t.Fatalf("first Reserve error = %v", err)
	}
	if err := bank.Reserve(1, "cash", 1); err != nil {
		t.Fatalf("second Reserve at limit error = %v", err)
	}
	requireError(t, bank.Reserve(1, "cash", 1), ErrPendingLimitReached)

	result, err := bank.Read("cash")
	if err != nil {
		t.Fatalf("Read error = %v", err)
	}
	if result.Lower != 50 || result.Upper != 52 {
		t.Fatalf("range = [%d,%d], want [50,52]", result.Lower, result.Upper)
	}
}

func TestValidationOrderAndRejectedOperationHasNoEffect(t *testing.T) {
	bank, _ := newLoggedBank(t)
	mustCreateAccount(t, bank, "cash", 0, 0, 10, 1)
	mustBegin(t, bank, 1)
	mustBegin(t, bank, 2)
	if err := bank.Reserve(1, "cash", 1); err != nil {
		t.Fatalf("fill pending slot: %v", err)
	}
	if err := bank.Commit(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	requireError(t, bank.Reserve(1, "missing", 0), ErrInvalidTransaction)
	requireError(t, bank.Reserve(2, "missing", 1), ErrAccountNotFound)
	requireError(t, bank.Reserve(2, "cash", 0), ErrZeroDelta)
	mustCreateAccount(t, bank, "full", 0, 0, 0, 1)
	mustBegin(t, bank, 3)
	if err := bank.Reserve(3, "full", 0+0); err == nil {
		t.Fatal("zero delta must be rejected before pending limit")
	}

	requireError(t, bank.Commit(404), ErrInvalidTransaction)
	requireError(t, bank.Commit(2), ErrNoReservations)

	result, err := bank.Read("cash")
	if err != nil || !result.Certain || result.Balance != 1 {
		t.Fatalf("cash after rejected operations = %+v, %v", result, err)
	}
}

func TestCrossAccountCommitAndAbort(t *testing.T) {
	bank, _ := newLoggedBank(t)
	mustCreateAccount(t, bank, "a", 10, 0, 100, 10)
	mustCreateAccount(t, bank, "b", 20, 0, 100, 10)
	mustBegin(t, bank, 1)
	if err := bank.Reserve(1, "a", 5); err != nil {
		t.Fatalf("Reserve a: %v", err)
	}
	if err := bank.Reserve(1, "b", -7); err != nil {
		t.Fatalf("Reserve b: %v", err)
	}
	if err := bank.Commit(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	a, err := bank.Read("a")
	if err != nil || a.Balance != 15 {
		t.Fatalf("a = %+v, %v", a, err)
	}
	b, err := bank.Read("b")
	if err != nil || b.Balance != 13 {
		t.Fatalf("b = %+v, %v", b, err)
	}
}
