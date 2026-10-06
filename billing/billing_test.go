package billing

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, n int) *Biller {
	t.Helper()
	b, err := NewBiller(0, n)
	if err != nil {
		t.Fatalf("NewBiller: %v", err)
	}
	return b
}

func errCode(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var be *Error
	if errors.As(err, &be) {
		return be.Code
	}
	return "non_billing_error"
}

func submitOK(t *testing.T, b *Biller, s Sample) {
	t.Helper()
	if err := b.Submit(s); err != nil {
		t.Fatalf("submit %+v: unexpected error %v", s, err)
	}
}

func submitExpect(t *testing.T, b *Biller, s Sample, want ErrorCode) {
	t.Helper()
	if got := errCode(b.Submit(s)); got != want {
		t.Fatalf("submit %+v: got %q want %q", s, got, want)
	}
}

// TestDiscardJump 覆盖 K=19/20/39/40 时 floor(K*5%) 的跳变：0/1/1/2。
// 第 i 个槽位有效值为 i+1，丢弃 d 个最大值后剩余最大值为 K-d。
func TestDiscardJump(t *testing.T) {
	cases := []struct {
		k        int64
		wantRate int64
	}{
		{19, 19},
		{20, 19},
		{39, 38},
		{40, 38},
	}
	for _, c := range cases {
		b := mustNew(t, int(c.k))
		for i := int64(0); i < c.k; i++ {
			submitOK(t, b, Sample{Slot: int(i), Inbound: i + 1, Outbound: i + 1, Version: 1})
		}
		got, err := b.CurrentRate()
		if err != nil {
			t.Fatalf("K=%d: %v", c.k, err)
		}
		if got != c.wantRate {
			t.Fatalf("K=%d: rate=%d want %d", c.k, got, c.wantRate)
		}
	}
}

// TestTiesOccupySlots 并列值各自独立占位。
func TestTiesOccupySlots(t *testing.T) {
	b := mustNew(t, 40)
	for i := 0; i < 40; i++ {
		submitOK(t, b, Sample{Slot: i, Inbound: 100, Outbound: 100, Version: 1})
	}
	if rate, err := b.CurrentRate(); err != nil || rate != 100 {
		t.Fatalf("ties all: rate=%d err=%v, want 100", rate, err)
	}

	// 38 个 100、2 个 200；丢弃 2 个恰好丢掉两个 200，速率降为 100。
	b2 := mustNew(t, 40)
	for i := 0; i < 40; i++ {
		v := int64(100)
		if i >= 38 {
			v = 200
		}
		submitOK(t, b2, Sample{Slot: i, Inbound: v, Outbound: v, Version: 1})
	}
	if rate, _ := b2.CurrentRate(); rate != 100 {
		t.Fatalf("ties partial: rate=%d want 100", rate)
	}

	// K=20 只有一个 200：丢弃 1 个后速率为 100。
	b3 := mustNew(t, 20)
	for i := 0; i < 20; i++ {
		v := int64(100)
		if i == 0 {
			v = 200
		}
		submitOK(t, b3, Sample{Slot: i, Inbound: v, Outbound: v, Version: 1})
	}
	if rate, _ := b3.CurrentRate(); rate != 100 {
		t.Fatalf("ties single: rate=%d want 100", rate)
	}
}

// TestHigherVersionLowersRate 更高版本覆盖导致计费速率下降。
func TestHigherVersionLowersRate(t *testing.T) {
	b := mustNew(t, 20)
	submitOK(t, b, Sample{Slot: 0, Inbound: 900, Outbound: 900, Version: 1})
	for i := 1; i < 20; i++ {
		submitOK(t, b, Sample{Slot: i, Inbound: 10, Outbound: 10, Version: 1})
	}
	if rate, _ := b.CurrentRate(); rate != 10 {
		t.Fatalf("initial rate=%d want 10", rate)
	}
	submitOK(t, b, Sample{Slot: 1, Inbound: 900, Outbound: 900, Version: 2})
	if rate, _ := b.CurrentRate(); rate != 900 {
		t.Fatalf("two max rate=%d want 900", rate)
	}
	submitOK(t, b, Sample{Slot: 1, Inbound: 10, Outbound: 10, Version: 3})
	if rate, _ := b.CurrentRate(); rate != 10 {
		t.Fatalf("after override rate=%d want 10", rate)
	}
}

// TestWithdrawKeepsVersionFloor 撤回后槽位缺失，但版本门槛保留。
func TestWithdrawKeepsVersionFloor(t *testing.T) {
	b := mustNew(t, 4)
	submitOK(t, b, Sample{Slot: 1, Inbound: 5, Outbound: 7, Version: 3})

	if got := errCode(b.Withdraw(1, 2)); got != ErrVersionMismatch {
		t.Fatalf("withdraw wrong version: %q", got)
	}
	if err := b.Withdraw(1, 3); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	o := b.Overview()
	if o.ReceivedSlots != 0 || o.MissingSlots != 4 || o.RateDefined {
		t.Fatalf("overview after withdraw: %+v", o)
	}
	if got := errCode(b.Withdraw(1, 3)); got != ErrNotFound {
		t.Fatalf("withdraw missing: %q", got)
	}
	submitExpect(t, b, Sample{Slot: 1, Inbound: 1, Outbound: 1, Version: 3}, ErrStaleSample)
	submitExpect(t, b, Sample{Slot: 1, Inbound: 1, Outbound: 1, Version: 2}, ErrStaleSample)
	submitOK(t, b, Sample{Slot: 1, Inbound: 1, Outbound: 1, Version: 4})
}

// TestIdempotentAndConflict 同版本同内容幂等、同版本不同内容冲突。
func TestIdempotentAndConflict(t *testing.T) {
	b := mustNew(t, 2)
	s := Sample{Slot: 0, Inbound: 100, Outbound: 200, Version: 5}
	submitOK(t, b, s)
	submitOK(t, b, s)
	if o := b.Overview(); o.ReceivedSlots != 1 {
		t.Fatalf("idempotent duplicate changed state: %+v", o)
	}
	submitExpect(t, b, Sample{Slot: 0, Inbound: 100, Outbound: 201, Version: 5}, ErrVersionConflict)
	submitExpect(t, b, Sample{Slot: 0, Inbound: 99, Outbound: 200, Version: 5}, ErrVersionConflict)
	if rate, _ := b.CurrentRate(); rate != 200 {
		t.Fatalf("state changed after conflict: rate=%d", rate)
	}
	submitOK(t, b, Sample{Slot: 0, Inbound: 1, Outbound: 2, Version: 6})
}

// TestInboundOutboundMax 入向出向互为较大。
func TestInboundOutboundMax(t *testing.T) {
	b := mustNew(t, 20)
	submitOK(t, b, Sample{Slot: 0, Inbound: 300, Outbound: 100, Version: 1})
	submitOK(t, b, Sample{Slot: 1, Inbound: 100, Outbound: 400, Version: 1})
	for i := 2; i < 20; i++ {
		submitOK(t, b, Sample{Slot: i, Inbound: 50, Outbound: 60, Version: 1})
	}
	// 400 被丢弃（唯一最大值），速率为 300。
	if rate, _ := b.CurrentRate(); rate != 300 {
		t.Fatalf("max(in,out): rate=%d want 300", rate)
	}
}

// TestToleranceBoundary N=10000、容忍 1% 时恰允许缺失 100，差一即拒绝。
func TestToleranceBoundary(t *testing.T) {
	const n = 10000
	const tol = 100

	build := func(valid int) *Biller {
		b := mustNew(t, n)
		for i := 0; i < valid; i++ {
			submitOK(t, b, Sample{Slot: i, Inbound: 7, Outbound: 9, Version: 1})
		}
		return b
	}

	b := build(n - tol)
	res, err := b.Settle(5, tol)
	if err != nil {
		t.Fatalf("boundary settle: %v", err)
	}
	if res.MissingSlots != 100 || res.ValidSlots != n-100 || res.BilledRate != 9 || res.Base != 9 {
		t.Fatalf("boundary result: %+v", res)
	}

	b2 := build(n - tol - 1)
	if _, e := b2.Settle(5, tol); errCode(e) != ErrInsufficientData {
		t.Fatalf("one over boundary: %q", errCode(e))
	}
	if b2.Overview().Settled {
		t.Fatal("insufficient settle must not seal period")
	}
	submitOK(t, b2, Sample{Slot: n - tol - 1, Inbound: 9, Outbound: 9, Version: 1})
	res2, err := b2.Settle(5, tol)
	if err != nil || res2.MissingSlots != 100 {
		t.Fatalf("retry settle: %+v err=%v", res2, err)
	}

	b3 := build(n - 1)
	if _, e := b3.Settle(0, 0); errCode(e) != ErrInsufficientData {
		t.Fatalf("zero tolerance: %q", errCode(e))
	}
}

// TestBaseIsMax 基数取承诺速率与计费速率的较大者。
func TestBaseIsMax(t *testing.T) {
	b := mustNew(t, 20)
	for i := 0; i < 20; i++ {
		submitOK(t, b, Sample{Slot: i, Inbound: 100, Outbound: 100, Version: 1})
	}
	res, _ := b.Settle(500, 10000)
	if res.Base != 500 || res.BilledRate != 100 {
		t.Fatalf("committed wins: %+v", res)
	}

	b2 := mustNew(t, 20)
	for i := 0; i < 20; i++ {
		submitOK(t, b2, Sample{Slot: i, Inbound: 800, Outbound: 800, Version: 1})
	}
	res2, _ := b2.Settle(500, 10000)
	if res2.Base != 800 {
		t.Fatalf("billed wins: %+v", res2)
	}
}

// TestSettleRepeatsAndSeals 重复结算恒为首次结果；封存后写操作报已结算。
func TestSettleRepeatsAndSeals(t *testing.T) {
	b := mustNew(t, 20)
	for i := 0; i < 20; i++ {
		submitOK(t, b, Sample{Slot: i, Inbound: 100, Outbound: 100, Version: 1})
	}
	first, err := b.Settle(200, 5000)
	if err != nil || first.Repeated {
		t.Fatalf("first settle: %+v %v", first, err)
	}
	second, err := b.Settle(999, 0)
	if err != nil {
		t.Fatalf("repeat settle: %v", err)
	}
	if !second.Repeated ||
		second.BilledRate != first.BilledRate || second.Base != first.Base ||
		second.ValidSlots != first.ValidSlots || second.MissingSlots != first.MissingSlots {
		t.Fatalf("repeat settle mismatch: %+v vs %+v", second, first)
	}
	submitExpect(t, b, Sample{Slot: 0, Inbound: 1, Outbound: 1, Version: 2}, ErrAlreadySettled)
	if got := errCode(b.Withdraw(0, 1)); got != ErrAlreadySettled {
		t.Fatalf("withdraw after settle: %q", got)
	}
	if rate, err := b.CurrentRate(); err != nil || rate != first.BilledRate {
		t.Fatalf("query after settle: %d %v", rate, err)
	}
	if o := b.Overview(); !o.Settled || o.Rate != first.BilledRate {
		t.Fatalf("overview after settle: %+v", o)
	}
}

// TestInvalidArguments 参数非法覆盖各类越界。
func TestInvalidArguments(t *testing.T) {
	if _, err := NewBiller(0, 0); errCode(err) != ErrInvalidArgument {
		t.Fatalf("n=0: %v", err)
	}
	if _, err := NewBiller(0, MaxSlotCount+1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("n too large: %v", err)
	}
	b := mustNew(t, 10)
	bad := []Sample{
		{Slot: -1, Inbound: 1, Outbound: 1, Version: 1},
		{Slot: 10, Inbound: 1, Outbound: 1, Version: 1},
		{Slot: 0, Inbound: -1, Outbound: 1, Version: 1},
		{Slot: 0, Inbound: MaxRate + 1, Outbound: 1, Version: 1},
		{Slot: 0, Inbound: 1, Outbound: -1, Version: 1},
		{Slot: 0, Inbound: 1, Outbound: MaxRate + 1, Version: 1},
		{Slot: 0, Inbound: 1, Outbound: 1, Version: 0},
		{Slot: 0, Inbound: 1, Outbound: 1, Version: -3},
	}
	for _, s := range bad {
		if got := errCode(b.Submit(s)); got != ErrInvalidArgument {
			t.Fatalf("submit %+v: %q", s, got)
		}
	}
	if got := errCode(b.Withdraw(-1, 1)); got != ErrInvalidArgument {
		t.Fatalf("withdraw slot -1: %q", got)
	}
	if got := errCode(b.Withdraw(10, 1)); got != ErrInvalidArgument {
		t.Fatalf("withdraw slot 10: %q", got)
	}
	if got := errCode(b.Withdraw(0, 0)); got != ErrInvalidArgument {
		t.Fatalf("withdraw version 0: %q", got)
	}
	if _, got := b.Settle(-1, 0); errCode(got) != ErrInvalidArgument {
		t.Fatalf("settle rate -1: %q", got)
	}
	if _, got := b.Settle(MaxRate+1, 0); errCode(got) != ErrInvalidArgument {
		t.Fatalf("settle rate huge: %q", got)
	}
	if _, got := b.Settle(0, -1); errCode(got) != ErrInvalidArgument {
		t.Fatalf("settle tol -1: %q", got)
	}
	if _, got := b.Settle(0, MaxTolerance+1); errCode(got) != ErrInvalidArgument {
		t.Fatalf("settle tol huge: %q", got)
	}
}

// TestNoSamples 无有效槽位时查询与结算的行为。
func TestNoSamples(t *testing.T) {
	b := mustNew(t, 10)
	if _, e := b.CurrentRate(); errCode(e) != ErrNoSamples {
		t.Fatalf("empty query: %q", errCode(e))
	}
	o := b.Overview()
	if o.RateDefined || o.ReceivedSlots != 0 || o.MissingSlots != 10 {
		t.Fatalf("empty overview: %+v", o)
	}
	// 容忍 100% 且无采样：通过数据不足检查后报无采样，且不封存。
	if _, e := b.Settle(0, MaxTolerance); errCode(e) != ErrNoSamples {
		t.Fatalf("settle empty with full tolerance: %q", errCode(e))
	}
	if b.Overview().Settled {
		t.Fatal("no-samples settle must not seal period")
	}
}

// TestRejectPriority 错误固定优先级的直接核对：
// 非法 > 已结算 > 冲突/过期/版本不符 > 不存在 > 无采样/数据不足。
func TestRejectPriority(t *testing.T) {
	b := mustNew(t, 2)
	submitOK(t, b, Sample{Slot: 0, Inbound: 10, Outbound: 10, Version: 2})

	// 已结算前先封存：结算成功。
	if _, err := b.Settle(0, MaxTolerance); err != nil {
		t.Fatalf("seal: %v", err)
	}
	// 封存后即使参数也非法，仍优先报参数非法。
	if got := errCode(b.Submit(Sample{Slot: 99, Inbound: 1, Outbound: 1, Version: 1})); got != ErrInvalidArgument {
		t.Fatalf("invalid beats settled (submit): %q", got)
	}
	if got := errCode(b.Submit(Sample{Slot: 0, Inbound: 1, Outbound: 1, Version: 3})); got != ErrAlreadySettled {
		t.Fatalf("settled beats stale (submit): %q", got)
	}

	// 未封存实例：版本冲突优先于过期（同版本不同内容）。
	b2 := mustNew(t, 2)
	submitOK(t, b2, Sample{Slot: 0, Inbound: 10, Outbound: 10, Version: 5})
	submitExpect(t, b2, Sample{Slot: 0, Inbound: 11, Outbound: 10, Version: 5}, ErrVersionConflict)
	submitExpect(t, b2, Sample{Slot: 0, Inbound: 10, Outbound: 10, Version: 4}, ErrStaleSample)

	// 撤回：版本不符优先于不存在（空槽位传任何版本都先判不存在——
	// 因为无“当前版本”可比；非空槽位版本不对报版本不符）。
	if got := errCode(b2.Withdraw(1, 1)); got != ErrNotFound {
		t.Fatalf("withdraw empty: %q", got)
	}
	if got := errCode(b2.Withdraw(0, 4)); got != ErrVersionMismatch {
		t.Fatalf("withdraw wrong version: %q", got)
	}
}
