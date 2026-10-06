package aml

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func testSystem(t testing.TB) *System {
	t.Helper()
	system, err := NewSystem(Config{L: 100, H: 300, K: 3, D: 5})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	return system
}

func mustOpen(t testing.TB, system *System, now int64, accountID string) {
	t.Helper()
	if err := system.OpenAccount(now, accountID); err != nil {
		t.Fatalf("OpenAccount(%q) error = %v", accountID, err)
	}
}

func makeDeposit(t testing.TB, system *System, now int64, accountID, txID string, amount int64) *Report {
	t.Helper()
	report, err := system.Deposit(now, accountID, txID, amount)
	if err != nil {
		t.Fatalf("Deposit(%q, %q, %d) error = %v", accountID, txID, amount, err)
	}
	return report
}

func TestConfigValidation(t *testing.T) {
	tests := []Config{
		{L: 0, H: 10, K: 2, D: 1},
		{L: -1, H: 10, K: 2, D: 1},
		{L: 10, H: 10, K: 2, D: 1},
		{L: 11, H: 10, K: 2, D: 1},
		{L: 1, H: 10, K: 1, D: 1},
		{L: 1, H: 10, K: 2, D: 0},
	}
	for _, cfg := range tests {
		if _, err := NewSystem(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewSystem(%+v) error = %v, want %v", cfg, err, ErrInvalidArgument)
		}
	}
}

func TestLargeDepositAtThresholdIsIndependent(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 1, "a")

	report := makeDeposit(t, system, 1, "a", "large", 300)
	if report.Kind != LargeReport || report.Total != 300 || !reflect.DeepEqual(report.TransactionIDs, []string{"large"}) {
		t.Fatalf("large report = %+v", report)
	}

	summary, err := system.WindowSummary(1, "a")
	if err != nil {
		t.Fatalf("WindowSummary() error = %v", err)
	}
	if summary != (WindowSummary{}) {
		t.Fatalf("summary = %+v, want empty", summary)
	}
}

func TestSmallDepositAtThresholdAndStructuredExactlyThreshold(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 10, "a")

	makeDeposit(t, system, 10, "a", "below", 99)
	if got := makeDeposit(t, system, 10, "a", "first", 100); got != nil {
		t.Fatalf("first structured report = %+v, want nil", got)
	}
	if got := makeDeposit(t, system, 10, "a", "second", 100); got != nil {
		t.Fatalf("second structured report = %+v, want nil", got)
	}
	report := makeDeposit(t, system, 10, "a", "third", 100)
	if report == nil || report.Kind != StructuredReport || report.Total != 300 || report.Number != 1 {
		t.Fatalf("third report = %+v", report)
	}
	wantIDs := []string{"first", "second", "third"}
	if !reflect.DeepEqual(report.TransactionIDs, wantIDs) {
		t.Fatalf("transactions = %v, want %v", report.TransactionIDs, wantIDs)
	}

	summary, _ := system.WindowSummary(10, "a")
	if summary.Count != 3 || summary.Total != 300 {
		t.Fatalf("summary = %+v, want count=3 total=300", summary)
	}
}

func TestThresholdsDifferByOne(t *testing.T) {
	system, err := NewSystem(Config{L: 99, H: 300, K: 3, D: 5})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	mustOpen(t, system, 1, "a")
	makeDeposit(t, system, 1, "a", "x1", 100)
	makeDeposit(t, system, 1, "a", "x2", 100)
	if got := makeDeposit(t, system, 1, "a", "x3", 98); got != nil {
		t.Fatalf("amount below L produced report %+v", got)
	}
	summary, _ := system.WindowSummary(1, "a")
	if summary.Count != 2 || summary.Total != 200 {
		t.Fatalf("summary = %+v, want two eligible deposits", summary)
	}

	if err := system.Reverse(1, "x3"); err != nil {
		t.Fatalf("Reverse(below-L) error = %v", err)
	}
	if got := makeDeposit(t, system, 1, "a", "x4", 99); got != nil {
		t.Fatalf("three deposits totaling 299 produced report %+v", got)
	}
	summary, _ = system.WindowSummary(1, "a")
	if summary.Count != 3 || summary.Total != 299 {
		t.Fatalf("summary = %+v, want count=3 total=299", summary)
	}
	reportsBefore, _ := system.Reports(1)
	if len(reportsBefore) != 0 {
		t.Fatalf("reports below total threshold = %d, want 0", len(reportsBefore))
	}

	if got := makeDeposit(t, system, 1, "a", "ignored", 1); got != nil {
		t.Fatalf("amount below L produced report %+v", got)
	}
	summary, _ = system.WindowSummary(1, "a")
	if summary.Count != 3 || summary.Total != 299 {
		t.Fatalf("summary after below-L deposit = %+v, want unchanged", summary)
	}

	if err := system.Reverse(1, "x4"); err != nil {
		t.Fatalf("Reverse(x4) error = %v", err)
	}
	report := makeDeposit(t, system, 1, "a", "x5", 100)
	if report == nil || report.Total != 300 || len(report.TransactionIDs) != 3 {
		t.Fatalf("threshold report = %+v, want structured report", report)
	}
}

func TestWindowBoundary(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 0, "a")
	makeDeposit(t, system, 2, "a", "old", 100)
	makeDeposit(t, system, 5, "a", "middle", 100)
	makeDeposit(t, system, 6, "a", "new", 100)

	summaryAt6, _ := system.WindowSummary(6, "a")
	if summaryAt6.Count != 3 || summaryAt6.Total != 300 {
		t.Fatalf("summary at 6 = %+v, want all three", summaryAt6)
	}
	summaryAt7, _ := system.WindowSummary(7, "a")
	if summaryAt7.Count != 2 || summaryAt7.Total != 200 {
		t.Fatalf("summary at 7 = %+v, want boundary day 3..7", summaryAt7)
	}
}

func TestNegativeIntegerDays(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, -6, "a")
	makeDeposit(t, system, -5, "a", "old", 100)
	makeDeposit(t, system, -2, "a", "boundary", 100)
	makeDeposit(t, system, -1, "a", "new", 100)

	summaryAtMinus1, err := system.WindowSummary(-1, "a")
	if err != nil {
		t.Fatalf("WindowSummary(-1) error = %v", err)
	}
	if summaryAtMinus1.Count != 3 || summaryAtMinus1.Total != 300 {
		t.Fatalf("summary at -1 = %+v, want all three", summaryAtMinus1)
	}
	summaryAt0, err := system.WindowSummary(0, "a")
	if err != nil {
		t.Fatalf("WindowSummary(0) error = %v", err)
	}
	if summaryAt0.Count != 2 || summaryAt0.Total != 200 {
		t.Fatalf("summary at 0 = %+v, want negative window boundary", summaryAt0)
	}
}

func TestLinkTriggersStructuredReport(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 10, "a")
	mustOpen(t, system, 10, "b")
	makeDeposit(t, system, 10, "a", "a1", 100)
	makeDeposit(t, system, 10, "a", "a2", 100)
	makeDeposit(t, system, 10, "b", "b1", 100)

	report, err := system.Link(10, "a", "b")
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if report == nil || report.Kind != StructuredReport || report.Trigger.Kind != LinkOperation {
		t.Fatalf("link report = %+v", report)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(report.Accounts, want) {
		t.Fatalf("accounts = %v, want %v", report.Accounts, want)
	}
	if want := []string{"a1", "a2", "b1"}; !reflect.DeepEqual(report.TransactionIDs, want) {
		t.Fatalf("transactions = %v, want %v", report.TransactionIDs, want)
	}

	if _, err := system.Link(10, "b", "a"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second Link() error = %v, want %v", err, ErrAlreadyLinked)
	}
}

func TestReverseThenNewDepositReportsAgain(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 1, "a")
	makeDeposit(t, system, 1, "a", "x1", 100)
	makeDeposit(t, system, 1, "a", "x2", 100)
	first := makeDeposit(t, system, 1, "a", "x3", 100)
	if first == nil {
		t.Fatal("missing first structured report")
	}

	if err := system.Reverse(1, "x3"); err != nil {
		t.Fatalf("Reverse() error = %v", err)
	}
	reports, _ := system.Reports(1)
	if len(reports) != 1 {
		t.Fatalf("reports after reversal = %d, report must not be withdrawn", len(reports))
	}
	report := makeDeposit(t, system, 1, "a", "x4", 100)
	if report == nil || report.Number != 2 {
		t.Fatalf("new uncovered deposit after reversal report = %+v", report)
	}
	wantIDs := []string{"x1", "x2", "x4"}
	if !reflect.DeepEqual(report.TransactionIDs, wantIDs) {
		t.Fatalf("second report transactions = %v, want %v", report.TransactionIDs, wantIDs)
	}
	large := makeDeposit(t, system, 1, "a", "large-after-covered", 300)
	if large.Kind != LargeReport {
		t.Fatalf("large deposit report kind = %s, want large", large.Kind)
	}
	summary, _ := system.WindowSummary(1, "a")
	if summary.Count != 3 || summary.Total != 300 {
		t.Fatalf("summary after large deposit = %+v, want structured set unchanged", summary)
	}
}

func TestReverseErrors(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 1, "a")
	makeDeposit(t, system, 1, "a", "x1", 100)

	if err := system.Reverse(1, "missing"); !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("Reverse(missing) error = %v, want %v", err, ErrTransactionNotFound)
	}
	if err := system.Reverse(1, "x1"); err != nil {
		t.Fatalf("first Reverse() error = %v", err)
	}
	if err := system.Reverse(1, "x1"); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("second Reverse() error = %v, want %v", err, ErrAlreadyReversed)
	}
}

func TestRejectedOperationsLeaveNoState(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 10, "a")

	if err := system.OpenAccount(9, "late"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("OpenAccount rollback error = %v", err)
	}
	if _, err := system.Deposit(10, "missing", "m1", 100); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("Deposit missing account error = %v", err)
	}
	if _, err := system.Deposit(10, "a", "dup", 300); err != nil {
		t.Fatalf("accepted large deposit: %v", err)
	}
	if _, err := system.Deposit(10, "a", "dup", 100); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("duplicate deposit error = %v", err)
	}
	if _, err := system.Link(9, "a", "missing"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Link priority error = %v", err)
	}

	reports, err := system.Reports(10)
	if err != nil {
		t.Fatalf("Reports() error = %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want only accepted large report", len(reports))
	}
	summary, _ := system.WindowSummary(10, "a")
	if summary != (WindowSummary{}) {
		t.Fatalf("summary = %+v, rejected small deposit left state", summary)
	}
}

func TestConcurrentOperationsEquivalentToSerializedSequence(t *testing.T) {
	system := testSystem(t)
	mustOpen(t, system, 0, "a")

	const goroutines = 16
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txID := "p" + string(rune('a'+i))
			_, _ = system.Deposit(1, "a", txID, 100)
		}(i)
	}
	wg.Wait()

	summary, err := system.WindowSummary(1, "a")
	if err != nil {
		t.Fatalf("WindowSummary() error = %v", err)
	}
	if summary.Count != goroutines {
		t.Fatalf("count = %d, want %d", summary.Count, goroutines)
	}
}
