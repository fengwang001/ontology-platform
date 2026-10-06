package freighttest

import (
	"testing"

	"ontology/freight/model"
	"ontology/freight/system"
)

func newSystem(t *testing.T) *system.System {
	t.Helper()
	s := system.New(nil)
	mustAdd(t, s, baseContract("C1", "顺丰", 100, 200))
	return s
}

// 区间两端取等：左闭 [100,200) 右开。
func TestIntervalEndpoints(t *testing.T) {
	s := newSystem(t)

	fb, err := s.Price(waybill("W0", "顺丰", 100, 1000, 10, 10, 10, "上海"))
	if err != nil {
		t.Fatalf("左端点应命中: %v", err)
	}
	if fb.ContractID != "C1" {
		t.Fatalf("左端点命中合同错误: %s", fb.ContractID)
	}

	if _, err = s.Price(waybill("W1", "顺丰", 199, 1000, 10, 10, 10, "上海")); err != nil {
		t.Fatalf("199 应命中: %v", err)
	}

	_, err = s.Price(waybill("W2", "顺丰", 200, 1000, 10, 10, 10, "上海"))
	if got := model.CodeOf(err); got != model.CodeTimeNotCovered {
		t.Fatalf("右端点开区间，应=时刻未覆盖, 实际=%v (%v)", got, err)
	}
}

// 相接合同 [100,200)+[200,300) 合法，且 200 命中后者。
func TestAdjacentContracts(t *testing.T) {
	s := newSystem(t)
	c2 := baseContract("C2", "顺丰", 200, 300)
	c2.MinimumCharge = 3000
	mustAdd(t, s, c2)

	fb, err := s.Price(waybill("W0", "顺丰", 200, 1000, 10, 10, 10, "上海"))
	if err != nil {
		t.Fatalf("相接点应命中 C2: %v", err)
	}
	if fb.ContractID != "C2" || fb.MinimumCharge != 3000 {
		t.Fatalf("相接点命中错误: %+v", fb)
	}
}

// 区间重叠：添加失败且不改变已有合同。
func TestOverlapRejectedAndNoSideEffect(t *testing.T) {
	s := newSystem(t)
	bad := baseContract("CX", "顺丰", 150, 250)
	err := s.AddContract(bad)
	if got := model.CodeOf(err); got != model.CodeIntervalOverlap {
		t.Fatalf("应报区间重叠, 实际=%v", got)
	}
	// 旧合同在 150 仍应命中。
	fb, err := s.Price(waybill("W0", "顺丰", 150, 1000, 10, 10, 10, "上海"))
	if err != nil || fb.ContractID != "C1" {
		t.Fatalf("重叠拒绝后旧合同应不变, fb=%+v err=%v", fb, err)
	}

	// 参数非法优先于区间重叠。
	bad2 := baseContract("CY", "顺丰", 150, 250)
	bad2.BillingUnit = 0
	if got := model.CodeOf(s.AddContract(bad2)); got != model.CodeInvalidArgument {
		t.Fatalf("应报参数非法, 实际=%v", got)
	}
}

// 无合同 与 时刻未覆盖 必须可区分。
func TestNoContractVsTimeNotCovered(t *testing.T) {
	s := newSystem(t)

	_, err := s.Price(waybill("W0", "圆通", 100, 1000, 10, 10, 10, "上海"))
	if got := model.CodeOf(err); got != model.CodeNoContract {
		t.Fatalf("应=无合同, 实际=%v", got)
	}
	// 同承运商不同线路也算无合同。
	w := waybill("W1", "顺丰", 100, 1000, 10, 10, 10, "广州")
	_, err = s.Price(w)
	if got := model.CodeOf(err); got != model.CodeNoContract {
		t.Fatalf("不同线路应=无合同, 实际=%v", got)
	}
	// 时刻超出范围。
	_, err = s.Price(waybill("W2", "顺丰", 99, 1000, 10, 10, 10, "上海"))
	if got := model.CodeOf(err); got != model.CodeTimeNotCovered {
		t.Fatalf("99 应=时刻未覆盖, 实际=%v", got)
	}
}

// 完整计费：体积重量/实际重量互换、累进、最低收费、燃油、偏远、双超限。
func TestPricingDetails(t *testing.T) {
	s := newSystem(t)

	// 实重 12kg，体积 300*400*500=60,000,000 mm3 → 体积重 60kg（>12kg）。
	// 计费重 60kg。累进：
	//   [0,10):10*100=1000；[10,50):40*80=3200；[50,60]:10*60=600 → 基础 4800。
	// 高于最低 2000。燃油 ceil(4800*100/1000)=480。
	// 终点上海非偏远。最大边 500<=1000 不超大；60kg>40kg 超重 +900。
	// 合计 4800+480+900=6180。
	w := waybill("W0", "顺丰", 100, 12_000, 300, 400, 500, "上海")
	fb, err := s.Price(w)
	if err != nil {
		t.Fatalf("计价失败: %v", err)
	}
	if fb.RawVolumeWeight != 60_000 || fb.VolumeWeight != 60_000 ||
		fb.ChargeableWeight != 60_000 {
		t.Fatalf("体积重/计费重错误: %+v", fb)
	}
	if fb.BaseFreight != 4_800 || len(fb.TierLines) != 3 {
		t.Fatalf("累进基础运费错误: %+v", fb.TierLines)
	}
	if fb.BaseAfterMinimum != 4_800 || fb.FuelSurcharge != 480 {
		t.Fatalf("提升后金额/燃油错误: %+v", fb)
	}
	if fb.Remote || fb.Oversize || !fb.Overweight {
		t.Fatalf("附加条件判定错误: remote=%v oversize=%v overweight=%v",
			fb.Remote, fb.Oversize, fb.Overweight)
	}
	if fb.Total != 6_180 {
		t.Fatalf("合计错误: %d", fb.Total)
	}
}

// 实际重量主导且低于最低收费。
func TestActualWeightAndMinimum(t *testing.T) {
	s := newSystem(t)
	// 1kg：体积很小（10^3 → 0.001kg→ceil=1kg），实重 1kg。
	// 基础 = 1*100 = 100 < 最低 2000 → 提升到 2000。
	// 燃油 ceil(2000*100/1000)=200。无附加。合计 2200。
	fb, err := s.Price(waybill("W0", "顺丰", 100, 1000, 10, 10, 10, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if fb.BaseFreight != 100 || fb.BaseAfterMinimum != 2000 ||
		fb.FuelSurcharge != 200 || fb.Total != 2200 {
		t.Fatalf("最低收费提升链错误: %+v", fb)
	}
}

// 最低收费恰等于基础运费：不提升，明细自洽。
func TestMinimumExactlyEqualsBase(t *testing.T) {
	s := system.New(nil)
	c := baseContract("C1", "顺丰", 100, 200)
	c.MinimumCharge = 500 // 5kg*100 = 500
	mustAdd(t, s, c)
	fb, err := s.Price(waybill("W0", "顺丰", 100, 5000, 10, 10, 10, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if fb.BaseFreight != 500 || fb.BaseAfterMinimum != 500 {
		t.Fatalf("恰等时不应提升: base=%d after=%d", fb.BaseFreight, fb.BaseAfterMinimum)
	}
}

// 偏远 + 两项超限彼此独立。
func TestRemoteAndIndependentSurcharges(t *testing.T) {
	s := newSystem(t)
	remote := baseContract("C1R", "顺丰", 100, 200)
	remote.Route.To = "西藏"
	mustAdd(t, s, remote)

	// 偏远（西藏）、单边 1001>1000、计费重 60kg>40kg：三项全收。
	fb, err := s.Price(waybill("W0", "顺丰", 100, 60_000, 1001, 10, 10, "西藏"))
	if err != nil {
		t.Fatal(err)
	}
	want := fb.BaseAfterMinimum + fb.FuelSurcharge + 500 + 700 + 900
	if !fb.Remote || !fb.Oversize || !fb.Overweight || fb.Total != want {
		t.Fatalf("三附加应同时成立: %+v", fb)
	}

	// 仅超大：任一边超阈值即可；计费重未超阈值。
	fb2, err := s.Price(waybill("W1", "顺丰", 100, 1000, 10, 1001, 10, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if !fb2.Oversize || fb2.Overweight || fb2.Remote {
		t.Fatalf("应仅超大: %+v", fb2)
	}

	// 恰等于阈值不触发（严格大于）。
	fb3, err := s.Price(waybill("W2", "顺丰", 100, 40_000, 10, 10, 10, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if fb3.Oversize || fb3.Overweight {
		t.Fatalf("阈值取等不应加收: %+v", fb3)
	}
}

// 超出承运范围：实际重量/实际单边，且优先级在时刻覆盖之后。
func TestOutOfCarrierRange(t *testing.T) {
	s := newSystem(t)
	_, err := s.Price(waybill("W0", "顺丰", 100, 100_001, 10, 10, 10, "上海"))
	if got := model.CodeOf(err); got != model.CodeOutOfRange {
		t.Fatalf("实重超限应=超出承运范围, 实际=%v", got)
	}
	_, err = s.Price(waybill("W1", "顺丰", 100, 1000, 2001, 10, 10, "上海"))
	if got := model.CodeOf(err); got != model.CodeOutOfRange {
		t.Fatalf("单边超限应=超出承运范围, 实际=%v", got)
	}
	// 时刻未覆盖时优先报时刻，而不是超范围。
	_, err = s.Price(waybill("W2", "顺丰", 300, 100_001, 5000, 10, 10, "上海"))
	if got := model.CodeOf(err); got != model.CodeTimeNotCovered {
		t.Fatalf("时刻未覆盖优先, 实际=%v", got)
	}
}

// 向上取整边界：体积重 ceil 与计重单位 ceil。
func TestCeilBoundaries(t *testing.T) {
	s := newSystem(t)
	// 1000*1000*1 = 1,000,000 mm³；/1000 = 1000g 整除 → 恰好 1 个计重单位。
	fb, err := s.Price(waybill("W0", "顺丰", 100, 1, 1000, 1000, 1, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if fb.RawVolumeWeight != 1000 || fb.ChargeableWeight != 1000 {
		t.Fatalf("整除法边界错误: raw=%d chg=%d", fb.RawVolumeWeight, fb.ChargeableWeight)
	}

	// 1001*1000*1 = 1,001,000 mm³ → ceil = 1001g → 向上取整到 1000g = 2000g。
	fb2, err := s.Price(waybill("W1", "顺丰", 100, 1, 1001, 1000, 1, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if fb2.RawVolumeWeight != 1001 || fb2.VolumeWeight != 2000 || fb2.ChargeableWeight != 2000 {
		t.Fatalf("ceil 边界错误: raw=%d vol=%d chg=%d",
			fb2.RawVolumeWeight, fb2.VolumeWeight, fb2.ChargeableWeight)
	}

	// 实重 1001g 主导（体积仅 1g）：先取大 1001，再 ceil 到 1000 = 2000g。
	fb3, err := s.Price(waybill("W2", "顺丰", 100, 1001, 1, 1, 1, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	if fb3.ChargeableWeight != 2000 || fb3.RawVolumeWeight != 1 {
		t.Fatalf("实重主导取整错误: raw=%d chg=%d", fb3.RawVolumeWeight, fb3.ChargeableWeight)
	}
}
