package locker

import (
	"bytes"
	"errors"
	"testing"
)

func testCfg() Config {
	return Config{
		FreeStorage:   10,
		BillingPeriod: 5,
		FeePerPeriod:  2,
		FeeCap:        9,
		MaxStorage:    100,
		CodeCooldown:  20,
		CodeCount:     10,
	}
}

func stdCells() []Cell {
	return []Cell{
		{ID: 1, Size: SizeSmall},
		{ID: 2, Size: SizeSmall},
		{ID: 3, Size: SizeMedium},
		{ID: 4, Size: SizeLarge},
	}
}

func newTestCabinet(t *testing.T) *Cabinet {
	t.Helper()
	c, err := NewCabinet(stdCells(), testCfg(), NewTextLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatalf("NewCabinet: %v", err)
	}
	return c
}

func TestFeeBoundaries(t *testing.T) {
	cfg := testCfg() // free=10, period=5, unit=2, cap=9
	cases := []struct {
		elapsed int64
		want    int64
	}{
		{9, 0}, {10, 0}, // 恰好等于免费时长不收费
		{11, 0}, {14, 0}, // 超出不足一个完整周期不收
		{15, 2}, // 恰好满 1 个周期
		{20, 4}, // 恰好满 2 个周期
		{21, 4}, // 不足下一周期
		{30, 8}, // 恰好满 4 个周期
		{35, 9}, // 5 周期本应为 10，封顶 9
		{1000, 9},
	}
	for _, tc := range cases {
		got := storageFee(cfg, 0, tc.elapsed)
		if got != tc.want {
			t.Errorf("elapsed=%d fee=%d want %d", tc.elapsed, got, tc.want)
		}
	}
}

func TestTimeoutBoundary(t *testing.T) {
	c := newTestCabinet(t)
	r, err := c.Deposit(0, "A1", SizeSmall, "13800001234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pickup(10, r.Code, "13800001234"); err != nil {
		t.Fatalf("pickup at limit-1 should succeed: %v", err)
	}

	r2, err := c.Deposit(11, "A2", SizeSmall, "13800001234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pickup(111, r2.Code, "13800001234"); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("pickup at exact limit want ErrTimedOut, got %v", err)
	}
}

func TestCellSelectionExactSizeAndOneTierUp(t *testing.T) {
	c := newTestCabinet(t)
	r1, err := c.Deposit(0, "T1", SizeSmall, "13800000001")
	if err != nil || r1.Cell != 1 {
		t.Fatalf("small got cell=%d err=%v, want 1", r1.Cell, err)
	}
	r2, err := c.Deposit(1, "T2", SizeSmall, "13800000002")
	if err != nil || r2.Cell != 2 {
		t.Fatalf("second small got cell=%d err=%v, want 2", r2.Cell, err)
	}
	r3, err := c.Deposit(2, "T3", SizeSmall, "13800000003")
	if err != nil || r3.Cell != 3 {
		t.Fatalf("upgraded small got cell=%d err=%v, want 3", r3.Cell, err)
	}
	r4, err := c.Deposit(3, "T4", SizeMedium, "13800000004")
	if err != nil || r4.Cell != 4 {
		t.Fatalf("medium got cell=%d err=%v, want 4", r4.Cell, err)
	}
	if _, err := c.Deposit(5, "T6", SizeLarge, "13800000006"); !errors.Is(err, ErrAllFittingBusy) {
		t.Fatalf("want ErrAllFittingBusy, got %v", err)
	}
}

func TestNoFittingCellPrecedesBusy(t *testing.T) {
	c, err := NewCabinet([]Cell{{ID: 7, Size: SizeSmall}}, testCfg(), NewTextLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Deposit(0, "B1", SizeLarge, "13800000000"); !errors.Is(err, ErrNoFittingCell) {
		t.Fatalf("want ErrNoFittingCell, got %v", err)
	}
}

func TestDuplicateAndClockRollback(t *testing.T) {
	c := newTestCabinet(t)
	if _, err := c.Deposit(5, "D1", SizeSmall, "13800000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Deposit(5, "D1", SizeSmall, "13800000002"); !errors.Is(err, ErrDuplicateTracking) {
		t.Fatalf("want duplicate, got %v", err)
	}
	if _, err := c.Deposit(4, "D2", SizeSmall, "13800000003"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want clock rollback, got %v", err)
	}
	if got := c.Clock(); got != 5 {
		t.Fatalf("clock=%d want 5 (rejections must not move clock)", got)
	}
}

func TestCodeCooldownExactAndOneSecondBefore(t *testing.T) {
	cfg := testCfg()
	cfg.CodeCount = 1
	c, err := NewCabinet(
		[]Cell{{ID: 1, Size: SizeSmall}, {ID: 2, Size: SizeSmall}},
		cfg, NewTextLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Deposit(0, "K1", SizeSmall, "13800000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pickup(1, r.Code, "13800000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Deposit(20, "K2", SizeSmall, "13800000002"); !errors.Is(err, ErrNoCodeAvailable) {
		t.Fatalf("one second before cooldown ready want ErrNoCodeAvailable, got %v", err)
	}
	r2, err := c.Deposit(21, "K3", SizeSmall, "13800000003")
	if err != nil || r2.Code != 1 {
		t.Fatalf("exact cooldown reuse want code 1, got code=%d err=%v", r2.Code, err)
	}
}

func TestThreeMismatchesLockAndUnlock(t *testing.T) {
	c := newTestCabinet(t)
	r, err := c.Deposit(0, "L1", SizeSmall, "13800009999")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Pickup(int64(i+1), r.Code, "13800000000"); !errors.Is(err, ErrPhoneMismatch) {
			t.Fatalf("mismatch %d want ErrPhoneMismatch, got %v", i+1, err)
		}
	}
	if got := c.Clock(); got != 0 {
		t.Fatalf("clock=%d want 0 after mismatches", got)
	}
	if _, err := c.Pickup(2, r.Code, "13800009999"); !errors.Is(err, ErrParcelLocked) {
		t.Fatalf("want locked even with correct phone, got %v", err)
	}
	if err := c.Unlock(3, "L1"); err != nil {
		t.Fatal(err)
	}
	if tracking, err := c.Pickup(4, r.Code, "13800009999"); err != nil || tracking != "L1" {
		t.Fatalf("post-unlock pickup tracking=%q err=%v", tracking, err)
	}
}

func TestTwoMismatchesThenSuccessResets(t *testing.T) {
	c := newTestCabinet(t)
	r, err := c.Deposit(0, "L2", SizeSmall, "13800008888")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := c.Pickup(0, r.Code, "13800000000"); !errors.Is(err, ErrPhoneMismatch) {
			t.Fatal(err)
		}
	}
	if _, err := c.Pickup(0, r.Code, "13800008888"); err != nil {
		t.Fatalf("successful pickup after 2 mismatches: %v", err)
	}
}

func TestPayAccruesAndTopUp(t *testing.T) {
	c := newTestCabinet(t)
	r, err := c.Deposit(0, "F1", SizeSmall, "13800007777")
	if err != nil {
		t.Fatal(err)
	}
	if due, _ := c.Due(15, "F1"); due != 2 {
		t.Fatalf("due at 15 = %d want 2", due)
	}
	if err := c.Pay(15, "F1", 2); err != nil {
		t.Fatal(err)
	}
	if due, _ := c.Due(20, "F1"); due != 2 {
		t.Fatalf("due at 20 after payment = %d want 2", due)
	}
	if _, err := c.Pickup(20, r.Code, "13800007777"); !errors.Is(err, ErrUnpaidFee) {
		t.Fatalf("want unpaid fee before top-up, got %v", err)
	}
	if err := c.Pay(20, "F1", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pickup(20, r.Code, "13800007777"); err != nil {
		t.Fatalf("pickup after top-up: %v", err)
	}
}

func TestRecycleOnlyTimedOutAndCellReuse(t *testing.T) {
	c := newTestCabinet(t)
	r, err := c.Deposit(0, "R1", SizeSmall, "13800006666")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Recycle(99, "R1"); !errors.Is(err, ErrNotTimedOut) {
		t.Fatalf("recycle before timeout want ErrNotTimedOut, got %v", err)
	}
	if _, err := c.Pickup(100, r.Code, "13800006666"); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("want timeout at exact limit, got %v", err)
	}
	if err := c.Recycle(100, "R1"); err != nil {
		t.Fatalf("recycle at exact limit: %v", err)
	}
	if _, ok := c.Occupied()["R1"]; ok {
		t.Fatal("recycled parcel still occupied")
	}
	// 码在 t=100 失效；冷却 20，t=120 恰好满；格口 1 立即可用。
	r2, err := c.Deposit(120, "R2", SizeSmall, "13800005555")
	if err != nil || r2.Cell != 1 || r2.Code != 1 {
		t.Fatalf("after recycle want cell 1/code 1, got cell=%d code=%d err=%v",
			r2.Cell, r2.Code, err)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	c := newTestCabinet(t)
	r, err := c.Deposit(0, "M1", SizeSmall, "13800004444")
	if err != nil {
		t.Fatal(err)
	}
	before := c.Occupied()
	// 参数非法、时钟回退、码不存在：状态与时钟均不变。
	if _, err := c.Pickup(0, Code(999), "13800004444"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("want code not found, got %v", err)
	}
	if _, err := c.Pickup(-1, r.Code, "13800004444"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want invalid param, got %v", err)
	}
	after := c.Occupied()
	if len(before) != len(after) || after["M1"] != before["M1"] {
		t.Fatalf("state changed by rejected op: before=%v after=%v", before, after)
	}
	if c.Clock() != 0 {
		t.Fatalf("clock moved: %d", c.Clock())
	}
}
