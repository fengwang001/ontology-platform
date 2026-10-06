package pvbinding

import (
	"errors"
	"fmt"
	"testing"
)

type operationLog struct {
	t      *testing.T
	number int
}

func newOperationLog(t *testing.T) *operationLog {
	return &operationLog{t: t}
}

func (l *operationLog) record(input string, err error, basis string) {
	l.number++
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	l.t.Logf("操作%03d | 输入=%s | 实际输出=%s | 判定依据=%s", l.number, input, result, basis)
}

func requireOK(t *testing.T, controller *Controller, log *operationLog, input string, err error, basis string) {
	t.Helper()
	log.record(input, err, basis)
	if err != nil {
		t.Fatalf("%s: %v", input, err)
	}
	if err := controller.CheckConsistency(); err != nil {
		t.Fatalf("%s: consistency: %v", input, err)
	}
}

func requireCode(t *testing.T, controller *Controller, log *operationLog, input string, err error, code ErrorCode, basis string) {
	t.Helper()
	log.record(input, err, basis)
	var bindingErr Error
	if !errors.As(err, &bindingErr) || bindingErr.Code != code {
		t.Fatalf("%s: expected %d, got %v", input, code, err)
	}
	if err := controller.CheckConsistency(); err != nil {
		t.Fatalf("%s: consistency: %v", input, err)
	}
}

func testVolume(name string, capacity int64, class string) VolumeSpec {
	return VolumeSpec{
		Name:          name,
		Capacity:      capacity,
		StorageClass:  class,
		AccessModes:   []string{"RWO"},
		ReclaimPolicy: ReclaimRetain,
	}
}

func testClaim(name string, capacity int64, class string, mode BindingMode) ClaimSpec {
	return ClaimSpec{
		Name:              name,
		RequestedCapacity: capacity,
		StorageClass:      class,
		AccessModes:       []string{"RWO"},
		BindingMode:       mode,
	}
}

func boundVolume(t *testing.T, controller *Controller, name string) string {
	t.Helper()
	status, ok := controller.GetClaim(name)
	if !ok {
		t.Fatalf("claim %s missing", name)
	}
	return status.BoundVolumeName
}

func describe(value any) string {
	return fmt.Sprintf("%+v", value)
}
