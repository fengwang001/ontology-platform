package approval

import "testing"

func TestErrorPriority(t *testing.T) {
	service := newFlowService(t, false, nil)

	if err := service.Pass(StageCommand{}); err != ErrInvalidArgument {
		t.Fatalf("invalid args = %v", err)
	}
	if err := service.Pass(StageCommand{LicenseID: "missing", StageID: "auto", Actor: "dept-auto", Day: 0}); err != ErrInvalidArgument {
		t.Fatalf("day zero beats missing license = %v", err)
	}
	if err := service.Pass(StageCommand{LicenseID: "missing", StageID: "auto", Actor: "dept-auto", Day: 1}); err != ErrNotFound {
		t.Fatalf("missing license = %v", err)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "missing", Actor: "dept-auto", Day: 1}); err != ErrNotFound {
		t.Fatalf("missing stage = %v", err)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "stranger", Day: 1}); err != ErrForbidden {
		t.Fatalf("forbidden = %v", err)
	}
	if err := service.RequestCorrection(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1}); err != ErrInvalidState {
		t.Fatalf("prerequisite = %v", err)
	}
	if err := service.SubmitCorrection(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestCorrection(StageCommand{LicenseID: "L", StageID: "auto", Actor: "dept-auto", Day: 1}); err != ErrCorrectionLimit {
		t.Fatalf("correction limit = %v", err)
	}

	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "dept-auto", Day: 0}); err != ErrInvalidArgument {
		t.Fatalf("clock invalid before clock fallback = %v", err)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "stranger", Day: 0}); err != ErrInvalidArgument {
		t.Fatalf("invalid beats forbidden = %v", err)
	}
	if err := service.Withdraw(WithdrawRequest{LicenseID: "L", Actor: "applicant", Day: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "dept-auto", Day: 1}); err != ErrInvalidState {
		t.Fatalf("terminal = %v", err)
	}

	state := service.licenses["L"]
	if got := len(state.events); got != 5 {
		t.Fatalf("rejected commands changed events: %d", got)
	}
}

func TestClockMovedBackPriority(t *testing.T) {
	service := newFlowService(t, false, nil)
	err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "stranger", Day: 0})
	if err != ErrInvalidArgument {
		t.Fatalf("invalid day = %v", err)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "dept-auto", Day: 2}); err != nil {
		t.Fatal(err)
	}
	err = service.Pass(StageCommand{LicenseID: "missing", StageID: "auto", Actor: "dept-auto", Day: 1})
	if err != ErrClockMovedBack {
		t.Fatalf("clock beats not found = %v", err)
	}
	err = service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "stranger", Day: 1})
	if err != ErrClockMovedBack {
		t.Fatalf("clock beats forbidden = %v", err)
	}
}

func TestPrerequisiteErrorType(t *testing.T) {
	service, state := setupTwoStageService(t, nil)
	stage := state.stages["b"]
	stage.status = StageProcessing
	stage.segmentStartAt = intPtr(1)
	stage.startedAt = intPtr(1)
	err := service.Pass(StageCommand{LicenseID: "L", StageID: "b", Actor: "dept-b", Day: 1})
	if err != ErrPrerequisiteNotPassed {
		t.Fatalf("prerequisite = %v", err)
	}
}
