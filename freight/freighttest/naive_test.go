package freighttest

// 独立编写的朴素参考模型：与生产代码完全不同的实现策略——
// 合同放进切片线性扫描、计价用最直白的逐步循环。
// 随机差分测试将同一操作序列同时喂给生产系统与朴素模型，逐字段比对。

import (
	"sort"

	"ontology/freight/model"
)

type naiveContract struct {
	c *model.Contract
}

type naiveSettlement struct {
	fb *naiveBreakdown
}

type naiveBreakdown struct {
	fb model.FeeBreakdown
}

// NaiveModel 朴素模型。
type NaiveModel struct {
	contracts []*model.Contract
	settled   map[string]*model.FeeBreakdown
	priced    map[string]*model.FeeBreakdown
}

// NewNaive 构造朴素模型。
func NewNaive() *NaiveModel {
	return &NaiveModel{
		settled: map[string]*model.FeeBreakdown{},
		priced:  map[string]*model.FeeBreakdown{},
	}
}

func naiveKey(c *model.Contract) [4]string {
	return [4]string{c.CarrierID, string(c.Route.From), string(c.Route.To), string(c.Class)}
}

func naiveClone(c *model.Contract) *model.Contract {
	cp := *c
	cp.Tiers = append([]model.WeightTier(nil), c.Tiers...)
	cp.RemoteRegions = append([]model.Region(nil), c.RemoteRegions...)
	return &cp
}

// Add 朴素添加/修订。
func (n *NaiveModel) Add(in *model.Contract) error {
	if err := in.Validate(); err != nil {
		return err
	}
	// 构造候选集合并线性检查重叠（含同 ID 修订替换）。
	var cand []*model.Contract
	for _, c := range n.contracts {
		if c.ID != in.ID {
			cand = append(cand, c)
		}
	}
	if existing := n.findByID(in.ID); existing != nil {
		if existing.CarrierID != in.CarrierID || existing.Route != in.Route ||
			existing.Class != in.Class {
			return model.ErrInvalid("修订合同不得改变承运商、线路或等级")
		}
	}
	for _, c := range cand {
		if naiveKey(c) != naiveKey(in) {
			continue
		}
		if in.EffectiveAt < c.ExpiresAt && c.EffectiveAt < in.ExpiresAt {
			return model.ErrIntervalOverlap("naive overlap")
		}
	}
	replaced := false
	for i, c := range n.contracts {
		if c.ID == in.ID {
			n.contracts[i] = naiveClone(in)
			replaced = true
			break
		}
	}
	if !replaced {
		n.contracts = append(n.contracts, naiveClone(in))
	}
	return nil
}

func (n *NaiveModel) findByID(id string) *model.Contract {
	for _, c := range n.contracts {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (n *NaiveModel) findContract(carrier string, r model.Route, class model.ServiceClass,
	t model.Time) (hasLane bool, c *model.Contract) {
	for _, x := range n.contracts {
		if x.CarrierID == carrier && x.Route == r && x.Class == class {
			hasLane = true
			if t >= x.EffectiveAt && t < x.ExpiresAt {
				c = x
			}
		}
	}
	return hasLane, c
}

func ceilDiv64(a, b int64) int64 { return (a + b - 1) / b }

// Price 朴素计价。
func (n *NaiveModel) Price(w *model.Waybill) (*model.FeeBreakdown, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	hasLane, c := n.findContract(w.CarrierID, w.Route, w.Class, w.PickupAt)
	if c == nil {
		if hasLane {
			return nil, model.NewError(model.CodeTimeNotCovered, "naive 时刻未覆盖")
		}
		return nil, model.NewError(model.CodeNoContract, "naive 无合同")
	}
	if w.Weight > c.MaxWeight {
		return nil, model.NewError(model.CodeOutOfRange, "naive 超重")
	}
	if c.MaxSide != 0 && (w.Dim.Length > c.MaxSide || w.Dim.Width > c.MaxSide ||
		w.Dim.Height > c.MaxSide) {
		return nil, model.NewError(model.CodeOutOfRange, "naive 超大")
	}

	vol := w.Dim.Length * w.Dim.Width * w.Dim.Height
	rawVol := ceilDiv64(vol, c.VolumeFactor)
	volW := ceilDiv64(rawVol, c.BillingUnit) * c.BillingUnit
	heavy := w.Weight
	if volW > heavy {
		heavy = volW
	}
	chg := ceilDiv64(heavy, c.BillingUnit) * c.BillingUnit

	fb := &model.FeeBreakdown{
		WaybillNumber: w.Number, CarrierID: c.CarrierID, ContractID: c.ID,
		PickupAt: w.PickupAt, ActualWeight: w.Weight, Volume: vol,
		RawVolumeWeight: rawVol, BillingUnit: c.BillingUnit,
		VolumeWeight: volW, ChargeableWeight: chg,
		MinimumCharge: c.MinimumCharge, FuelRatePerMille: c.FuelRatePerMille,
	}

	remaining := chg
	for _, t := range c.Tiers {
		if remaining <= t.Lower {
			break
		}
		top := t.Upper
		stop := remaining
		if top != 0 && top < stop {
			stop = top
		}
		part := stop - t.Lower
		if part > 0 {
			units := ceilDiv64(part, c.BillingUnit)
			amt := units * t.PricePerUnit
			fb.BaseFreight += amt
			fb.TierLines = append(fb.TierLines, model.TierLine{
				Lower: t.Lower, Upper: t.Upper, Units: units,
				PricePerUnit: t.PricePerUnit, Amount: amt,
			})
		}
	}
	fb.BaseAfterMinimum = fb.BaseFreight
	if c.MinimumCharge > fb.BaseAfterMinimum {
		fb.BaseAfterMinimum = c.MinimumCharge
	}
	fb.FuelSurcharge = ceilDiv64(fb.BaseAfterMinimum*c.FuelRatePerMille, 1000)
	for _, r := range c.RemoteRegions {
		if r == w.Route.To {
			fb.Remote = true
			fb.RemoteSurcharge = c.RemoteSurcharge
		}
	}
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
	if c.WeightThreshold > 0 && chg > c.WeightThreshold {
		fb.Overweight = true
		fb.OverweightSurcharge = c.OverweightSurcharge
	}
	fb.Total = fb.BaseAfterMinimum + fb.FuelSurcharge + fb.RemoteSurcharge +
		fb.OversizeSurcharge + fb.OverweightSurcharge

	n.priced[w.Number] = fb
	return fb, nil
}

// Settle 朴素结算。
func (n *NaiveModel) Settle(num string) (*model.FeeBreakdown, error) {
	if _, ok := n.settled[num]; ok {
		return nil, model.NewError(model.CodeAlreadySettled, "naive 已结算")
	}
	fb, ok := n.priced[num]
	if !ok {
		return nil, model.NewError(model.CodeInvalidArgument, "naive 未计价")
	}
	n.settled[num] = fb
	return fb, nil
}

// Settlement 查询朴素结算快照。
func (n *NaiveModel) Settlement(num string) *model.FeeBreakdown {
	return n.settled[num]
}

// Quote 朴素多承运商询价。
func (n *NaiveModel) Quote(req quoteReqSpec) []quoteLineSpec {
	carriers := append([]string(nil), req.carriers...)
	if len(carriers) == 0 {
		seen := map[string]bool{}
		for _, c := range n.contracts {
			if c.Route == req.route && c.Class == req.class {
				seen[c.CarrierID] = true
			}
		}
		for id := range seen {
			carriers = append(carriers, id)
		}
		sort.Strings(carriers)
	} else {
		// 与生产端一致：显式承运商列表先排序、去重。
		sort.Strings(carriers)
		kept := carriers[:0]
		for i, v := range carriers {
			if i > 0 && v == carriers[i-1] {
				continue
			}
			kept = append(kept, v)
		}
		carriers = kept
	}
	// 与生产端一致：成功项（总价、承运商升序）在前，失败项（承运商升序）在后。
	var ok, fail []quoteLineSpec
	for _, id := range carriers {
		w := &model.Waybill{
			Number: "__quote__", CarrierID: id, Route: req.route, Class: req.class,
			PickupAt: req.pickupAt, Weight: req.weight, Dim: req.dim,
		}
		fb, err := n.Price(w)
		if err != nil {
			fail = append(fail, quoteLineSpec{carrier: id, reason: model.CodeOf(err)})
		} else {
			ok = append(ok, quoteLineSpec{carrier: id, total: fb.Total, fb: fb})
		}
	}
	sort.Slice(ok, func(i, j int) bool {
		if ok[i].total != ok[j].total {
			return ok[i].total < ok[j].total
		}
		return ok[i].carrier < ok[j].carrier
	})
	sort.Slice(fail, func(i, j int) bool { return fail[i].carrier < fail[j].carrier })
	return append(ok, fail...)
}

// 朴素询价输入/输出的纯数据形态，避免直接依赖 system 包。
type quoteReqSpec struct {
	route    model.Route
	class    model.ServiceClass
	pickupAt model.Time
	weight   int64
	dim      model.Dimensions
	carriers []string
}

type quoteLineSpec struct {
	carrier string
	total   int64
	reason  model.Code
	fb      *model.FeeBreakdown
}
