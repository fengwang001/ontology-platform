package qc

import (
	"errors"
	"sync"
	"testing"
)

func testSpec() AssaySpec {
	return AssaySpec{
		InstrumentID: "instrument",
		AssayID:      "assay",
		Low:          LevelSpec{Target: 0, StandardDeviation: 10},
		High:         LevelSpec{Target: 0, StandardDeviation: 10},
		Validity:     10,
	}
}

type fataler interface {
	Helper()
	Fatalf(string, ...any)
}

func runAt(t fataler, system *System, now int64, low, high int64) RunResult {
	t.Helper()
	result, err := system.SubmitRun(now, RunInput{InstrumentID: "instrument", AssayID: "assay", LowValue: low, HighValue: high})
	if err != nil {
		t.Fatalf("run at %d (%d,%d): %v", now, low, high, err)
	}
	return result
}

func assertRules(t *testing.T, result RunResult, want []int) {
	t.Helper()
	if len(result.Rules) != len(want) {
		t.Fatalf("rules = %v, want %v", result.Rules, want)
	}
	for index := range want {
		if result.Rules[index] != want[index] {
			t.Fatalf("rules = %v, want %v", result.Rules, want)
		}
	}
}

func TestRuleBoundariesAndOrdering(t *testing.T) {
	t.Run("rule one strict three sigma", func(t *testing.T) {
		system := NewSystem()
		if err := system.RegisterAssay(0, testSpec()); err != nil {
			t.Fatal(err)
		}
		assertRules(t, runAt(t, system, 1, 20, 0), nil)
		if result := runAt(t, system, 2, 0, 0); result.Warning {
			t.Fatalf("normal point must not be warning: %+v", result)
		}
		assertRules(t, runAt(t, system, 3, 31, 0), []int{1})
		_ = system.Calibrate(4, "instrument", "assay")
		assertRules(t, runAt(t, system, 5, -30, 0), nil)
	})

	t.Run("rule two strict two sigma same side", func(t *testing.T) {
		system := NewSystem()
		_ = system.RegisterAssay(0, testSpec())
		assertRules(t, runAt(t, system, 1, 21, 0), nil)
		assertRules(t, runAt(t, system, 2, 21, 0), []int{2})
		_ = system.Calibrate(3, "instrument", "assay")
		assertRules(t, runAt(t, system, 4, 21, 0), nil)
		assertRules(t, runAt(t, system, 5, -21, 0), nil)
		assertRules(t, runAt(t, system, 6, -21, 0), []int{2})
	})

	t.Run("rule three opposite levels", func(t *testing.T) {
		system := NewSystem()
		_ = system.RegisterAssay(0, testSpec())
		assertRules(t, runAt(t, system, 1, 20, -20), nil)
		assertRules(t, runAt(t, system, 2, 21, -21), []int{3})
	})

	t.Run("rule four zero and side interruption", func(t *testing.T) {
		system := NewSystem()
		_ = system.RegisterAssay(0, testSpec())
		runAt(t, system, 1, 11, 0)
		runAt(t, system, 2, 11, 0)
		runAt(t, system, 3, 11, 0)
		assertRules(t, runAt(t, system, 4, 11, 0), []int{4})
		_ = system.Calibrate(5, "instrument", "assay")
		runAt(t, system, 6, 11, 0)
		runAt(t, system, 7, 11, 0)
		runAt(t, system, 8, 11, 0)
		assertRules(t, runAt(t, system, 9, 11, 0), []int{4})
		runAt(t, system, 15, 0, 0)
		runAt(t, system, 16, 1, 0)
		runAt(t, system, 17, 1, 0)
		runAt(t, system, 18, 1, 0)
		assertRules(t, runAt(t, system, 19, 1, 0), nil)
	})

	t.Run("rule five ten same side", func(t *testing.T) {
		system := NewSystem()
		_ = system.RegisterAssay(0, testSpec())
		for index := 0; index < 9; index++ {
			assertRules(t, runAt(t, system, int64(index+1), 1, 0), nil)
		}
		assertRules(t, runAt(t, system, 10, 1, 0), []int{5})
	})

	t.Run("multiple rules ordered", func(t *testing.T) {
		system := NewSystem()
		_ = system.RegisterAssay(0, testSpec())
		runAt(t, system, 1, 11, 1)
		runAt(t, system, 2, 11, 1)
		runAt(t, system, 3, 11, -21)
		assertRules(t, runAt(t, system, 4, 31, -31), []int{1, 2, 3, 4})
	})
}

func TestWarningRecoveryAndCalibration(t *testing.T) {
	system := NewSystem()
	_ = system.RegisterAssay(0, testSpec())

	normal := runAt(t, system, 1, 10, -10)
	if normal.Warning || normal.Outage {
		t.Fatalf("equal 1sd should be normal: %+v", normal)
	}
	warning := runAt(t, system, 2, 20, -21)
	if !warning.Warning || warning.Outage {
		t.Fatalf("one level over 2sd should warn: %+v", warning)
	}
	failure := runAt(t, system, 3, 31, 0)
	if !failure.Outage {
		t.Fatal("expected outage")
	}
	if err := system.IssueReport(4, "blocked", "instrument", "assay"); !errors.Is(err, ErrOutOfControl) {
		t.Fatalf("issue during outage = %v", err)
	}

	if result := runAt(t, system, 5, 0, 0); !result.Outage || result.Recovered {
		t.Fatalf("first normal should not recover: %+v", result)
	}
	if result := runAt(t, system, 6, 21, 0); !result.Warning || result.Recovered {
		t.Fatalf("warning should reset recovery: %+v", result)
	}
	runAt(t, system, 7, 0, 0)
	recovered := runAt(t, system, 8, 0, 0)
	if !recovered.Recovered || recovered.Outage {
		t.Fatalf("second consecutive normal should recover: %+v", recovered)
	}

	for index := 0; index < 3; index++ {
		runAt(t, system, int64(9+index), 11, 0)
	}
	if err := system.Calibrate(12, "instrument", "assay"); err != nil {
		t.Fatal(err)
	}
	result := runAt(t, system, 13, 11, 0)
	assertRules(t, result, nil)
	if result.Warning || result.Outage {
		t.Fatalf("calibration should clear sequence and control state: %+v", result)
	}
}

func TestValidityAndRetrospectiveReviewEndpoints(t *testing.T) {
	system := NewSystem()
	_ = system.RegisterAssay(0, testSpec())
	runAt(t, system, 10, 0, 0)
	if err := system.IssueReport(10, "at-boundary", "instrument", "assay"); err != nil {
		t.Fatalf("issue at same time: %v", err)
	}
	if err := system.IssueReport(11, "after-boundary", "instrument", "assay"); err != nil {
		t.Fatalf("issue one second later: %v", err)
	}
	if err := system.IssueReport(20, "at-expiry", "instrument", "assay"); err != nil {
		t.Fatalf("issue at exact validity: %v", err)
	}
	if err := system.IssueReport(21, "expired", "instrument", "assay"); !errors.Is(err, ErrQcExpired) {
		t.Fatalf("issue after validity = %v", err)
	}

	runAt(t, system, 22, 31, 0)
	if status, _ := system.ReportStatus("at-boundary"); status != ReportIssued {
		t.Fatalf("same-time report = %s", status)
	}
	if status, _ := system.ReportStatus("after-boundary"); status != ReportPending {
		t.Fatalf("later report = %s", status)
	}
	if status, _ := system.ReportStatus("at-expiry"); status != ReportPending {
		t.Fatalf("at-expiry report = %s", status)
	}
	if _, err := system.ReviewReport(23, "at-boundary"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("review issued report = %v", err)
	}
	if _, err := system.ReviewReport(24, "after-boundary"); err != nil {
		t.Fatalf("review pending: %v", err)
	}
	if status, _ := system.ReportStatus("after-boundary"); status != ReportReviewed {
		t.Fatalf("reviewed report = %s", status)
	}
}

func TestRetrospectiveMarksAllReportsBeforeFirstNonFailure(t *testing.T) {
	system := NewSystem()
	_ = system.RegisterAssay(0, testSpec())
	failureOnly := RunInput{InstrumentID: "instrument", AssayID: "assay", LowValue: 31, HighValue: 0}
	_, _ = system.SubmitRun(1, failureOnly)

	normal := RunInput{InstrumentID: "instrument", AssayID: "assay", LowValue: 0, HighValue: 0}
	_ = system.Calibrate(2, "instrument", "assay")
	_, _ = system.SubmitRun(3, normal)
	if err := system.IssueReport(4, "all-one", "instrument", "assay"); err != nil {
		t.Fatalf("issue after calibration normal: %v", err)
	}
	if err := system.IssueReport(5, "all-two", "instrument", "assay"); err != nil {
		t.Fatalf("issue second report: %v", err)
	}
	_, _ = system.SubmitRun(6, failureOnly)
	_ = system.Calibrate(7, "instrument", "assay")
	_, _ = system.SubmitRun(8, failureOnly)

	if status, _ := system.ReportStatus("all-one"); status != ReportPending {
		t.Fatalf("all-one = %s, want pending_review", status)
	}
	if status, _ := system.ReportStatus("all-two"); status != ReportPending {
		t.Fatalf("all-two = %s, want pending_review", status)
	}
}

func TestIssueBeforeAnyRun(t *testing.T) {
	system := NewSystem()
	_ = system.RegisterAssay(0, testSpec())
	if err := system.IssueReport(1, "never-run", "instrument", "assay"); !errors.Is(err, ErrNeverRun) {
		t.Fatalf("issue after calibration and normal run: %v", err)
	}
	if _, err := system.ReportStatus("never-run"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected report was recorded: %v", err)
	}
}

func TestErrorPriorityAndRollbackAtomicity(t *testing.T) {
	system := NewSystem()
	_ = system.RegisterAssay(5, testSpec())
	_, err := system.SubmitRun(4, RunInput{InstrumentID: "", AssayID: ""})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("priority = %v", err)
	}
	_, err = system.SubmitRun(4, RunInput{InstrumentID: "missing", AssayID: "missing"})
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock before not found = %v", err)
	}
	_, err = system.SubmitRun(5, RunInput{InstrumentID: "missing", AssayID: "missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found = %v", err)
	}
	if _, err := system.ReportStatus("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing report = %v", err)
	}
	result := runAt(t, system, 6, 0, 0)
	if result.Time != 6 {
		t.Fatal("accepted run did not advance clock")
	}
	if _, err := system.SubmitRun(6, RunInput{InstrumentID: "", AssayID: ""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid equal-time operation = %v", err)
	}
}

func TestConcurrentNormalRunsEquivalentToSerialOrder(t *testing.T) {
	system := NewSystem()
	_ = system.RegisterAssay(0, testSpec())
	const count = 100
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := system.SubmitRun(1, RunInput{InstrumentID: "instrument", AssayID: "assay", LowValue: 0, HighValue: 0})
			if err != nil {
				t.Errorf("concurrent run: %v", err)
			}
		}()
	}
	wait.Wait()
}
