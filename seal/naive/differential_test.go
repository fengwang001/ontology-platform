package naive

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/seal"
)

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 80; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferentialSeed(t, seed)
		})
	}
}

func runDifferentialSeed(t *testing.T, seed int64) {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	config := seal.Config{
		ApprovalValiditySeconds: 8, ReceiptDeadlineSeconds: 6,
		FirstAmountThreshold: 100, SecondAmountThreshold: 200, DualPresenceThreshold: 200,
	}
	service := seal.NewService(config)
	model := New(config)
	now := int64(0)

	mustBoth(t, seed, 0, "create", service.CreateStamp(seal.CreateStampInput{
		Now: 0, StampID: "stamp", Category: "contract", CustodianA: "c1", CustodianB: "c2",
	}), model.CreateStamp(seal.CreateStampInput{
		Now: 0, StampID: "stamp", Category: "contract", CustodianA: "c1", CustodianB: "c2",
	}))

	for step := 1; step <= 260; step++ {
		if random.Intn(5) != 0 {
			now += int64(random.Intn(4))
		}
		operation := random.Intn(13)
		employee := []string{"alice", "bob", "carol"}[random.Intn(3)]
		authID := "auth-" + employee
		appID := fmt.Sprintf("app-%d", step)

		switch operation {
		case 0:
			input := seal.GrantAuthorizationInput{
				AuthorizationID: authID, Now: now, EmployeeID: employee, StampID: "stamp",
				MaterialKinds: []string{"contract", "invoice"}, MaxAmount: int64(random.Intn(300)),
				DailyLimit: int64(1 + random.Intn(3)), StartsAt: now, EndsAt: now + int64(20+random.Intn(100)),
			}
			err1, err2 := service.GrantAuthorization(input), model.Grant(input)
			logStep(t, seed, step, "grant", input, err1, err2, "valid interval, limit and material set checked at submit/execute")
			assertSame(t, seed, step, err1, err2)
		case 1:
			if random.Intn(3) == 0 {
				input := seal.RevokeAuthorizationInput{Now: now, AuthorizationID: authID}
				err1, err2 := service.RevokeAuthorization(input), model.Revoke(input)
				logStep(t, seed, step, "revoke", input, err1, err2, "revoked authorization survives history but blocks execution")
				assertSame(t, seed, step, err1, err2)
			}
		case 2:
			input := seal.SubmitApplicationInput{
				Now: now, ApplicationID: appID, ApplicantID: employee, StampID: "stamp",
				MaterialKind: []string{"contract", "invoice", "note"}[random.Intn(3)],
				Amount:       int64(random.Intn(300)),
			}
			err1, err2 := service.SubmitApplication(input), model.Submit(input)
			logStep(t, seed, step, "submit", input, err1, err2, "only format, current authorization and freeze are checked; no quota consumed")
			assertSame(t, seed, step, err1, err2)
		case 3, 4:
			id := pickApplication(model, random)
			if id == "" {
				continue
			}
			approver := []string{"a1", "a2", "a3", "c1", "c2", employee}[random.Intn(6)]
			input := seal.DecisionInput{Now: now, ApplicationID: id, ApproverID: approver, Approve: random.Intn(7) != 0}
			err1, err2 := service.DecideApplication(input), model.Decide(input)
			logStep(t, seed, step, "decide", input, err1, err2, "distinct approvers; amount tier 1/2/3; highest tier requires custodian; one rejection terminates")
			assertSame(t, seed, step, err1, err2)
		case 5:
			id := pickApproved(model, random)
			if id == "" {
				continue
			}
			input := seal.PresenceInput{Now: now, ApplicationID: id, CustodianID: []string{"c1", "c2", "other"}[random.Intn(3)]}
			err1, err2 := service.ConfirmPresence(input), model.Confirm(input)
			logStep(t, seed, step, "presence", input, err1, err2, "only the two named custodians may confirm; same custodian cannot count twice")
			assertSame(t, seed, step, err1, err2)
		case 6:
			id := pickApproved(model, random)
			if id == "" {
				continue
			}
			input := seal.ApplicationInput{Now: now, ApplicationID: id}
			err1, err2 := service.ExecuteApplication(input), model.Execute(input)
			logStep(t, seed, step, "execute", input, err1, err2, "validity, stamp, authorization, category/amount, daily quota and two-person presence checked in priority")
			assertSame(t, seed, step, err1, err2)
		case 7:
			id := pickExecuted(model, random)
			if id == "" {
				continue
			}
			input := seal.ApplicationInput{Now: now, ApplicationID: id}
			err1, err2 := service.RegisterReceipt(input), model.Receipt(input)
			logStep(t, seed, step, "receipt", input, err1, err2, "deadline inclusive: now == executedAt + fixed seconds is still timely")
			assertSame(t, seed, step, err1, err2)
		case 8:
			id := pickExecuted(model, random)
			if id == "" {
				continue
			}
			input := seal.MarkVoidInput{Now: now, ApplicationID: id, CustodianID: []string{"c1", "c2", "other"}[random.Intn(3)]}
			err1, err2 := service.MarkVoid(input), model.Void(input)
			logStep(t, seed, step, "void", input, err1, err2, "void annotation does not undo execution or restore daily count")
			assertSame(t, seed, step, err1, err2)
		case 9:
			input := seal.SetStampStatusInput{Now: now, StampID: "stamp", Disabled: random.Intn(2) == 0}
			err1, err2 := service.SetStampStatus(input), model.SetStampStatus(input)
			logStep(t, seed, step, "stamp-status", input, err1, err2, "disable immediately invalidates pending/approved applications; restore does not revive them")
			assertSame(t, seed, step, err1, err2)
		}

		assertState(t, service, model)
		for _, authorizationID := range []string{"auth-alice", "auth-bob", "auth-carol"} {
			if count1, count2 := service.DailyUsageAt(authorizationID, now), model.Daily(authorizationID, now); count1 != count2 {
				t.Fatalf("seed %d step %d daily count for %s = %d, naive %d", seed, step, authorizationID, count1, count2)
			}
		}
	}
}

func mustBoth(t *testing.T, seed int64, step int, name string, err1 error, err2 error) {
	t.Helper()
	assertSame(t, seed, step, err1, err2)
}

func assertSame(t *testing.T, seed int64, step int, err1 error, err2 error) {
	t.Helper()
	if seal.ErrorKindOf(err1) != seal.ErrorKindOf(err2) {
		t.Fatalf("seed %d step %d service=%v naive=%v", seed, step, err1, err2)
	}
}

func logStep(t *testing.T, seed int64, step int, name string, input any, err1 error, err2 error, reason string) {
	t.Helper()
	t.Logf("seed=%d step=%d op=%s input=%+v => service=%q naive=%q basis=%q",
		seed, step, name, input, seal.ErrorKindOf(err1), seal.ErrorKindOf(err2), reason)
}

func assertState(t *testing.T, service *seal.Service, model *Model) {
	t.Helper()
	ids := make([]string, 0, len(model.Apps))
	for id := range model.Apps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		actual, exists := service.GetApplication(id)
		if !exists {
			t.Fatalf("service missing application %s", id)
		}
		expected := model.Apps[id]
		if actual.State != expected.State || actual.ExpiresAt != expected.ExpiresAt ||
			actual.ExecutedAt != expected.ExecutedAt || actual.ReceiptDueAt != expected.ReceiptDueAt ||
			actual.ReceiptDone != expected.ReceiptDone || actual.Overdue != expected.Overdue ||
			actual.Voided != expected.Voided || len(actual.Approvals) != len(expected.Approvals) ||
			len(actual.Confirmers) != len(expected.Confirmers) {
			t.Fatalf("application mismatch id=%s service=%+v naive=%+v", id, actual, expected)
		}
	}
}

func pickApplication(model *Model, random *rand.Rand) string {
	return pickByState(model, random, func(seal.ApplicationState) bool { return true })
}

func pickApproved(model *Model, random *rand.Rand) string {
	return pickByState(model, random, func(state seal.ApplicationState) bool { return state == seal.StateApproved })
}

func pickExecuted(model *Model, random *rand.Rand) string {
	return pickByState(model, random, func(state seal.ApplicationState) bool { return state == seal.StateExecuted })
}

func pickByState(model *Model, random *rand.Rand, match func(seal.ApplicationState) bool) string {
	ids := make([]string, 0)
	for id, application := range model.Apps {
		if match(application.State) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[random.Intn(len(ids))]
}
