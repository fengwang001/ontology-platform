package seal

import (
	"sync"
	"testing"
)

func testService() *Service {
	return NewService(Config{
		ApprovalValiditySeconds: 10,
		ReceiptDeadlineSeconds:  5,
		FirstAmountThreshold:    100,
		SecondAmountThreshold:   200,
		DualPresenceThreshold:   200,
	})
}

func setupService(t *testing.T) *Service {
	t.Helper()
	service := testService()
	must(t, service.CreateStamp(CreateStampInput{
		Now: 0, StampID: "stamp", Category: "contract", CustodianA: "c1", CustodianB: "c2",
	}))
	must(t, service.GrantAuthorization(GrantAuthorizationInput{
		AuthorizationID: "auth", Now: 0, EmployeeID: "alice", StampID: "stamp",
		MaterialKinds: []string{"contract", "invoice"}, MaxAmount: 300, DailyLimit: 2,
		StartsAt: 10, EndsAt: 200000,
	}))
	return service
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func assertKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if got := ErrorKindOf(err); got != kind {
		t.Fatalf("error kind = %q, want %q (err=%v)", got, kind, err)
	}
}

func submit(t *testing.T, service *Service, id string, now int64, amount int64, material string) {
	t.Helper()
	must(t, service.SubmitApplication(SubmitApplicationInput{
		Now: now, ApplicationID: id, ApplicantID: "alice", StampID: "stamp",
		MaterialKind: material, Amount: amount,
	}))
}

func approve(t *testing.T, service *Service, id string, now int64, approver string) {
	t.Helper()
	must(t, service.DecideApplication(DecisionInput{
		Now: now, ApplicationID: id, ApproverID: approver, Approve: true,
	}))
}

func TestApprovalThresholds(t *testing.T) {
	cases := []struct {
		name      string
		amount    int64
		approvers []string
	}{
		{"below first", 99, []string{"a1"}},
		{"exact first needs two", 100, []string{"a1", "a2"}},
		{"below second needs two", 199, []string{"a1", "a2"}},
		{"exact second needs custodian", 200, []string{"a1", "a2", "c1"}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := setupService(t)
			id := "app"
			submit(t, service, id, 20, tc.amount, "contract")
			for index, approver := range tc.approvers {
				approve(t, service, id, 21+int64(index), approver)
			}
			application, _ := service.GetApplication(id)
			want := StateApproved
			if index < 3 && len(tc.approvers) == 1 {
				want = StateApproved
			}
			if application.State != want {
				t.Fatalf("state = %q, want %q", application.State, want)
			}
		})
	}
}

func TestHighestTierCustodianRequired(t *testing.T) {
	service := setupService(t)
	submit(t, service, "app", 20, 200, "contract")
	approve(t, service, "app", 21, "a1")
	approve(t, service, "app", 22, "a2")
	err := service.DecideApplication(DecisionInput{Now: 23, ApplicationID: "app", ApproverID: "a3", Approve: true})
	assertKind(t, err, KindInvalidState)
	approve(t, service, "app", 24, "c1")
	application, _ := service.GetApplication("app")
	if application.State != StateApproved {
		t.Fatalf("state = %q", application.State)
	}
}

func TestApprovalValidityEndpoint(t *testing.T) {
	service := setupService(t)
	submit(t, service, "app", 20, 50, "contract")
	approve(t, service, "app", 21, "a1")
	assertKind(t, service.ExecuteApplication(ApplicationInput{Now: 32, ApplicationID: "app"}), KindInvalidState)

	service = setupService(t)
	submit(t, service, "app", 20, 50, "contract")
	approve(t, service, "app", 21, "a1")
	must(t, service.ExecuteApplication(ApplicationInput{Now: 31, ApplicationID: "app"}))
}

func TestAuthorizationEndpoints(t *testing.T) {
	service := setupService(t)
	must(t, service.GrantAuthorization(GrantAuthorizationInput{
		AuthorizationID: "short", Now: 0, EmployeeID: "alice", StampID: "stamp",
		MaterialKinds: []string{"contract"}, MaxAmount: 300, DailyLimit: 2,
		StartsAt: 10, EndsAt: 100,
	}))
	must(t, service.RevokeAuthorization(RevokeAuthorizationInput{Now: 1, AuthorizationID: "auth"}))
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 9, ApplicationID: "early", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}), KindAuthorization)
	must(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 10, ApplicationID: "start", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}))
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 100, ApplicationID: "end", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}), KindAuthorization)
}

func TestDayBoundaryAndDailyLimit(t *testing.T) {
	service := setupService(t)
	for _, item := range []struct {
		id  string
		now int64
	}{{"a", 86399}, {"b", 86400}, {"c", 172799}} {
		submit(t, service, item.id, item.now, 50, "contract")
		approve(t, service, item.id, item.now, "a1")
		must(t, service.ExecuteApplication(ApplicationInput{Now: item.now, ApplicationID: item.id}))
		must(t, service.RegisterReceipt(ApplicationInput{Now: item.now, ApplicationID: item.id}))
	}

	service = setupService(t)
	submit(t, service, "a", 86399, 50, "contract")
	approve(t, service, "a", 86399, "a1")
	must(t, service.ExecuteApplication(ApplicationInput{Now: 86399, ApplicationID: "a"}))
	submit(t, service, "b", 86399, 50, "contract")
	approve(t, service, "b", 86399, "a1")
	must(t, service.ExecuteApplication(ApplicationInput{Now: 86399, ApplicationID: "b"}))
	submit(t, service, "c", 86399, 50, "contract")
	approve(t, service, "c", 86399, "a1")
	assertKind(t, service.ExecuteApplication(ApplicationInput{Now: 86399, ApplicationID: "c"}), KindDailyLimitExceeded)
	must(t, service.ExecuteApplication(ApplicationInput{Now: 86400, ApplicationID: "c"}))
}

func TestDualPresence(t *testing.T) {
	service := setupService(t)
	submit(t, service, "app", 20, 200, "contract")
	approve(t, service, "app", 21, "a1")
	approve(t, service, "app", 22, "a2")
	approve(t, service, "app", 23, "c1")
	must(t, service.ConfirmPresence(PresenceInput{Now: 24, ApplicationID: "app", CustodianID: "c1"}))
	assertKind(t, service.ConfirmPresence(PresenceInput{Now: 25, ApplicationID: "app", CustodianID: "c1"}), KindInvalidState)
	assertKind(t, service.ExecuteApplication(ApplicationInput{Now: 26, ApplicationID: "app"}), KindPresenceRequired)
	must(t, service.ConfirmPresence(PresenceInput{Now: 27, ApplicationID: "app", CustodianID: "c2"}))
	must(t, service.ExecuteApplication(ApplicationInput{Now: 28, ApplicationID: "app"}))
}

func TestReceiptDeadlineAndMultipleOverdue(t *testing.T) {
	service := setupService(t)
	submit(t, service, "a", 20, 50, "contract")
	approve(t, service, "a", 21, "a1")
	must(t, service.ExecuteApplication(ApplicationInput{Now: 22, ApplicationID: "a"}))
	submit(t, service, "b", 23, 50, "contract")
	approve(t, service, "b", 24, "a1")
	must(t, service.ExecuteApplication(ApplicationInput{Now: 25, ApplicationID: "b"}))
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 31, ApplicationID: "blocked", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}), KindFrozen)
	must(t, service.RegisterReceipt(ApplicationInput{Now: 31, ApplicationID: "a"}))
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 32, ApplicationID: "still-blocked", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}), KindFrozen)
	must(t, service.RegisterReceipt(ApplicationInput{Now: 32, ApplicationID: "b"}))
	must(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 33, ApplicationID: "allowed", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}))

	service = setupService(t)
	submit(t, service, "exact", 20, 50, "contract")
	approve(t, service, "exact", 21, "a1")
	must(t, service.ExecuteApplication(ApplicationInput{Now: 22, ApplicationID: "exact"}))
	must(t, service.RegisterReceipt(ApplicationInput{Now: 27, ApplicationID: "exact"}))
	if service.IsFrozen("alice") {
		t.Fatal("receipt at exact deadline must not freeze applicant")
	}
}

func TestDisableRestore(t *testing.T) {
	service := setupService(t)
	submit(t, service, "app", 20, 50, "contract")
	must(t, service.SetStampStatus(SetStampStatusInput{Now: 21, StampID: "stamp", Disabled: true}))
	application, _ := service.GetApplication("app")
	if application.State != StateInvalidated {
		t.Fatalf("state = %q", application.State)
	}
	must(t, service.SetStampStatus(SetStampStatusInput{Now: 22, StampID: "stamp", Disabled: false}))
	application, _ = service.GetApplication("app")
	if application.State != StateInvalidated {
		t.Fatalf("state after restore = %q", application.State)
	}
}

func TestErrorPriority(t *testing.T) {
	service := setupService(t)
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: -1, ApplicationID: "missing", ApplicantID: "",
	}), KindInvalidParameter)
	must(t, service.SetStampStatus(SetStampStatusInput{Now: 1, StampID: "stamp", Disabled: false}))
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 0, ApplicationID: "missing", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}), KindClockRollback)
	must(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 10, ApplicationID: "advance", ApplicantID: "alice", StampID: "stamp",
		MaterialKind: "contract", Amount: 1,
	}))
	assertKind(t, service.SubmitApplication(SubmitApplicationInput{
		Now: 20, ApplicationID: "missing", ApplicantID: "nobody", StampID: "missing",
		MaterialKind: "contract", Amount: 1,
	}), KindNotFound)
}

func TestConcurrentExecution(t *testing.T) {
	service := setupService(t)
	submit(t, service, "app", 20, 50, "contract")
	approve(t, service, "app", 21, "a1")
	var wait sync.WaitGroup
	var firstMu sync.Mutex
	var success int
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := service.ExecuteApplication(ApplicationInput{Now: 22, ApplicationID: "app"})
			if err == nil {
				firstMu.Lock()
				success++
				firstMu.Unlock()
			}
		}()
	}
	wait.Wait()
	if success != 1 {
		t.Fatalf("successful executions = %d, want 1", success)
	}
}

func TestConcurrentBatchEquivalentToSerial(t *testing.T) {
	concurrentService, concurrentResults := runConcurrentBatch(t)
	serialService, serialResults := runConcurrentBatch(t)

	concurrentKinds := countKinds(concurrentResults)
	serialKinds := countKinds(serialResults)
	if len(concurrentKinds) != len(serialKinds) {
		t.Fatalf("result kind counts differ: concurrent=%v serial=%v", concurrentKinds, serialKinds)
	}
	for kindValue, count := range serialKinds {
		if concurrentKinds[kindValue] != count {
			t.Fatalf("result kind %s: concurrent=%d serial=%d", kindValue, concurrentKinds[kindValue], count)
		}
	}
	for index := range 32 {
		id := batchApplicationID(index)
		concurrentApplication, _ := concurrentService.GetApplication(id)
		serialApplication, _ := serialService.GetApplication(id)
		if concurrentApplication.State != serialApplication.State ||
			concurrentApplication.ExecutedAt != serialApplication.ExecutedAt {
			t.Fatalf("application %s concurrent=%+v serial=%+v", id, concurrentApplication, serialApplication)
		}
	}
}

func runConcurrentBatch(t *testing.T) (*Service, []error) {
	t.Helper()
	service := setupService(t)
	must(t, service.GrantAuthorization(GrantAuthorizationInput{
		AuthorizationID: "batch-auth", Now: 0, EmployeeID: "alice", StampID: "stamp",
		MaterialKinds: []string{"contract"}, MaxAmount: 300, DailyLimit: 64,
		StartsAt: 0, EndsAt: 1000,
	}))
	must(t, service.RevokeAuthorization(RevokeAuthorizationInput{Now: 1, AuthorizationID: "auth"}))

	for index := range 32 {
		id := batchApplicationID(index)
		submitTime := int64(20 + index*2)
		submit(t, service, id, submitTime, 50, "contract")
		approve(t, service, id, submitTime+1, "a1")
	}

	type result struct {
		err error
	}
	results := make(chan result, 128)
	var wait sync.WaitGroup
	for repeat := 0; repeat < 4; repeat++ {
		for index := range 32 {
			id := batchApplicationID(index)
			wait.Add(1)
			go func() {
				defer wait.Done()
				results <- result{err: service.ExecuteApplication(ApplicationInput{
					Now: 200, ApplicationID: id,
				})}
			}()
		}
	}
	wait.Wait()
	close(results)

	errs := make([]error, 0, 128)
	for resultValue := range results {
		errs = append(errs, resultValue.err)
	}
	return service, errs
}

func batchApplicationID(index int) string {
	return "batch-" + string(rune('a'+index))
}

func countKinds(errs []error) map[ErrorKind]int {
	counts := map[ErrorKind]int{}
	for _, err := range errs {
		counts[ErrorKindOf(err)]++
	}
	return counts
}
