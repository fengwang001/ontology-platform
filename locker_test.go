package smartlocker

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type testLogger struct {
	lines []string
}

func (logger *testLogger) Printf(format string, args ...any) {
	logger.lines = append(logger.lines, fmt.Sprintf(format, args...))
}

func testConfig() Config {
	return Config{
		Cells: map[string]Size{
			"s1": Small,
			"m1": Medium,
			"m2": Medium,
			"l1": Large,
		},
		FeePolicy: FeePolicy{
			FreeDuration: 10,
			Period:       5,
			PeriodFee:    2,
			MaximumFee:   6,
		},
		StorageLimit:   30,
		CodeCooldown:   7,
		WrongCodeLimit: 3,
	}
}

func parcel(waybill string, size Size) Parcel {
	return Parcel{Waybill: waybill, Phone: "13800001234", Size: size}
}

func errorCode(t *testing.T, err error) string {
	t.Helper()
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		return operationErr.Code
	}
	t.Fatalf("expected OperationError, got %v", err)
	return ""
}

func requireError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s", want)
	}
	if got := errorCode(t, err); got != want {
		t.Fatalf("error = %s, want %s", got, want)
	}
}

func TestAllocationExactSizeAndOneLarger(t *testing.T) {
	locker, err := NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	first, err := locker.Deposit(1, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}
	if first.CellID != "s1" {
		t.Fatalf("cell = %s, want s1", first.CellID)
	}

	second, err := locker.Deposit(2, parcel("w2", Small))
	if err != nil {
		t.Fatal(err)
	}
	if second.CellID != "m1" {
		t.Fatalf("cell = %s, want m1", second.CellID)
	}

	medium, err := locker.Deposit(3, parcel("w3", Medium))
	if err != nil {
		t.Fatal(err)
	}
	if medium.CellID != "m2" {
		t.Fatalf("cell = %s, want m2", medium.CellID)
	}
}

func TestCapacityErrorsPrioritizeNoCompatibleCell(t *testing.T) {
	cfg := testConfig()
	cfg.Cells = map[string]Size{"s1": Small}
	locker, err := NewLocker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.Deposit(1, parcel("w1", Large))
	requireError(t, err, ErrNoCompatibleCell.Code)

	_, err = locker.Deposit(2, parcel("w2", Small))
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.Deposit(3, parcel("w3", Small))
	requireError(t, err, ErrAllFitCellsOccupied.Code)
}

func TestCodeCooldownBoundary(t *testing.T) {
	locker, err := NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	first, err := locker.Deposit(0, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}
	second, err := locker.Deposit(1, parcel("w2", Medium))
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.Pickup(3, first.Code, "13800001234")
	if err != nil {
		t.Fatal(err)
	}

	early, err := locker.Deposit(9, parcel("w3", Small))
	if err != nil {
		t.Fatal(err)
	}
	if early.Code == first.Code {
		t.Fatalf("released code reused one second early")
	}

	exact, err := locker.Deposit(10, parcel("w4", Large))
	if err != nil {
		t.Fatal(err)
	}
	if exact.Code != first.Code {
		t.Fatalf("code = %s, want reused %s at exact cooldown", exact.Code, first.Code)
	}
	if second.Code == first.Code || exact.Code == second.Code {
		t.Fatalf("active codes were reused: %s", second.Code)
	}
}

func TestFeeBoundariesAndSupplementaryPayment(t *testing.T) {
	locker, err := NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := locker.Deposit(0, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = locker.Pickup(10, result.Code, "13800001234"); err != nil {
		t.Fatalf("exact free duration should be free: %v", err)
	}

	locker, err = NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.StorageLimit = 100
	locker, err = NewLocker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err = locker.Deposit(0, parcel("w2", Small))
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.Pickup(15, result.Code, "13800001234")
	requireError(t, err, ErrFeeDue.Code)

	_, err = locker.Pay(15, "w2", 1)
	requireError(t, err, ErrWrongPaymentAmount.Code)
	if _, err = locker.Pay(15, "w2", 2); err != nil {
		t.Fatal(err)
	}
	_, err = locker.Pickup(20, result.Code, "13800001234")
	requireError(t, err, ErrFeeDue.Code)
	if _, err = locker.Pay(20, "w2", 2); err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Pay(40, "w2", 2); err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Pickup(40, result.Code, "13800001234"); err != nil {
		t.Fatal(err)
	}
}

func TestStorageLimitBoundaryAndRecycle(t *testing.T) {
	locker, err := NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := locker.Deposit(0, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}

	_, err = locker.Recycle(29, "w1")
	requireError(t, err, ErrParcelNotTimedOut.Code)
	if _, err = locker.Pay(29, "w1", 6); err != nil {
		t.Fatalf("pay before limit boundary failed: %v", err)
	}
	if _, err = locker.Pickup(29, result.Code, "13800001234"); err != nil {
		t.Fatalf("pickup one second before limit failed: %v", err)
	}

	result, err = locker.Deposit(30, parcel("w2", Small))
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.Pickup(60, result.Code, "13800001234")
	requireError(t, err, ErrParcelTimedOut.Code)
	_, err = locker.Recycle(60, "w2")
	if err != nil {
		t.Fatal(err)
	}
	reused, err := locker.Deposit(61, parcel("w3", Small))
	if err != nil {
		t.Fatal(err)
	}
	if reused.CellID != result.CellID {
		t.Fatalf("cell = %s, want immediately reusable %s", reused.CellID, result.CellID)
	}
}

func TestWrongPhoneLocksAndSuccessClears(t *testing.T) {
	locker, err := NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := locker.Deposit(0, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		_, err = locker.Pickup(5, result.Code, "13900009999")
		requireError(t, err, ErrPhoneMismatch.Code)
	}
	if locker.lastTime != 0 {
		t.Fatalf("phone mismatch changed clock to %d", locker.lastTime)
	}
	if _, err = locker.Pickup(5, result.Code, "13900009999"); err == nil || errorCode(t, err) != ErrPhoneMismatch.Code {
		t.Fatalf("third mismatch = %v, want phone mismatch with side-effect lock", err)
	}
	_, err = locker.Pickup(6, result.Code, "13800001234")
	requireError(t, err, ErrParcelLocked.Code)

	if _, err = locker.Unlock(7, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Pickup(8, result.Code, "13800001234"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedOperationDoesNotChangeState(t *testing.T) {
	cfg := testConfig()
	logger := &testLogger{}
	cfg.Logger = logger
	locker, err := NewLocker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := locker.Deposit(5, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}

	_, err = locker.Deposit(3, parcel("w2", Small))
	requireError(t, err, ErrClockRewound.Code)
	_, err = locker.Deposit(6, parcel("w1", Medium))
	requireError(t, err, ErrDuplicateWaybill.Code)
	_, err = locker.Pickup(6, "missing", "13800001234")
	requireError(t, err, ErrCodeNotFound.Code)

	snapshot := locker.Snapshot()
	if snapshot.LastTime != 5 || len(snapshot.Active) != 1 {
		t.Fatalf("state changed after rejects: %+v", snapshot)
	}
	if _, err = locker.Pickup(6, result.Code, "13800001234"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logger.lines[len(logger.lines)-1], "pickup accept") {
		t.Fatalf("last log = %q", logger.lines[len(logger.lines)-1])
	}
}

func TestConcurrentPickupExactlyOnce(t *testing.T) {
	locker, err := NewLocker(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := locker.Deposit(0, parcel("w1", Small))
	if err != nil {
		t.Fatal(err)
	}

	const workers = 32
	var wait sync.WaitGroup
	successes := make(chan PickupResult, workers)
	errorsCh := make(chan error, workers)
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			pickup, err := locker.Pickup(5, result.Code, "13800001234")
			if err != nil {
				errorsCh <- err
				return
			}
			successes <- pickup
		}()
	}
	close(start)
	wait.Wait()
	close(successes)
	close(errorsCh)

	if len(successes) != 1 {
		t.Fatalf("successes = %d, want 1", len(successes))
	}
	for err := range errorsCh {
		if errorCode(t, err) != ErrCodeNotFound.Code {
			t.Fatalf("concurrent loser error = %v, want code not found", err)
		}
	}
}

func TestAllocationFindProbeBound(t *testing.T) {
	cfg := testConfig()
	cfg.Cells = make(map[string]Size)
	for index := 0; index < 100000; index++ {
		cfg.Cells[fmt.Sprintf("cell-%06d", index)] = Size(index%3 + 1)
	}
	locker, err := NewLocker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1000; index++ {
		if _, err = locker.Deposit(int64(index), parcel(fmt.Sprintf("w%06d", index), Small)); err != nil {
			t.Fatal(err)
		}
	}
	stats := locker.AllocationStats()
	if stats.MaximumFindProbes <= 0 || stats.MaximumFindProbes > 14 {
		t.Fatalf("maximum probes = %d, want bounded small constant", stats.MaximumFindProbes)
	}
}
