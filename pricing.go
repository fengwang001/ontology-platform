package baggage

// bagUnit 一件参与计费的行李：GlobalIndex 为整条行程输入内的序号（1 起）。
type bagUnit struct {
	globalIndex int
	passengerID string
	tier        Tier
	weight      Weight
}

// priceConsignment 按本段托运适用承运人的制式与单价计算费用。
// 先按全局序号扫描绝对上限（命中即拒收且不收费），再计费用；
// 总开销 O(件数)，与历史记录、机场总数无关。
func priceConsignment(
	segs []Segment,
	bags []bagUnit,
	cfg Config,
	reg *Registry,
) (Money, AllowanceMode, string) {
	carrierID := applicableCarrierID(segs, reg)
	carrier, _ := reg.carrier(carrierID)

	for _, bag := range bags {
		if bag.weight > carrier.AbsWeight {
			return 0, carrier.Mode, carrierID
		}
	}

	var fee Money
	if carrier.Mode == ModePiece {
		fee = pricePiece(carrier, bags, cfg)
	} else {
		fee = priceWeight(carrier, bags, cfg)
	}
	return fee, carrier.Mode, carrierID
}

// pricePiece 计件制：免费件数按人不合并；超重按件计一次，与超件可叠加于同一件。
func pricePiece(carrier Carrier, bags []bagUnit, cfg Config) Money {
	used := make(map[string]int)
	var fee Money
	for _, bag := range bags {
		n := used[bag.passengerID]
		freePieces := carrier.FreePieces + cfg.TierExtraPiece[bag.tier]
		if n >= freePieces {
			fee += carrier.ExtraPieceRate
		}
		used[bag.passengerID] = n + 1
		if bag.weight > carrier.PieceFreeWeight {
			fee += carrier.OverweightRate
		}
	}
	return fee
}

// priceWeight 计重制：基础免费总重量全组共享；会员额外重量只抵扣本人，不转移。
// 费用 = max(0, 各旅客超出(基础+本人额外)的重量之和 - 其余旅客未用满的基础额度) * 单价。
func priceWeight(carrier Carrier, bags []bagUnit, cfg Config) Money {
	byPassenger := make(map[string][]bagUnit)
	order := make([]string, 0)
	for _, bag := range bags {
		if _, ok := byPassenger[bag.passengerID]; !ok {
			order = append(order, bag.passengerID)
		}
		byPassenger[bag.passengerID] = append(byPassenger[bag.passengerID], bag)
	}

	var excess, unusedBase Weight
	for _, id := range order {
		pb := byPassenger[id]
		var own Weight
		for _, bag := range pb {
			own += bag.weight
		}
		extra := cfg.TierExtraWeight[pb[0].tier]
		// 会员额外额度先抵扣本人，剩余不转移；基础额度用不完的部分进入共享池。
		baseUsed := own - extra
		if baseUsed < 0 {
			baseUsed = 0
		}
		if baseUsed > carrier.FreeWeight {
			excess += baseUsed - carrier.FreeWeight
			baseUsed = carrier.FreeWeight
		}
		unusedBase += carrier.FreeWeight - baseUsed
	}
	chargeable := excess
	if unusedBase < chargeable {
		chargeable -= unusedBase
	} else {
		chargeable = 0
	}
	return Money(chargeable) * carrier.PerUnitRate
}
