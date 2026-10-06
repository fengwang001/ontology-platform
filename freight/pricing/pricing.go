// Package pricing 为纯函数计价引擎：输入运单与合同，输出可复现费用明细。
package pricing

import (
	"math/big"

	"ontology/freight/model"
)

// Calculate 依据合同对运单计价。
// 调用方须已完成参数校验、合同选择，并保证运单在合同承运范围内。
func Calculate(w *model.Waybill, c *model.Contract) (*model.FeeBreakdown, error) {
	volume := w.Dim.Length * w.Dim.Width * w.Dim.Height
	rawVol, err := ceilDiv(volume, c.VolumeFactor)
	if err != nil {
		return nil, model.ErrInvalid("体积重量计算溢出")
	}
	volWeight, err := roundUpUnit(rawVol, c.BillingUnit)
	if err != nil {
		return nil, model.ErrInvalid("体积重量取整溢出")
	}
	heavier := w.Weight
	if volWeight > heavier {
		heavier = volWeight
	}
	chargeable, err := roundUpUnit(heavier, c.BillingUnit)
	if err != nil {
		return nil, model.ErrInvalid("计费重量取整溢出")
	}

	fb := &model.FeeBreakdown{
		WaybillNumber: w.Number,
		CarrierID:     c.CarrierID,
		ContractID:    c.ID,
		PickupAt:      w.PickupAt,

		ActualWeight:     w.Weight,
		Volume:           volume,
		RawVolumeWeight:  rawVol,
		BillingUnit:      c.BillingUnit,
		VolumeWeight:     volWeight,
		ChargeableWeight: chargeable,

		MinimumCharge: c.MinimumCharge,

		FuelRatePerMille: c.FuelRatePerMille,
	}

	// 累进阶梯：计费重量落入各档的部分分别按该档单价计价。
	base, err := tierFreight(c, chargeable, fb)
	if err != nil {
		return nil, err
	}
	fb.BaseFreight = base
	fb.BaseAfterMinimum = base
	if c.MinimumCharge > base {
		fb.BaseAfterMinimum = c.MinimumCharge
	}

	// 燃油附加费作用于最低收费提升后的金额，按千分比向上取整。
	fuel, err := perMilleCeil(fb.BaseAfterMinimum, c.FuelRatePerMille)
	if err != nil {
		return nil, err
	}
	fb.FuelSurcharge = fuel

	// 偏远附加费：仅看终点区域。
	for _, r := range c.RemoteRegions {
		if r == w.Route.To {
			fb.Remote = true
			fb.RemoteSurcharge = c.RemoteSurcharge
			break
		}
	}

	// 两个超限条件彼此独立，使用各自的“实际”计量值。
	maxSide := w.Dim.Length
	if w.Dim.Width > maxSide {
		maxSide = w.Dim.Width
	}
	if w.Dim.Height > maxSide {
		maxSide = w.Dim.Height
	}
	if c.SideThreshold > 0 && maxSide > c.SideThreshold {
		fb.Oversize = true
		fb.OversizeSurcharge = c.OversizeSurcharge
	}
	if c.WeightThreshold > 0 && chargeable > c.WeightThreshold {
		fb.Overweight = true
		fb.OverweightSurcharge = c.OverweightSurcharge
	}

	fb.Total, err = addAll(fb.BaseAfterMinimum, fb.FuelSurcharge,
		fb.RemoteSurcharge, fb.OversizeSurcharge, fb.OverweightSurcharge)
	if err != nil {
		return nil, err
	}
	return fb, nil
}

// InCarrierRange 使用实际重量与实际单边尺寸判定是否在可承运范围内。
func InCarrierRange(w *model.Waybill, c *model.Contract) bool {
	if w.Weight > c.MaxWeight {
		return false
	}
	if c.MaxSide != 0 {
		if w.Dim.Length > c.MaxSide || w.Dim.Width > c.MaxSide || w.Dim.Height > c.MaxSide {
			return false
		}
	}
	return true
}

func ceilDiv(a, b int64) (int64, error) {
	if b <= 0 || a < 0 {
		return 0, model.ErrInvalid("ceilDiv 参数非法")
	}
	num := new(big.Int).Add(big.NewInt(a), big.NewInt(b-1))
	num.Quo(num, big.NewInt(b))
	if err := checkInt64Range(num); err != nil {
		return 0, err
	}
	return num.Int64(), nil
}

func roundUpUnit(x, unit int64) (int64, error) {
	if unit <= 0 || x < 0 {
		return 0, model.ErrInvalid("roundUpUnit 参数非法")
	}
	v, err := ceilDiv(x, unit)
	if err != nil {
		return 0, err
	}
	return mul(v, unit)
}

func tierFreight(c *model.Contract, chargeable int64, fb *model.FeeBreakdown) (int64, error) {
	var total int64
	for _, t := range c.Tiers {
		if chargeable <= t.Lower {
			break
		}
		upper := t.Upper
		covered := chargeable
		if upper != 0 && upper < covered {
			covered = upper
		}
		span := covered - t.Lower
		units, err := ceilDiv(span, c.BillingUnit)
		if err != nil {
			return 0, err
		}
		amount, err := mul(units, t.PricePerUnit)
		if err != nil {
			return 0, err
		}
		total, err = add(total, amount)
		if err != nil {
			return 0, err
		}
		fb.TierLines = append(fb.TierLines, model.TierLine{
			Lower:        t.Lower,
			Upper:        t.Upper,
			Units:        units,
			PricePerUnit: t.PricePerUnit,
			Amount:       amount,
		})
	}
	return total, nil
}

func perMilleCeil(amount, rate int64) (int64, error) {
	if amount < 0 || rate < 0 {
		return 0, model.ErrInvalid("千分比参数非法")
	}
	num := new(big.Int).Mul(big.NewInt(amount), big.NewInt(rate))
	num.Add(num, big.NewInt(999))
	num.Quo(num, big.NewInt(1000))
	if err := checkInt64Range(num); err != nil {
		return 0, err
	}
	return num.Int64(), nil
}

func mul(a, b int64) (int64, error) {
	r := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if err := checkInt64Range(r); err != nil {
		return 0, err
	}
	return r.Int64(), nil
}

func add(values ...int64) (int64, error) { return addAll(values...) }

func addAll(values ...int64) (int64, error) {
	r := new(big.Int)
	for _, v := range values {
		r.Add(r, big.NewInt(v))
	}
	if err := checkInt64Range(r); err != nil {
		return 0, err
	}
	return r.Int64(), nil
}

func checkInt64Range(v *big.Int) error {
	if v.Cmp(big.NewInt(0)) < 0 {
		return model.ErrInvalid("金额为负")
	}
	max := new(big.Int).SetInt64(1<<63 - 1)
	if v.Cmp(max) > 0 {
		return model.ErrInvalid("整数溢出")
	}
	return nil
}
