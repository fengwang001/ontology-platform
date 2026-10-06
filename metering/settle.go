package metering

import (
	"math/big"
	"sort"
)

// allocate 把 total 按 weights 比例分摊为整数，结果之和恒等于 total。
// 采用最大余数法：先取 floor(total*w_i/W)，剩余名额按精确余数从大到小分配，
// 余数并列时按下标（户号升序）优先，保证确定性。total 为负时按对称规则处理。
func allocate(total int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	if total == 0 || len(weights) == 0 {
		return out
	}
	if total < 0 {
		neg := allocate(-total, weights)
		for i := range neg {
			neg[i] = -neg[i]
		}
		return neg
	}
	var wSum int64
	for _, w := range weights {
		wSum += w
	}
	if wSum <= 0 {
		return out // 调用方保证 total>0 时 wSum>0
	}
	bigW := big.NewInt(wSum)
	rems := make([]int64, len(weights))
	var floorSum int64
	for i, w := range weights {
		prod := new(big.Int).Mul(big.NewInt(total), big.NewInt(w))
		q := new(big.Int).Quo(prod, bigW).Int64()
		out[i] = q
		floorSum += q
		rems[i] = new(big.Int).Sub(prod, new(big.Int).Mul(big.NewInt(q), bigW)).Int64()
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return rems[order[a]] > rems[order[b]] })
	for k := int64(0); k < total-floorSum; k++ {
		out[order[k]]++
	}
	return out
}

// occupiedWithin 返回在住记录与 [s, e) 的重叠时长及是否存在相交记录。
func occupiedWithin(ivs []interval, s, e int64) (occupied int64, hasRecord bool) {
	for _, iv := range ivs {
		lo, hi := max(s, iv.start), min(e, iv.end)
		if lo < hi {
			hasRecord = true
			occupied += hi - lo
		}
	}
	return occupied, hasRecord
}

// computeBill 计算账期 [start, end) 的账单。allowNegative 用于更正重算，
// 允许公摊为负（按同一比例规则分摊负额）；正常结算拒绝负公摊。
func (s *Service) computeBill(start, end int64, allowNegative bool) (*Bill, *Error) {
	master := s.master.usageInRange(start, end, &s.stats)
	owns := make([]int64, len(s.units))
	weights := make([]int64, len(s.units))
	var ownSum int64
	for i, u := range s.units {
		owns[i] = u.usageInRange(start, end, &s.stats)
		ownSum += owns[i]
		occ, has := occupiedWithin(u.occupancy, start, end)
		if !has {
			occ = end - start // 整个账期无在住记录的空置分户仍全额参与公摊
		}
		weights[i] = u.area * occ
	}
	shared := master - ownSum
	if shared < 0 && !allowNegative {
		return nil, fail(ErrNegativeShared, "账期 [%d,%d) 总表用量 %d 小于分户自用合计 %d", start, end, master, ownSum)
	}
	if shared != 0 {
		var wSum int64
		for _, w := range weights {
			wSum += w
		}
		if wSum <= 0 {
			return nil, fail(ErrInvalidParam, "账期 [%d,%d) 公摊 %d 但无可分摊的分户", start, end, shared)
		}
	}
	shares := allocate(shared, weights)
	bill := &Bill{Start: start, End: end, MasterUsage: master, SharedUsage: shared}
	for i, u := range s.units {
		bill.Units = append(bill.Units, UnitBill{UnitID: u.id, Own: owns[i], Share: shares[i], Payable: shares[i]})
	}
	return bill, nil
}
