package approval

import (
	"errors"
	"math/rand"
	"testing"
)

func TestNaiveModelRandomComparison(t *testing.T) {
	definitions := []StageDefinition{
		{ID: "a", Department: "A", TimeLimit: 2, CorrectionLimit: 2, CorrectionDays: 3},
		{ID: "b", Department: "B", TimeLimit: 1, AutoPassOnTime: true, Prerequisites: []string{"a"}, CorrectionLimit: 0},
		{ID: "c", Department: "C", TimeLimit: 3, CorrectionLimit: 1, CorrectionDays: 2},
	}
	holidays := []int{4, 11, 18}
	service, err := NewService(NewCalendar(holidays), []LicenseType{{ID: "T", Stages: definitions}})
	if err != nil {
		t.Fatal(err)
	}
	actors := map[string]Actor{}
	for _, definition := range definitions {
		id := "actor-" + definition.Department
		actors[id] = Actor{ID: id, Department: definition.Department}
	}
	err = service.Accept(AcceptRequest{LicenseID: "L", TypeID: "T", ApplicantID: "applicant", Actors: actors, Day: 1})
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel(definitions, holidays, 1)
	rng := rand.New(rand.NewSource(1633))
	day := 1
	for iteration := 0; iteration < 300; iteration++ {
		day += rng.Intn(3)
		definition := definitions[rng.Intn(len(definitions))]
		cmd := StageCommand{LicenseID: "L", StageID: definition.ID, Actor: "actor-" + definition.Department, Day: day}
		var actual error
		var basis string
		switch rng.Intn(5) {
		case 0:
			actual = service.Pass(cmd)
			basis = "人工通过：目标链到期结算后，办理中且前置通过才可办结"
			if modelErr := model.command("pass", cmd); !sameError(actual, modelErr) {
				t.Fatalf("step %d pass mismatch actual=%v model=%v", iteration, actual, modelErr)
			}
		case 1:
			actual = service.Fail(cmd)
			basis = "人工不通过：当前环节失败，未启动后继终止，并行环节继续"
			if modelErr := model.command("fail", cmd); !sameError(actual, modelErr) {
				t.Fatalf("step %d fail mismatch actual=%v model=%v", iteration, actual, modelErr)
			}
		case 2:
			actual = service.RequestCorrection(cmd)
			basis = "补正通知：暂停时扣除当前计时段已用工作日，通知次日起算补正期限"
			if modelErr := model.command("request", cmd); !sameError(actual, modelErr) {
				t.Fatalf("step %d request mismatch actual=%v model=%v", iteration, actual, modelErr)
			}
		case 3:
			actual = service.SubmitCorrection(cmd)
			basis = "补正提交：期限内提交后从下一工作日恢复剩余时限，逾期目标链结算为不通过"
			if modelErr := model.submit(cmd); !sameError(actual, modelErr) {
				t.Fatalf("step %d submit mismatch actual=%v model=%v", iteration, actual, modelErr)
			}
		default:
			queryDay := day
			basis = "历史查询：按双时间可见事件重放并投影自动通过/补正逾期/级联"
			actualView, queryErr := service.QueryStage("L", definition.ID, queryDay)
			expectedView := model.viewAt(definition.ID, queryDay)
			t.Logf("step=%d input=query stage=%s day=%d output=%+v err=%v basis=%s", iteration, definition.ID, queryDay, actualView, queryErr, basis)
			if queryErr != nil {
				t.Fatalf("query error: %v", queryErr)
			}
			assertSameView(t, iteration, definition.ID, actualView, expectedView)
			continue
		}
		t.Logf("step=%d input=%+v output=%v basis=%s", iteration, cmd, actual, basis)
		for _, definition := range definitions {
			actualView, queryErr := service.QueryStage("L", definition.ID, day)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			expectedView := model.viewAt(definition.ID, day)
			assertSameView(t, iteration, definition.ID, actualView, expectedView)
		}
	}
}

func sameError(actual, expected error) bool {
	return errors.Is(actual, expected) || (actual == nil && expected == nil)
}

func assertSameView(t *testing.T, iteration int, stageID string, actual, expected StageView) {
	t.Helper()
	if actual.Status != expected.Status ||
		actual.Overdue != expected.Overdue ||
		!sameInt(actual.PassedAt, expected.PassedAt) ||
		!sameInt(actual.FailedAt, expected.FailedAt) ||
		!sameInt(actual.StartedAt, expected.StartedAt) ||
		!sameInt(actual.CorrectionDeadline, expected.CorrectionDeadline) ||
		!sameInt(actual.RemainingWorkdays, expected.RemainingWorkdays) {
		t.Fatalf("step %d stage %s mismatch\nactual=%+v\nexpected=%+v", iteration, stageID, actual, expected)
	}
}

func sameInt(actual, expected *int) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	return *actual == *expected
}
