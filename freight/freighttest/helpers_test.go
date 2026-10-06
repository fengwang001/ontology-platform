package freighttest

import (
	"ontology/freight/model"
	"ontology/freight/system"
)

func baseContract(id, carrier string, lo, hi model.Time) *model.Contract {
	return &model.Contract{
		ID:           id,
		CarrierID:    carrier,
		Route:        model.Route{From: "北京", To: "上海"},
		Class:        model.Standard,
		EffectiveAt:  lo,
		ExpiresAt:    hi,
		VolumeFactor: 1_000, // 体积重量(克) = 体积(mm³)/1000（1cm³ 折算 1 克）
		BillingUnit:  1000,
		Tiers: []model.WeightTier{
			{Lower: 0, Upper: 10_000, PricePerUnit: 100},
			{Lower: 10_000, Upper: 50_000, PricePerUnit: 80},
			{Lower: 50_000, Upper: 0, PricePerUnit: 60},
		},
		FuelRatePerMille:    100, // 10%
		RemoteRegions:       []model.Region{"西藏", "新疆"},
		RemoteSurcharge:     500,
		SideThreshold:       1000,
		WeightThreshold:     40_000,
		OversizeSurcharge:   700,
		OverweightSurcharge: 900,
		MinimumCharge:       2000,
		MaxWeight:           100_000,
		MaxSide:             2000,
	}
}

func waybill(number, carrier string, t model.Time, weight int64,
	l, w, h int64, to model.Region) *model.Waybill {
	return &model.Waybill{
		Number:    number,
		CarrierID: carrier,
		Route:     model.Route{From: "北京", To: to},
		Class:     model.Standard,
		PickupAt:  t,
		Weight:    weight,
		Dim:       model.Dimensions{Length: l, Width: w, Height: h},
	}
}

func mustAdd(t testingT, s *system.System, c *model.Contract) {
	t.Helper()
	if err := s.AddContract(c); err != nil {
		t.Fatalf("AddContract(%s) 意外失败: %v", c.ID, err)
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
}
