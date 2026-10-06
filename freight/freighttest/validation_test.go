package freighttest

import (
	"testing"

	"ontology/freight/model"
)

func validContract() *model.Contract { return baseContract("V1", "顺丰", 0, 100) }

func TestContractValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(c *model.Contract)
	}{
		{"空编号", func(c *model.Contract) { c.ID = "" }},
		{"空承运商", func(c *model.Contract) { c.CarrierID = "" }},
		{"空起点", func(c *model.Contract) { c.Route.From = "" }},
		{"空终点", func(c *model.Contract) { c.Route.To = "" }},
		{"非法等级", func(c *model.Contract) { c.Class = "火箭达" }},
		{"区间空", func(c *model.Contract) { c.ExpiresAt = c.EffectiveAt }},
		{"区间反向", func(c *model.Contract) { c.ExpiresAt = c.EffectiveAt - 1 }},
		{"折算系数为0", func(c *model.Contract) { c.VolumeFactor = 0 }},
		{"计重单位为负", func(c *model.Contract) { c.BillingUnit = -1 }},
		{"无阶梯", func(c *model.Contract) { c.Tiers = nil }},
		{"阶梯下界为负", func(c *model.Contract) { c.Tiers[0].Lower = -1 }},
		{"阶梯单价为负", func(c *model.Contract) { c.Tiers[0].PricePerUnit = -1 }},
		{"阶梯区间非正长", func(c *model.Contract) { c.Tiers[0].Upper = 0 }},
		{"阶梯有空档", func(c *model.Contract) { c.Tiers[1].Lower = 11_000 }},
		{"首档非0", func(c *model.Contract) { c.Tiers[0].Lower = 1000 }},
		{"末档非无穷", func(c *model.Contract) { c.Tiers[2].Upper = 99_999 }},
		{"档界不对齐单位", func(c *model.Contract) { c.Tiers[1].Lower = 10_500; c.Tiers[1].Upper = 50_000 }},
		{"燃油千分比为负", func(c *model.Contract) { c.FuelRatePerMille = -1 }},
		{"偏远定额为负", func(c *model.Contract) { c.RemoteSurcharge = -1 }},
		{"超大定额为负", func(c *model.Contract) { c.OversizeSurcharge = -1 }},
		{"超重定额为负", func(c *model.Contract) { c.OverweightSurcharge = -1 }},
		{"最低收费为负", func(c *model.Contract) { c.MinimumCharge = -1 }},
		{"尺寸阈值为负", func(c *model.Contract) { c.SideThreshold = -1 }},
		{"重量阈值为负", func(c *model.Contract) { c.WeightThreshold = -1 }},
		{"最大重量为0", func(c *model.Contract) { c.MaxWeight = 0 }},
		{"最大单边为负", func(c *model.Contract) { c.MaxSide = -1 }},
		{"偏远清单空区域", func(c *model.Contract) { c.RemoteRegions = []model.Region{""} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validContract()
			tc.mut(c)
			if err := c.Validate(); model.CodeOf(err) != model.CodeInvalidArgument {
				t.Fatalf("%s 应报参数非法, 实际 err=%v", tc.name, err)
			}
		})
	}

	if err := validContract().Validate(); err != nil {
		t.Fatalf("合法合同被拒: %v", err)
	}
}

func TestWaybillValidation(t *testing.T) {
	w := waybill("W1", "顺丰", 0, 1000, 10, 10, 10, "上海")
	if err := w.Validate(); err != nil {
		t.Fatalf("合法运单被拒: %v", err)
	}
	bad := []func(x *model.Waybill){
		func(x *model.Waybill) { x.Number = "" },
		func(x *model.Waybill) { x.CarrierID = "" },
		func(x *model.Waybill) { x.Route.From = "" },
		func(x *model.Waybill) { x.Class = "瞬移" },
		func(x *model.Waybill) { x.Weight = 0 },
		func(x *model.Waybill) { x.Dim.Length = -1 },
	}
	for i, mut := range bad {
		x := *w
		mut(&x)
		if err := x.Validate(); model.CodeOf(err) != model.CodeInvalidArgument {
			t.Fatalf("非法运单 #%d 应报参数非法, 实际=%v", i, err)
		}
	}
}

// 非法入参在任何业务判定之前拒绝（优先级证据）。
func TestInvalidArgumentPriority(t *testing.T) {
	s := newSystem(t)
	bad := waybill("W1", "顺丰", 100, -1, 10, 10, 10, "上海")
	if _, err := s.Price(bad); model.CodeOf(err) != model.CodeInvalidArgument {
		t.Fatalf("非法运单应报参数非法, 实际=%v", err)
	}

	badC := baseContract("BAD", "顺丰", 100, 200)
	badC.BillingUnit = 0
	if err := s.AddContract(badC); model.CodeOf(err) != model.CodeInvalidArgument {
		t.Fatalf("非法合同应报参数非法, 实际=%v", err)
	}
}
