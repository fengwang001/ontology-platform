package speq

import (
	"errors"
	"testing"
)

func testSystem(t *testing.T) *System {
	t.Helper()
	s := New()
	must(t, s.AddCategory(CategoryConfig{
		Code: "BOILER", Kind: KindDevice, PeriodMonths: 12,
		EarlyWindowDays: 30, MinUnsealDays: 10, WarningLeadDays: 15,
	}))
	must(t, s.AddCategory(CategoryConfig{
		Code: "PV", Kind: KindDevice, PeriodMonths: 24,
		EarlyWindowDays: 60, MinUnsealDays: 20, WarningLeadDays: 30,
	}))
	must(t, s.AddCategory(CategoryConfig{
		Code: "SV", Kind: KindSafetyValve, PeriodMonths: 12,
		EarlyWindowDays: 30, MinUnsealDays: 5, WarningLeadDays: 10,
	}))
	must(t, s.AddCategory(CategoryConfig{
		Code: "PG", Kind: KindPressureGauge, PeriodMonths: 6,
		EarlyWindowDays: 15, MinUnsealDays: 5, WarningLeadDays: 7,
	}))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// mountSV 登记并挂接一个长有效期安全阀，使设备满足"至少一个安全阀"。
func mountSV(t *testing.T, s *System, date int, svID, devID string) {
	t.Helper()
	_, err := s.RegisterAttachment(date, svID, "SV", KindSafetyValve, date)
	must(t, err)
	must(t, s.MountAttachment(date, svID, devID))
}

func codeOf(err error) ErrorCode {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return -1
}

func TestExpiryBoundaryAndEarlyWindow(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	exp, err := s.RegisterDevice(d0, "B1", "BOILER", d0)
	must(t, err)
	mountSV(t, s, d0, "V1", "B1")
	_, err = s.RegisterDevice(d0, "B2", "BOILER", d0)
	must(t, err)
	mountSV(t, s, d0, "V2", "B2")
	_, err = s.RegisterDevice(d0, "B3", "BOILER", d0)
	must(t, err)
	mountSV(t, s, d0, "V3", "B3")
	if exp != ord(2021, 1, 1) {
		t.Fatalf("first expiry = %d want 2021-01-01", exp)
	}

	// B2：到期日当天仍有效，次日超期（该操作不推进日期之外的状态，但推进全局日期）。
	must(t, s.RegisterUse(ord(2021, 1, 1), "B2"))
	err = s.RegisterUse(ord(2021, 1, 2), "B2")
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Reason != RejectDeviceExpired {
		t.Fatalf("day after expiry should be expired, got %v", err)
	}

	// B3 已无法再以更早日期检验，因此改用独立系统验证窗口边界。
	sb := testSystem(t)
	_, err = sb.RegisterDevice(d0, "X1", "BOILER", d0)
	must(t, err)
	mountSV(t, sb, d0, "VX1", "X1")
	insp := exp - 30
	newExp, err := sb.Inspect(insp, "X1", ResultPass, 0)
	must(t, err)
	if newExp != ord(2022, 1, 1) {
		t.Fatalf("window boundary should base on old expiry, got %d", newExp)
	}
	// 窗口外（过早一天）：以检验日为基准。
	insp2 := ord(2021, 11, 1)
	newExp2, err := sb.Inspect(insp2, "X1", ResultPass, 0)
	must(t, err)
	if newExp2 != ord(2022, 11, 1) {
		t.Fatalf("outside window should base on inspect date, got %d", newExp2)
	}

	// B1：已超期检验合格：以检验日为基准，合格后恢复可使用。
	insp3 := ord(2023, 6, 1)
	newExp3, err := s.Inspect(insp3, "B1", ResultPass, 0)
	must(t, err)
	if newExp3 != ord(2024, 6, 1) {
		t.Fatalf("overdue inspect should base on inspect date, got %d", newExp3)
	}
	_, err = s.Inspect(insp3, "V1", ResultPass, 0)
	must(t, err)
	must(t, s.RegisterUse(insp3, "B1"))
}

func TestConditionalTakesEarlier(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "B1", "BOILER", d0)
	mountSV(t, s, d0, "V1", "B1")
	// 在到期日当天检验有条件合格，整改限期 20 天。
	insp := ord(2021, 1, 1)
	exp, err := s.Inspect(insp, "B1", ResultConditional, 20)
	must(t, err)
	if exp != insp+20 {
		t.Fatalf("conditional expiry should be inspect+20, got %d", exp)
	}
	// 整改限期很长时取合格规则结果。
	exp2, err := s.Inspect(ord(2021, 1, 21), "B1", ResultConditional, 400)
	must(t, err)
	// 检验日已超期 -> 以检验日为基准 +12 月 = 2022-01-21，早于 +400。
	if exp2 != ord(2022, 1, 21) {
		t.Fatalf("conditional long limit should take pass-rule date, got %d", exp2)
	}
}

func TestFailDisablesAndRecover(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)
	_, _ = s.RegisterDevice(d0, "B1", "BOILER", d0)
	mountSV(t, s, d0, "V1", "B1")
	_, err := s.Inspect(ord(2020, 6, 1), "B1", ResultFail, 0)
	must(t, err)
	err = s.RegisterUse(ord(2020, 6, 2), "B1")
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Reason != RejectDeviceDisabled {
		t.Fatalf("fail should disable, got %v", err)
	}
	// 到期日不变。
	snap, _ := s.Get("B1")
	if snap.Expiry != ord(2021, 1, 1) {
		t.Fatalf("fail must keep expiry, got %d", snap.Expiry)
	}
	// 有条件合格不恢复停用。
	_, _ = s.Inspect(ord(2020, 7, 1), "B1", ResultConditional, 30)
	if err := s.RegisterUse(ord(2020, 7, 2), "B1"); err == nil {
		t.Fatal("conditional pass must not recover disabled")
	}
	// 复检合格恢复。
	_, _ = s.Inspect(ord(2020, 8, 1), "B1", ResultPass, 0)
	must(t, s.RegisterUse(ord(2020, 8, 1), "B1"))
}
