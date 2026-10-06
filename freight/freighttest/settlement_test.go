package freighttest

import (
	"testing"

	"ontology/freight/model"
	"ontology/freight/system"
)

// 结算一次：金额固化；合同修订后已结算金额不变；重复结算报已结算。
func TestSettleFreezeAndIdempotency(t *testing.T) {
	s := system.New(nil)
	c := baseContract("C1", "顺丰", 100, 300)
	mustAdd(t, s, c)

	w := waybill("W0", "顺丰", 150, 5000, 10, 10, 10, "上海")
	fb, err := s.Price(w)
	if err != nil {
		t.Fatal(err)
	}
	first := fb.Total

	set1, err := s.Settle("W0")
	if err != nil {
		t.Fatalf("首次结算失败: %v", err)
	}
	if set1.Total != first {
		t.Fatalf("结算金额与计价不一致: %d != %d", set1.Total, first)
	}

	// 同一运单再次结算 → 已结算。
	if _, err = s.Settle("W0"); model.CodeOf(err) != model.CodeAlreadySettled {
		t.Fatalf("重复结算应报已结算, 实际=%v", err)
	}

	// 结算后修订合同（同 ID、同车道，区间不重叠：整体替换为新区间）。
	c2 := baseContract("C1", "顺丰", 100, 300)
	c2.Tiers = []model.WeightTier{
		{Lower: 0, Upper: 10_000, PricePerUnit: 999},
		{Lower: 10_000, Upper: 0, PricePerUnit: 999},
	}
	c2.MinimumCharge = 99_999
	mustAdd(t, s, c2)

	if got := s.Settlement("W0"); got == nil || got.Total != first {
		t.Fatalf("已结算金额不得被合同修订改写: got=%v want=%d", got, first)
	}

	// 未结算运单再次计价使用最新合同集合。
	fb2, err := s.Price(waybill("W1", "顺丰", 150, 5000, 10, 10, 10, "上海"))
	if err != nil {
		t.Fatal(err)
	}
	// 5 单位 * 999 = 4995 < 最低 99999 → 提升；燃油 100‰ → 9999.9 → ceil 10000。
	if fb2.BaseAfterMinimum != 99_999 || fb2.FuelSurcharge != 10_000 {
		t.Fatalf("未结算运单应采用新合同: %+v", fb2)
	}

	// 已结算运单再次计价：最新报价变化，但固化结算不变。
	if _, err = s.Price(waybill("W0", "顺丰", 150, 5000, 10, 10, 10, "上海")); err != nil {
		t.Fatal(err)
	}
	if got := s.Settlement("W0"); got.Total != first {
		t.Fatalf("已结算运单再次计价后固化金额仍应不变: %d != %d", got.Total, first)
	}

	// 未计价直接结算报参数非法。
	if _, err = s.Settle("NOPE"); model.CodeOf(err) != model.CodeInvalidArgument {
		t.Fatalf("未计价结算应报参数非法, 实际=%v", err)
	}
}

// 多承运商询价：排序、失败单列、不改状态。
func TestMultiCarrierQuote(t *testing.T) {
	s := system.New(nil)
	c1 := baseContract("C-B", "乙快递", 0, 200)
	c1.MinimumCharge = 10_000 // 报价更贵
	mustAdd(t, s, c1)
	c2 := baseContract("C-A", "甲快运", 0, 200)
	c2.MinimumCharge = 3_000 // 报价更便宜
	mustAdd(t, s, c2)
	// 丙快递：同线路有合同但时刻不覆盖该揽收时刻。
	c3 := baseContract("C-C", "丙物流", 500, 900)
	c3.MinimumCharge = 1_000
	mustAdd(t, s, c3)

	lines, err := s.Quote(system.QuoteRequest{
		Route:    model.Route{From: "北京", To: "上海"},
		Class:    model.Standard,
		PickupAt: 100,
		Weight:   1000,
		Dim:      model.Dimensions{Length: 10, Width: 10, Height: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("应有三条结果, 实际=%d: %+v", len(lines), lines)
	}
	if lines[0].CarrierID != "甲快运" || lines[1].CarrierID != "乙快递" {
		t.Fatalf("成功项应按总价升序: %+v", lines)
	}
	if lines[2].CarrierID != "丙物流" || lines[2].Reason != model.CodeTimeNotCovered {
		t.Fatalf("失败项单列在后且原因正确: %+v", lines[2])
	}

	// 显式指定承运商；其中一家完全无合同。
	lines2, err := s.Quote(system.QuoteRequest{
		Route:    model.Route{From: "北京", To: "上海"},
		Class:    model.Standard,
		PickupAt: 100,
		Weight:   1000,
		Dim:      model.Dimensions{Length: 10, Width: 10, Height: 10},
		Carriers: []string{"甲快运", "丁新商"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lines2[0].CarrierID != "甲快运" || lines2[0].Reason != "" {
		t.Fatalf("甲应成功: %+v", lines2[0])
	}
	if lines2[1].CarrierID != "丁新商" || lines2[1].Reason != model.CodeNoContract {
		t.Fatalf("丁应无合同且不影响甲: %+v", lines2[1])
	}

	// 超范围承运商单列原因，不影响他人。
	lines3, err := s.Quote(system.QuoteRequest{
		Route:    model.Route{From: "北京", To: "上海"},
		Class:    model.Standard,
		PickupAt: 100,
		Weight:   500_000,
		Dim:      model.Dimensions{Length: 10, Width: 10, Height: 10},
		Carriers: []string{"甲快运"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lines3[0].Reason != model.CodeOutOfRange {
		t.Fatalf("应报超出承运范围: %+v", lines3[0])
	}

	// 询价不产生可结算状态。
	if got := s.Settlement("__quote__"); got != nil {
		t.Fatalf("询价不得改变结算状态: %+v", got)
	}

	// 入参非法时整单失败。
	if _, err = s.Quote(system.QuoteRequest{
		Route:  model.Route{From: "北京", To: "上海"},
		Class:  model.Standard,
		Weight: 0,
		Dim:    model.Dimensions{Length: 10, Width: 10, Height: 10},
	}); model.CodeOf(err) != model.CodeInvalidArgument {
		t.Fatalf("询价入参非法应报参数非法, 实际=%v", err)
	}
}

// 加急与标准相互独立选合同。
func TestServiceClassIsolation(t *testing.T) {
	s := system.New(nil)
	mustAdd(t, s, baseContract("C-S", "顺丰", 0, 200))
	ce := baseContract("C-E", "顺丰", 0, 200)
	ce.Class = model.Express
	ce.MinimumCharge = 7_777
	mustAdd(t, s, ce)

	we := waybill("E0", "顺丰", 100, 1000, 10, 10, 10, "上海")
	we.Class = model.Express
	fb, err := s.Price(we)
	if err != nil {
		t.Fatal(err)
	}
	if fb.ContractID != "C-E" || fb.MinimumCharge != 7_777 {
		t.Fatalf("加急应命中加急合同: %+v", fb)
	}
}
