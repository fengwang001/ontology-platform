package billing

import (
	"errors"
	"testing"
)

func codeOf(err error) Code {
	var be *Error
	if errors.As(err, &be) {
		return be.Code
	}
	return 0
}

func mustRate(t *testing.T, s *Settler) int64 {
	t.Helper()
	rate, err := s.CurrentRate()
	if err != nil {
		t.Fatalf("CurrentRate 意外失败: %v", err)
	}
	return rate
}

func assertCode(t *testing.T, err error, want Code, ctx string) {
	t.Helper()
	if got := codeOf(err); got != want {
		t.Fatalf("%s: 错误码 = %v, 期望 %v (err=%v)", ctx, got, want, err)
	}
}

// fillSlots 向 start 起 count 个槽位提交入向值为 v、出向为 0、版本 1 的采样。
func fillSlots(t *testing.T, s *Settler, start, count int, v int64) {
	t.Helper()
	for i := 0; i < count; i++ {
		if err := s.Submit(Sample{Slot: start + i, Ingress: v, Version: 1}); err != nil {
			t.Fatalf("提交槽位 %d 值 %d 失败: %v", start+i, v, err)
		}
	}
}

// TestDiscardBoundary 覆盖 K=19/20/39/40 时丢弃数量 floor(K*5/100) 的跳变。
func TestDiscardBoundary(t *testing.T) {
	cases := []struct {
		k       int
		discard int
	}{
		{19, 0},
		{20, 1},
		{39, 1},
		{40, 2},
	}
	for _, c := range cases {
		s := NewSettler(0, 1000)
		for i := 0; i < c.k; i++ {
			if err := s.Submit(Sample{Slot: i, Ingress: int64(i + 1), Version: 1}); err != nil {
				t.Fatalf("K=%d 提交失败: %v", c.k, err)
			}
		}
		want := int64(c.k - c.discard)
		if got := mustRate(t, s); got != want {
			t.Errorf("K=%d: 丢弃 %d 个，计费速率 = %d, 期望 %d", c.k, c.discard, got, want)
		}
	}
}

// TestTiesOccupySlots 验证并列值各自独立占位。
func TestTiesOccupySlots(t *testing.T) {
	s := NewSettler(0, 20)
	fillSlots(t, s, 0, 20, 100)
	if got := mustRate(t, s); got != 100 {
		t.Fatalf("全并列 K=20: 计费速率 = %d, 期望 100", got)
	}

	s = NewSettler(0, 40)
	fillSlots(t, s, 0, 2, 1000)
	fillSlots(t, s, 2, 38, 5)
	if got := mustRate(t, s); got != 5 {
		t.Fatalf("并列最大值恰好被丢尽: 计费速率 = %d, 期望 5", got)
	}

	s = NewSettler(0, 40)
	fillSlots(t, s, 0, 3, 1000)
	fillSlots(t, s, 3, 37, 5)
	if got := mustRate(t, s); got != 1000 {
		t.Fatalf("并列跨越丢弃边界: 计费速率 = %d, 期望 1000", got)
	}
}

// TestHigherVersionLowersRate 验证更高版本覆盖导致计费速率下降。
func TestHigherVersionLowersRate(t *testing.T) {
	s := NewSettler(0, 40)
	fillSlots(t, s, 3, 37, 10)
	fillSlots(t, s, 0, 3, 1000)
	if got := mustRate(t, s); got != 1000 {
		t.Fatalf("覆盖前费率 = %d, 期望 1000", got)
	}
	if err := s.Submit(Sample{Slot: 0, Ingress: 1, Version: 2}); err != nil {
		t.Fatalf("高版本覆盖失败: %v", err)
	}
	if got := mustRate(t, s); got != 10 {
		t.Fatalf("高版本覆盖后费率 = %d, 期望 10（应下降）", got)
	}
}

// TestWithdrawKeepsVersionFloor 验证撤回后槽位回到缺失且版本门槛保留。
func TestWithdrawKeepsVersionFloor(t *testing.T) {
	s := NewSettler(0, 10)
	if err := s.Submit(Sample{Slot: 3, Ingress: 100, Version: 7}); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Withdraw(3, 6), CodeVersionMismatch, "撤回错误版本")
	if err := s.Withdraw(3, 7); err != nil {
		t.Fatalf("撤回失败: %v", err)
	}

	_, err := s.CurrentRate()
	assertCode(t, err, CodeNoSamples, "撤回后查询")
	o := s.Overview()
	if o.ReceivedSlots != 0 || o.MissingSlots != 10 || o.RateDefined {
		t.Fatalf("撤回后概览异常: %+v", o)
	}
	assertCode(t, s.Withdraw(3, 7), CodeNotFound, "撤回已缺失槽位")
	assertCode(t, s.Submit(Sample{Slot: 3, Ingress: 1, Version: 7}), CodeStaleSample, "撤回后提交同版本")
	assertCode(t, s.Submit(Sample{Slot: 3, Ingress: 1, Version: 5}), CodeStaleSample, "撤回后提交低版本")
	if hv, err := s.HighestSeenVersion(3); err != nil || hv != 7 {
		t.Fatalf("highestSeen = %d, %v, 期望 7", hv, err)
	}
	if err := s.Submit(Sample{Slot: 3, Ingress: 1, Version: 8}); err != nil {
		t.Fatalf("撤回后更高版本应被接受: %v", err)
	}
}

// TestMissingToleranceBoundary 验证缺失比例恰在容忍边界与差一槽位。
func TestMissingToleranceBoundary(t *testing.T) {
	// N=20，容忍 25%（2500bp）：missing*10000 <= 2500*20=50000 => missing<=5。
	n, tol := 20, 2500

	s := NewSettler(0, n)
	fillSlots(t, s, 0, n-5, 100)
	r, err := s.Settle(50, tol)
	if err != nil {
		t.Fatalf("缺失恰在边界应成功: %v", err)
	}
	if r.MissingSlots != 5 || r.ValidSlots != 15 {
		t.Fatalf("边界结算计数异常: %+v", r)
	}

	s = NewSettler(0, n)
	fillSlots(t, s, 0, n-6, 100)
	_, err = s.Settle(50, tol)
	assertCode(t, err, CodeInsufficientData, "缺失差一越界")
	if s.IsSettled() {
		t.Fatal("数据不足不应封存周期")
	}
	if err := s.Submit(Sample{Slot: n - 6, Ingress: 100, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settle(50, tol); err != nil {
		t.Fatalf("补齐后重新结算应成功: %v", err)
	}
}

// TestIngressEgressMax 验证入向/出向互为较大时取较大者。
func TestIngressEgressMax(t *testing.T) {
	s := NewSettler(0, 20)
	if err := s.Submit(Sample{Slot: 0, Ingress: 300, Egress: 100, Version: 1}); err != nil {
		t.Fatal(err)
	}
	fillSlots(t, s, 1, 19, 50)
	if got := mustRate(t, s); got != 50 {
		t.Fatalf("入向较大: 费率 = %d, 期望 50", got)
	}

	s = NewSettler(0, 20)
	if err := s.Submit(Sample{Slot: 0, Ingress: 100, Egress: 300, Version: 1}); err != nil {
		t.Fatal(err)
	}
	fillSlots(t, s, 1, 19, 50)
	if got := mustRate(t, s); got != 50 {
		t.Fatalf("出向较大: 费率 = %d, 期望 50", got)
	}
	if err := s.Submit(Sample{Slot: 0, Ingress: 100, Egress: 60, Version: 2}); err != nil {
		t.Fatal(err)
	}
	if got := mustRate(t, s); got != 50 {
		t.Fatalf("翻转大小方向后费率 = %d, 期望 50", got)
	}
}

// TestSettleRepeatAndPostSettleOps 验证重复结算以第一次为准及结算后的提交/撤回。
func TestSettleRepeatAndPostSettleOps(t *testing.T) {
	s := NewSettler(0, 20)
	fillSlots(t, s, 0, 20, 100)
	first, err := s.Settle(80, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if first.Repeated || first.Base != 100 || first.BilledRate != 100 {
		t.Fatalf("首次结算异常: %+v", first)
	}

	second, err := s.Settle(123, 0)
	if err != nil {
		t.Fatalf("重复结算不应报错: %v", err)
	}
	if !second.Repeated || second.Base != 100 || second.BilledRate != 100 ||
		second.CommittedRate != 80 || second.ToleranceBp != 10000 {
		t.Fatalf("重复结算未忠实回放首次结果: %+v", second)
	}

	assertCode(t, s.Submit(Sample{Slot: 0, Ingress: 1, Version: 2}), CodeSettled, "结算后提交")
	assertCode(t, s.Withdraw(0, 1), CodeSettled, "结算后撤回")
	if got := mustRate(t, s); got != first.BilledRate {
		t.Fatalf("结算后查询费率 %d != %d", got, first.BilledRate)
	}
	if o := s.Overview(); !o.Settled || o.BilledRate != first.BilledRate {
		t.Fatalf("结算后概览异常: %+v", o)
	}
}

// TestRejectionOrdering 验证错误固定优先级。
func TestRejectionOrdering(t *testing.T) {
	s := NewSettler(0, 20)

	assertCode(t, s.Submit(Sample{Slot: -1, Version: 0}), CodeInvalidArgument, "提交非法下标")
	assertCode(t, s.Submit(Sample{Slot: 0, Ingress: -1, Version: 1}), CodeInvalidArgument, "入向越界")
	assertCode(t, s.Submit(Sample{Slot: 0, Egress: MaxRate + 1, Version: 1}), CodeInvalidArgument, "出向越界")
	assertCode(t, s.Submit(Sample{Slot: 0, Version: 0}), CodeInvalidArgument, "版本非正")

	_, err := s.Settle(-1, 0)
	assertCode(t, err, CodeInvalidArgument, "承诺速率为负")
	_, err = s.Settle(0, -1)
	assertCode(t, err, CodeInvalidArgument, "容忍比例为负")

	if err := s.Submit(Sample{Slot: 1, Ingress: 10, Version: 5}); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Submit(Sample{Slot: 1, Ingress: 11, Version: 5}), CodeVersionConflict, "等版本不同内容")
	if err := s.Submit(Sample{Slot: 1, Ingress: 10, Version: 5}); err != nil {
		t.Fatalf("幂等重复应成功: %v", err)
	}
	assertCode(t, s.Submit(Sample{Slot: 1, Ingress: 11, Version: 4}), CodeStaleSample, "更低版本")

	// 撤回：槽位缺失时版本不符优先于不存在。
	assertCode(t, s.Withdraw(2, 9), CodeNotFound, "从未存在槽位直接撤回")
	assertCode(t, s.Withdraw(1, 4), CodeVersionMismatch, "错误版本撤回")
	if err := s.Withdraw(1, 5); err != nil {
		t.Fatalf("正确版本撤回应成功: %v", err)
	}

	// 结算后：参数合法的提交/撤回一律报已结算（版本类错误被压过）。
	fillSlots(t, s, 0, 1, 7)
	fillSlots(t, s, 2, 18, 7)
	if err := s.Submit(Sample{Slot: 1, Ingress: 7, Version: 6}); err != nil {
		t.Fatalf("槽位 1 以更高版本补齐失败: %v", err)
	}
	if _, err := s.Settle(0, 10000); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Submit(Sample{Slot: 1, Ingress: 1, Version: 4}), CodeSettled, "结算后低版本仍报已结算")
	assertCode(t, s.Withdraw(1, 1), CodeSettled, "结算后撤回版本不符也报已结算")
	// 结算后重复结算成功，但参数非法仍优先报参数非法。
	_, err = s.Settle(-1, 0)
	assertCode(t, err, CodeInvalidArgument, "重复结算参数非法优先")
}

// TestSettleBaseMax 验证基数取承诺速率与计费速率的较大者。
func TestSettleBaseMax(t *testing.T) {
	s := NewSettler(0, 20)
	fillSlots(t, s, 0, 20, 100)
	r, err := s.Settle(200, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Base != 200 {
		t.Fatalf("承诺速率较大时基数 = %d, 期望 200", r.Base)
	}

	s = NewSettler(0, 20)
	fillSlots(t, s, 0, 20, 300)
	r, err = s.Settle(100, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Base != 300 {
		t.Fatalf("计费速率较大时基数 = %d, 期望 300", r.Base)
	}
}
