package qc

import (
	"errors"
	"testing"
)

func TestRecoveryCalibrationAndValidity(t *testing.T) {
	system := NewSystem()
	registerTestAssay(t, system, "workflow")
	rejectAt := int64(2)
	runTestQC(t, system, rejectAt, "workflow", 131, 50)
	assertStatus(t, system, "workflow", StatusOutOfControl)

	if err := system.IssueReport(3, "analyzer", "workflow", "blocked"); !errorIs(err, ErrAssayOutOfControl) {
		t.Fatalf("issue during out-of-control state: %v", err)
	}

	runTestQC(t, system, 4, "workflow", 121, 50)
	assertStatus(t, system, "workflow", StatusOutOfControl)
	runTestQC(t, system, 5, "workflow", 131, 50)
	assertStatus(t, system, "workflow", StatusOutOfControl)
	runTestQC(t, system, 6, "workflow", 100, 50)
	assertStatus(t, system, "workflow", StatusOutOfControl)
	runTestQC(t, system, 7, "workflow", 100, 50)
	assertStatus(t, system, "workflow", StatusInControl)

	if err := system.IssueReport(17, "analyzer", "workflow", "exact-validity"); err != nil {
		t.Fatalf("exact validity must issue: %v", err)
	}
	if err := system.IssueReport(18, "analyzer", "workflow", "expired"); !errorIs(err, ErrQCExpired) {
		t.Fatalf("one second beyond validity: %v", err)
	}

	runTestQC(t, system, 19, "workflow", 131, 50)
	if err := system.Calibrate(20, "analyzer", "workflow"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, system, "workflow", StatusInControl)
	if err := system.IssueReport(20, "analyzer", "workflow", "after-calibration"); !errorIs(err, ErrNeverControlled) {
		t.Fatalf("calibration must not count as quality control: %v", err)
	}
}

func TestRetrospectiveReportMarkingBoundaries(t *testing.T) {
	system := NewSystem()
	registerTestAssay(t, system, "trace")
	runTestQC(t, system, 10, "trace", 100, 50)

	mustIssue(t, system, 10, "trace", "at-non-ooc")
	mustIssue(t, system, 11, "trace", "after-non-ooc-1")
	mustIssue(t, system, 12, "trace", "after-non-ooc-2")
	runTestQC(t, system, 12, "trace", 131, 50)

	assertReportStatus(t, system, "at-non-ooc", ReportIssued)
	assertReportStatus(t, system, "after-non-ooc-1", ReportPending)
	assertReportStatus(t, system, "after-non-ooc-2", ReportPending)

	if err := system.ReviewReport(14, "after-non-ooc-1"); err != nil {
		t.Fatal(err)
	}
	assertReportStatus(t, system, "after-non-ooc-1", ReportReviewed)
	if err := system.ReviewReport(15, "at-non-ooc"); !errorIs(err, ErrStatusMismatch) {
		t.Fatalf("reviewing issued report: %v", err)
	}
}

func TestRetrospectiveMarkingWithoutPriorNonOutOfControlRun(t *testing.T) {
	system := NewSystem()
	config := AssayConfig{
		Low:      LevelConfig{Target: 0, SD: 100},
		High:     LevelConfig{Target: 0, SD: 100},
		ValidFor: 1000,
	}
	if err := system.RegisterAssay(1, "analyzer", "no-prior", config); err != nil {
		t.Fatal(err)
	}
	if err := system.IssueReport(2, "analyzer", "no-prior", "never-1"); !errorIs(err, ErrNeverControlled) {
		t.Fatal(err)
	}

	config.ValidFor = 1
	if err := system.RegisterAssay(2, "analyzer", "no-prior-all", config); err != nil {
		t.Fatal(err)
	}
	runTestQC(t, system, 2, "no-prior-all", 0, 0)
	mustIssue(t, system, 2, "no-prior-all", "all-1")
	mustIssue(t, system, 3, "no-prior-all", "all-2")
	if err := system.Calibrate(4, "analyzer", "no-prior-all"); err != nil {
		t.Fatal(err)
	}
	runTestQC(t, system, 5, "no-prior-all", 301, 0)
	assertReportStatus(t, system, "all-1", ReportPending)
	assertReportStatus(t, system, "all-2", ReportPending)
}

func TestErrorPriority(t *testing.T) {
	system := NewSystem()
	registerTestAssay(t, system, "errors")
	runTestQC(t, system, 1, "errors", 131, 50)

	if err := system.IssueReport(0, "", "errors", "bad"); !errorIs(err, ErrInvalidParameter) {
		t.Fatalf("got %v", err)
	}
	if err := system.IssueReport(0, "analyzer", "errors", "clock"); !errorIs(err, ErrClockRewound) {
		t.Fatalf("got %v", err)
	}
	if err := system.IssueReport(2, "analyzer", "missing", "not-found"); !errorIs(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := system.IssueReport(2, "analyzer", "errors", "out-of-control"); !errorIs(err, ErrAssayOutOfControl) {
		t.Fatalf("got %v", err)
	}

	if err := system.Calibrate(2, "analyzer", "errors"); err != nil {
		t.Fatal(err)
	}
	if err := system.IssueReport(2, "analyzer", "errors", "never"); !errorIs(err, ErrNeverControlled) {
		t.Fatalf("got %v", err)
	}
	runTestQC(t, system, 2, "errors", 100, 50)
	if err := system.IssueReport(2, "analyzer", "errors", "current"); err != nil {
		t.Fatalf("valid report: %v", err)
	}
	if err := system.IssueReport(13, "analyzer", "errors", "expired"); !errorIs(err, ErrQCExpired) {
		t.Fatalf("got %v", err)
	}
	if err := system.ReviewReport(13, "current"); !errorIs(err, ErrStatusMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestRejectedOperationsDoNotChangeClockOrState(t *testing.T) {
	system := NewSystem()
	registerTestAssay(t, system, "reject")
	runTestQC(t, system, 5, "reject", 100, 50)
	_, beforeErr := system.LastRun("analyzer", "reject")
	if beforeErr != nil {
		t.Fatal(beforeErr)
	}
	_, err := system.RunQC(4, "analyzer", "reject", 131, 50)
	if !errorIs(err, ErrClockRewound) {
		t.Fatalf("got %v", err)
	}
	record, err := system.LastRun("analyzer", "reject")
	if err != nil || record.Time != 5 || record.Outcome != OutcomeNormal {
		t.Fatalf("rejected run changed state: %+v %v", record, err)
	}
}

func assertStatus(t *testing.T, system *System, assay string, want AssayStatus) {
	t.Helper()
	got, _, _, err := system.AssaySnapshot("analyzer", assay)
	if err != nil || got != want {
		t.Fatalf("status = %s, %v; want %s", got, err, want)
	}
}

func mustIssue(t *testing.T, system *System, now int64, assay, reportID string) {
	t.Helper()
	if err := system.IssueReport(now, "analyzer", assay, reportID); err != nil {
		t.Fatalf("issue %s: %v", reportID, err)
	}
}

func assertReportStatus(t *testing.T, system *System, reportID string, want ReportStatus) {
	t.Helper()
	got, err := system.ReportStatus(reportID)
	if err != nil || got != want {
		t.Fatalf("report %s = %s, %v; want %s", reportID, got, err, want)
	}
}

func errorIs(err error, code ErrorCode) bool {
	var typed Error
	return errors.As(err, &typed) && typed.Code == code
}
