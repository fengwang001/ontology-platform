package approval

import (
	"sync"
	"testing"
)

func TestAutoPassRecordedOnNextWorkday(t *testing.T) {
	service := newFlowService(t, true, []int{2})
	err := service.Pass(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 4})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.QueryStage("L", "auto", 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StageProcessing {
		t.Fatalf("day one = %s", view.Status)
	}
	view, _ = service.QueryStage("L", "auto", 2)
	if view.Status != StageProcessing {
		t.Fatalf("non-working day = %s", view.Status)
	}
	view, _ = service.QueryStage("L", "auto", 3)
	if view.Status != StageProcessing {
		t.Fatalf("due day = %s", view.Status)
	}
	view, _ = service.QueryStage("L", "auto", 4)
	if view.Status != StagePassed || view.PassedAt == nil || *view.PassedAt != 4 {
		t.Fatalf("auto pass view = %+v", view)
	}
}

func TestFailureKeepsParallelStagesButDeniesAfterTheyFinish(t *testing.T) {
	service := newFlowService(t, false, nil)
	if err := service.Fail(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1}); err != nil {
		t.Fatal(err)
	}
	progress, err := service.QueryProgress("L", 1)
	if err != nil {
		t.Fatal(err)
	}
	if progress.FinalAt != nil {
		t.Fatalf("parallel active, finalAt = %v", progress.FinalAt)
	}
	if progress.Stages["auto"].Status != StageProcessing {
		t.Fatalf("parallel stage = %s", progress.Stages["auto"].Status)
	}
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "dept-auto", Day: 2}); err != nil {
		t.Fatal(err)
	}
	progress, _ = service.QueryProgress("L", 2)
	if progress.Status != OverallDenied || progress.FinalAt == nil || *progress.FinalAt != 2 {
		t.Fatalf("progress = %+v", progress)
	}
}

func TestWithdrawStopsAllAndPreservesResults(t *testing.T) {
	service := newFlowService(t, false, nil)
	if err := service.Pass(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.Withdraw(WithdrawRequest{LicenseID: "L", Actor: "applicant", Day: 2}); err != nil {
		t.Fatal(err)
	}
	progress, err := service.QueryProgress("L", 2)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Status != OverallWithdrawn || progress.Stages["other"].Status != StagePassed || progress.Stages["auto"].Status != StageWithdrawn {
		t.Fatalf("progress = %+v", progress)
	}
	err = service.Pass(StageCommand{LicenseID: "L", StageID: "auto", Actor: "dept-auto", Day: 2})
	if err != ErrInvalidState {
		t.Fatalf("post-final operation = %v", err)
	}
}

func TestConcurrentCommandsAreSerialized(t *testing.T) {
	service := newFlowService(t, false, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	var first, second error
	go func() {
		defer wg.Done()
		first = service.Pass(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1})
	}()
	go func() {
		defer wg.Done()
		second = service.Fail(StageCommand{LicenseID: "L", StageID: "other", Actor: "dept-other", Day: 1})
	}()
	wg.Wait()
	if (first != nil) == (second != nil) {
		t.Fatalf("exactly one terminal command should succeed: first=%v second=%v", first, second)
	}
}

func newFlowService(t *testing.T, auto bool, holidays []int) *Service {
	t.Helper()
	service, err := NewService(NewCalendar(holidays), []LicenseType{{
		ID: "T",
		Stages: []StageDefinition{
			{ID: "auto", Department: "AUTO", TimeLimit: 1, AutoPassOnTime: auto, CorrectionLimit: 0},
			{ID: "other", Department: "OTHER", TimeLimit: 3, CorrectionLimit: 2, CorrectionDays: 3},
			{ID: "dependent", Department: "DEP", TimeLimit: 1, Prerequisites: []string{"auto"}, CorrectionLimit: 0},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	actors := map[string]Actor{
		"dept-auto":  {ID: "dept-auto", Department: "AUTO"},
		"dept-other": {ID: "dept-other", Department: "OTHER"},
		"dept-dep":   {ID: "dept-dep", Department: "DEP"},
	}
	if err := service.Accept(AcceptRequest{LicenseID: "L", TypeID: "T", ApplicantID: "applicant", Actors: actors, Day: 1}); err != nil {
		t.Fatal(err)
	}
	return service
}
