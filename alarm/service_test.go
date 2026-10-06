package alarm

import (
	"bytes"
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		HighManualDuration: 10,
		LowManualDuration:  5,
		ChatterWindow:      10,
		ChatterCount:       3,
		ChatterDuration:    7,
	}
}

func mustService(t *testing.T, config Config, points ...PointConfig) (*Service, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	service, err := NewService(config, points)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.WithLogger(&logs)
	return service, &logs
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustOp(t *testing.T, _ Result, err error) {
	t.Helper()
	must(t, err)
}

func assertErrorKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	var alarmError *Error
	if !errors.As(err, &alarmError) {
		t.Fatalf("error %v is not *Error", err)
	}
	if alarmError.Kind != kind {
		t.Fatalf("error kind = %d, want %d (%v)", alarmError.Kind, kind, err)
	}
}

func assertActiveIDs(t *testing.T, service *Service, at int64, want []string) {
	t.Helper()
	got := make([]string, 0, len(want))
	list, err := service.ActiveAlarms(at)
	if err != nil {
		t.Fatalf("ActiveAlarms() error = %v", err)
	}
	for _, entry := range list {
		got = append(got, entry.PointID)
	}
	if len(got) != len(want) {
		t.Fatalf("active ids = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("active ids = %v, want %v", got, want)
		}
	}
}

func TestSuppressionReturnRetriggerThenRelease(t *testing.T) {
	service, _ := mustService(t, testConfig(), PointConfig{ID: "low", Priority: Low})

	if _, err := service.Trigger(Operation{At: 1, PointID: "low", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Acknowledge(Operation{At: 2, PointID: "low", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Suppress(Operation{At: 3, PointID: "low", Role: Operator, Duration: 5, Reason: "maintenance"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.ReturnToNormal(Operation{At: 4, PointID: "low", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 5, PointID: "low", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActiveIDs(t, service, 6, nil)

	list := mustResultList(t, service, 8)
	if len(list) != 1 || list[0].State != ActiveUnacknowledged {
		t.Fatalf("list after release = %+v, want one active-unacknowledged alarm", list)
	}
}

func mustResultList(t *testing.T, service *Service, at int64) []ActiveAlarm {
	t.Helper()
	list, err := service.ActiveAlarms(at)
	if err != nil {
		t.Fatalf("ActiveAlarms() error = %v", err)
	}
	return list
}

func TestSuppressionAndInhibitionOverlap(t *testing.T) {
	service, _ := mustService(t, testConfig(), PointConfig{ID: "high", Priority: High, Conditions: []string{"C"}})
	if _, err := service.Trigger(Operation{At: 1, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Suppress(Operation{At: 2, PointID: "high", Role: Operator, Duration: 10, Reason: "maintenance"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := service.SetCondition(ConditionUpdate{At: 3, Condition: "C", Active: true}); err != nil {
		t.Fatalf("SetCondition() error = %v", err)
	}
	assertActiveIDs(t, service, 7, nil)

	if err := service.SetCondition(ConditionUpdate{At: 8, Condition: "C", Active: false}); err != nil {
		t.Fatalf("SetCondition() error = %v", err)
	}
	assertActiveIDs(t, service, 8, nil)

	if err := service.SetCondition(ConditionUpdate{At: 9, Condition: "C", Active: true}); err != nil {
		t.Fatalf("SetCondition() error = %v", err)
	}
	assertActiveIDs(t, service, 9, nil)
	if _, err := service.ReleaseSuppression(Operation{At: 10, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActiveIDs(t, service, 10, nil)
	if err := service.SetCondition(ConditionUpdate{At: 11, Condition: "C", Active: false}); err != nil {
		t.Fatalf("SetCondition() error = %v", err)
	}
	assertActiveIDs(t, service, 11, []string{"high"})
}

func TestChatterWindowLeftOpenRightClosed(t *testing.T) {
	service, _ := mustService(t, testConfig(), PointConfig{ID: "high", Priority: High})
	if _, err := service.Trigger(Operation{At: 1, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.ReturnToNormal(Operation{At: 2, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 6, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.ReturnToNormal(Operation{At: 7, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 10, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActiveIDs(t, service, 10, nil)
	assertActiveIDs(t, service, 17, []string{"high"})

	service2, _ := mustService(t, testConfig(), PointConfig{ID: "high", Priority: High})
	if _, err := service2.Trigger(Operation{At: 1, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service2.ReturnToNormal(Operation{At: 2, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service2.Trigger(Operation{At: 11, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service2.ReturnToNormal(Operation{At: 12, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service2.Trigger(Operation{At: 21, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActiveIDs(t, service2, 21, []string{"high"})
}

func TestDisableEnableInitialState(t *testing.T) {
	service, _ := mustService(t, testConfig(), PointConfig{ID: "emergency", Priority: Emergency})
	if _, err := service.Trigger(Operation{At: 1, PointID: "emergency", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Disable(Operation{At: 2, PointID: "emergency", Role: Engineer, Ticket: "CHG-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.ReturnToNormal(Operation{At: 3, PointID: "emergency", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 4, PointID: "emergency", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Enable(Operation{At: 5, PointID: "emergency", Role: Engineer, Ticket: "CHG-2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActiveIDs(t, service, 5, nil)
	if _, err := service.Trigger(Operation{At: 6, PointID: "emergency", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActiveIDs(t, service, 6, []string{"emergency"})
}

func TestActiveAlarmSortingFourTiers(t *testing.T) {
	service, _ := mustService(t, testConfig(),
		PointConfig{ID: "high-ack", Priority: High},
		PointConfig{ID: "low-unack", Priority: Low},
		PointConfig{ID: "high-unack-late", Priority: High},
		PointConfig{ID: "emergency", Priority: Emergency},
		PointConfig{ID: "high-unack-early", Priority: High},
	)
	if _, err := service.Trigger(Operation{At: 1, PointID: "high-unack-late", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 2, PointID: "high-ack", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Acknowledge(Operation{At: 3, PointID: "high-ack", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 4, PointID: "low-unack", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 5, PointID: "emergency", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 6, PointID: "high-unack-early", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertActiveIDs(t, service, 11, []string{
		"emergency",
		"high-unack-late",
		"high-unack-early",
		"high-ack",
		"low-unack",
	})
}

func TestAlarmRateIntervalBoundaries(t *testing.T) {
	service, _ := mustService(t, testConfig(),
		PointConfig{ID: "a", Priority: High},
		PointConfig{ID: "b", Priority: High},
	)
	if _, err := service.Trigger(Operation{At: 1, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.ReturnToNormal(Operation{At: 2, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Acknowledge(Operation{At: 3, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 5, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 6, PointID: "b", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Acknowledge(Operation{At: 6, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.ReturnToNormal(Operation{At: 7, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Acknowledge(Operation{At: 8, PointID: "b", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := service.Trigger(Operation{At: 11, PointID: "a", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rate, err := service.AlarmRate(11, 10)
	if err != nil || rate != 3 {
		t.Fatalf("rate = %d, %v; want 3", rate, err)
	}
	rate, err = service.AlarmRate(11, 6)
	if err != nil || rate != 2 {
		t.Fatalf("rate = %d, %v; want 2 for right-closed current time", rate, err)
	}
	rate, err = service.AlarmRate(11, 5)
	if err != nil || rate != 1 {
		t.Fatalf("rate = %d, %v; want 1 with left endpoint excluded", rate, err)
	}
}

func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	service, logs := mustService(t, testConfig(),
		PointConfig{ID: "emergency", Priority: Emergency},
		PointConfig{ID: "high", Priority: High},
	)
	if _, err := service.Trigger(Operation{At: 10, PointID: "high", Role: Operator}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := service.Trigger(Operation{At: 9, PointID: "high", Role: Operator})
	assertErrorKind(t, err, ClockRewound)
	_, err = service.Trigger(Operation{At: 11, PointID: "missing", Role: Operator})
	assertErrorKind(t, err, PointNotFound)
	_, err = service.Disable(Operation{At: 12, PointID: "high", Role: Operator, Ticket: "T"})
	assertErrorKind(t, err, PermissionDenied)
	_, err = service.Acknowledge(Operation{At: 13, PointID: "emergency", Role: Operator})
	assertErrorKind(t, err, StateNotAllowed)
	_, err = service.Suppress(Operation{At: 14, PointID: "emergency", Role: Engineer, Duration: 1, Reason: "x"})
	assertErrorKind(t, err, StateNotAllowed)
	_, err = service.Suppress(Operation{At: 15, PointID: "high", Role: Engineer, Duration: 11, Reason: "x"})
	assertErrorKind(t, err, DurationLimitExceeded)

	list := mustResultList(t, service, 15)
	if len(list) != 1 || list[0].State != ActiveUnacknowledged {
		t.Fatalf("list = %+v; rejected operations changed state", list)
	}
	if !bytes.Contains(logs.Bytes(), []byte("ClockRewound")) && !bytes.Contains(logs.Bytes(), []byte("before last clock")) {
		t.Fatalf("logs missing rejection basis:\n%s", logs.String())
	}
}

func TestManualSuppressionPeriodDoesNotCountChatter(t *testing.T) {
	service, _ := mustService(t, testConfig(), PointConfig{ID: "high", Priority: High})
	mustManualSequence := func(at int64, action func(Operation) (Result, error), op Operation) {
		t.Helper()
		op.At = at
		op.PointID = "high"
		if _, err := action(op); err != nil {
			t.Fatalf("unexpected error at %d: %v", at, err)
		}
	}

	mustManualSequence(1, service.Trigger, Operation{Role: Operator})
	mustManualSequence(2, service.ReturnToNormal, Operation{Role: Operator})
	mustManualSequence(3, service.Suppress, Operation{Role: Engineer, Duration: 10, Reason: "maintenance"})
	mustManualSequence(4, service.Trigger, Operation{Role: Operator})
	mustManualSequence(5, service.ReturnToNormal, Operation{Role: Operator})
	mustManualSequence(6, service.Trigger, Operation{Role: Operator})
	mustManualSequence(7, service.ReleaseSuppression, Operation{Role: Engineer})
	assertActiveIDs(t, service, 7, []string{"high"})

	mustManualSequence(8, service.Acknowledge, Operation{Role: Operator})
	mustManualSequence(9, service.Trigger, Operation{Role: Operator})
	assertActiveIDs(t, service, 9, []string{"high"})
}
