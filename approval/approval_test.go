package approval

import "testing"

func TestCalendarBoundaries(t *testing.T) {
	calendar := NewCalendar([]int{3, 4, 7})
	if calendar.IsWorkday(4) {
		t.Fatal("day 4 must be non-working")
	}
	if got := calendar.NextWorkday(2); got != 5 {
		t.Fatalf("next workday = %d, want 5", got)
	}
	if got := calendar.NthWorkdayFrom(2, 3); got != 6 {
		t.Fatalf("third workday = %d, want 6", got)
	}
	if got := calendar.WorkdaysBetweenInclusive(2, 8); got != 4 {
		t.Fatalf("workdays = %d, want 4", got)
	}
}

func TestStartDueAndDueStillValid(t *testing.T) {
	service, state := setupTwoStageServiceWithLimit(t, []int{2, 4}, 1)
	stage := state.stages["a"]
	due := service.dueAt(stage)
	if due == nil || *due != 3 {
		t.Fatalf("due = %d, want 3", *due)
	}
	view, err := service.QueryStage("L", "a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StageProcessing || view.RemainingWorkdays == nil || *view.RemainingWorkdays != 1 || view.Overdue {
		t.Fatalf("due-day view = %+v", view)
	}
	view, _ = service.QueryStage("L", "a", 4)
	if view.Overdue {
		t.Fatalf("non-working overdue = %+v", view)
	}
	view, _ = service.QueryStage("L", "a", 5)
	if !view.Overdue || view.Status != StageProcessing {
		t.Fatalf("day-after-due view = %+v", view)
	}
}

func TestCorrectionDeadlineLastDayAndResume(t *testing.T) {
	service, state := setupTwoStageService(t, []int{3})
	if err := service.RequestCorrection(StageCommand{LicenseID: "L", StageID: "a", Actor: "dept-a", Day: 2}); err != nil {
		t.Fatal(err)
	}
	stage := state.stages["a"]
	if stage.correctionDueAt == nil || *stage.correctionDueAt != 8 {
		t.Fatalf("correction deadline = %d, want 8", *stage.correctionDueAt)
	}
	if stage.remainingLimit != 1 {
		t.Fatalf("remaining after pause = %d, want 1", stage.remainingLimit)
	}
	if err := service.SubmitCorrection(StageCommand{LicenseID: "L", StageID: "a", Actor: "dept-a", Day: 8}); err != nil {
		t.Fatal(err)
	}
	due := service.dueAt(stage)
	if due == nil || *due != 9 {
		t.Fatalf("resume due = %d, want 9", *due)
	}

	_ = service
}

func TestCorrectionOneDayLateFails(t *testing.T) {
	service, _ := setupTwoStageService(t, nil)
	if err := service.RequestCorrection(StageCommand{LicenseID: "L", StageID: "a", Actor: "dept-a", Day: 1}); err != nil {
		t.Fatal(err)
	}
	err := service.SubmitCorrection(StageCommand{LicenseID: "L", StageID: "a", Actor: "dept-a", Day: 14})
	if err != ErrInvalidState {
		t.Fatalf("late submit error = %v, want invalid state", err)
	}
	progress, err := service.QueryProgress("L", 14)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Status != OverallDenied {
		t.Fatalf("progress = %s, want denied", progress.Status)
	}
}

func setupTwoStageService(t *testing.T, holidays []int) (*Service, *licenseState) {
	return setupTwoStageServiceWithLimit(t, holidays, 2)
}

func setupTwoStageServiceWithLimit(t *testing.T, holidays []int, limit int) (*Service, *licenseState) {
	t.Helper()
	service, err := NewService(NewCalendar(holidays), []LicenseType{{
		ID: "T",
		Stages: []StageDefinition{
			{ID: "a", Department: "A", TimeLimit: limit, CorrectionLimit: 2, CorrectionDays: 5},
			{ID: "b", Department: "B", TimeLimit: 2, Prerequisites: []string{"a"}, CorrectionLimit: 0},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	actors := map[string]Actor{
		"dept-a": {ID: "dept-a", Department: "A"},
		"dept-b": {ID: "dept-b", Department: "B"},
	}
	if err := service.Accept(AcceptRequest{LicenseID: "L", TypeID: "T", ApplicantID: "applicant", Actors: actors, Day: 1}); err != nil {
		t.Fatal(err)
	}
	return service, service.licenses["L"]
}
