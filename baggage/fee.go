package baggage

import "fmt"

// SectionFee 计算一段托运的费用；若任一行李超过该段适用承运人的绝对重量上限，
// 返回 ErrOverweight 及第一件超限行李的全局序号（按旅客顺序、行李顺序从 1 起）。
// 纯函数，开销 O(旅客数+行李数)。
func SectionFee(segs []Segment, pax []PassengerBags, airports map[string]Airport, carriers map[string]Carrier, tiers map[string]Tier) (int64, *Error) {
	car := carriers[AllowanceCarrier(segs, airports)]

	seq := 0
	for _, p := range pax {
		for _, w := range p.Bags {
			seq++
			if w > car.AbsWeight {
				return 0, &Error{Code: ErrOverweight, BagSeq: seq,
					Msg: fmt.Sprintf("第 %d 件行李重量 %d 超过承运人 %s 绝对上限 %d", seq, w, car.Code, car.AbsWeight)}
			}
		}
	}

	switch car.Policy {
	case PiecePolicy:
		var fee int64
		for _, p := range pax {
			free := car.FreePieces + tiers[p.Tier].ExtraPieces
			if n := int64(len(p.Bags)); n > free {
				fee += (n - free) * car.PieceFee
			}
			for _, w := range p.Bags {
				if w > car.PieceFreeWeight {
					fee += car.OverweightFee
				}
			}
		}
		return fee, nil
	default: // WeightPolicy
		// 免费总重量在同记录旅客间合并共享；会员额外重量只抵扣本人行李。
		var remaining, pool int64
		for _, p := range pax {
			var w int64
			for _, b := range p.Bags {
				w += b
			}
			if w -= tiers[p.Tier].ExtraWeight; w > 0 {
				remaining += w
			}
			pool += car.FreeTotalWeight
		}
		if remaining > pool {
			return (remaining - pool) * car.UnitFee, nil
		}
		return 0, nil
	}
}
