// 可运行演示：go run ./freight/example
package main

import (
	"fmt"
	"os"

	"ontology/freight/model"
	"ontology/freight/system"
)

func main() {
	s := system.New(system.NewJSONLogger(os.Stdout))

	c1 := &model.Contract{
		ID: "C-OLD", CarrierID: "顺丰",
		Route:       model.Route{From: "北京", To: "上海"},
		Class:       model.Standard,
		EffectiveAt: 100, ExpiresAt: 200,

		VolumeFactor: 1000, BillingUnit: 1000,
		Tiers: []model.WeightTier{
			{Lower: 0, Upper: 10_000, PricePerUnit: 100},
			{Lower: 10_000, Upper: 50_000, PricePerUnit: 80},
			{Lower: 50_000, Upper: 0, PricePerUnit: 60},
		},
		FuelRatePerMille:    100,
		RemoteRegions:       []model.Region{"西藏"},
		RemoteSurcharge:     500,
		SideThreshold:       1000,
		WeightThreshold:     40_000,
		OversizeSurcharge:   700,
		OverweightSurcharge: 900,
		MinimumCharge:       2000,
		MaxWeight:           100_000,
		MaxSide:             2000,
	}
	must(s.AddContract(c1))

	w := &model.Waybill{
		Number: "W-001", CarrierID: "顺丰",
		Route:    model.Route{From: "北京", To: "上海"},
		Class:    model.Standard,
		PickupAt: 150,
		Weight:   12_000,
		Dim:      model.Dimensions{Length: 300, Width: 400, Height: 500},
	}
	fb, err := s.Price(w)
	must(err)

	fmt.Fprintln(os.Stderr, "---- 费用明细（人类可读） ----")
	fmt.Fprintf(os.Stderr, "合同=%s 计费重量=%dg（实重=%d 体积重=%d）\n",
		fb.ContractID, fb.ChargeableWeight, fb.ActualWeight, fb.VolumeWeight)
	for _, tl := range fb.TierLines {
		fmt.Fprintf(os.Stderr, "  档 [%d,%d): %d 单位 × %d = %d 分\n",
			tl.Lower, tl.Upper, tl.Units, tl.PricePerUnit, tl.Amount)
	}
	fmt.Fprintf(os.Stderr, "基础=%d 最低提升后=%d 燃油=%d 超重=%d 合计=%d\n",
		fb.BaseFreight, fb.BaseAfterMinimum, fb.FuelSurcharge,
		fb.OverweightSurcharge, fb.Total)

	settled, err := s.Settle("W-001")
	must(err)
	_ = settled

	// 结算后修订合同；已结算金额保持不变。
	c2 := *c1
	c2.MinimumCharge = 99_999
	must(s.AddContract(&c2))
	fmt.Fprintf(os.Stderr, "合同修订后固化结算金额仍为 %d 分\n", s.Settlement("W-001").Total)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
