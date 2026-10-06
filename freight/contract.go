package freight

import "sort"

// contractKey 合同索引键：承运商 + 线路 + 服务等级。
type contractKey struct {
	carrier string
	lane    Lane
	level   ServiceLevel
}

// pricedContract 合同及其预计算的阶梯前缀和，用于 O(log 档数) 累进计价。
type pricedContract struct {
	contract Contract
	remote   map[string]bool
	// tierPrefix[i] 为前 i 档（完整档）的累计运费，长度为 len(Tiers)+1。
	tierPrefix []int64
}

// contractSet 同一 contractKey 下的合同集合。
// 生效区间互不重叠（相接允许），按 Start 升序排列，支持二分查找与插入。
type contractSet struct {
	items []pricedContract // 按 contract.Start 升序
}

// validateContract 校验合同参数，非法时返回 ErrInvalidParam。
func validateContract(c *Contract) *Error {
	if c.CarrierID == "" {
		return newError(ErrInvalidParam, "承运商编号为空")
	}
	if c.Lane.Origin == "" || c.Lane.Dest == "" {
		return newError(ErrInvalidParam, "线路区域为空")
	}
	if c.Level != Standard && c.Level != Express {
		return newError(ErrInvalidParam, "服务等级非法: %d", c.Level)
	}
	if c.End <= c.Start {
		return newError(ErrInvalidParam, "生效区间非法: [%d, %d)", c.Start, c.End)
	}
	if c.VolumeDivisor <= 0 {
		return newError(ErrInvalidParam, "体积折算系数必须为正: %d", c.VolumeDivisor)
	}
	if c.BillingUnit <= 0 {
		return newError(ErrInvalidParam, "计重单位必须为正: %d", c.BillingUnit)
	}
	if len(c.Tiers) == 0 {
		return newError(ErrInvalidParam, "重量阶梯为空")
	}
	if c.Tiers[0].Lower != 0 {
		return newError(ErrInvalidParam, "重量阶梯首档必须从 0 开始")
	}
	prev := c.Tiers[0]
	if prev.Lower >= prev.Upper {
		return newError(ErrInvalidParam, "阶梯区间非法: [%d, %d)", prev.Lower, prev.Upper)
	}
	if prev.PricePerUnit < 0 {
		return newError(ErrInvalidParam, "阶梯单价为负: %d", prev.PricePerUnit)
	}
	if prev.Lower%c.BillingUnit != 0 || prev.Upper%c.BillingUnit != 0 {
		return newError(ErrInvalidParam, "阶梯边界必须是计重单位的整数倍")
	}
	for i := 1; i < len(c.Tiers); i++ {
		t := c.Tiers[i]
		if t.Lower >= t.Upper {
			return newError(ErrInvalidParam, "阶梯区间非法: [%d, %d)", t.Lower, t.Upper)
		}
		if t.Lower != prev.Upper {
			return newError(ErrInvalidParam, "阶梯必须连续相接: 档%d 起点 %d != 上一档终点 %d", i, t.Lower, prev.Upper)
		}
		if t.PricePerUnit < 0 {
			return newError(ErrInvalidParam, "阶梯单价为负: %d", t.PricePerUnit)
		}
		if t.Upper%c.BillingUnit != 0 {
			return newError(ErrInvalidParam, "阶梯边界必须是计重单位的整数倍")
		}
		prev = t
	}
	if c.FuelPermille < 0 {
		return newError(ErrInvalidParam, "燃油附加费比率为负: %d", c.FuelPermille)
	}
	if c.RemoteFee < 0 || c.DimExcessFee < 0 || c.WeightExcessFee < 0 || c.MinCharge < 0 {
		return newError(ErrInvalidParam, "费用定额不得为负")
	}
	if c.DimThreshold < 0 || c.WeightThreshold < 0 {
		return newError(ErrInvalidParam, "阈值不得为负")
	}
	if c.MaxWeight <= 0 {
		return newError(ErrInvalidParam, "可承运最大重量必须为正: %d", c.MaxWeight)
	}
	if c.MaxDim <= 0 {
		return newError(ErrInvalidParam, "可承运最大单边尺寸必须为正: %d", c.MaxDim)
	}
	return nil
}

// newPricedContract 构造带前缀和与偏远区域索引的合同。
func newPricedContract(c Contract) pricedContract {
	pc := pricedContract{contract: c, remote: make(map[string]bool, len(c.RemoteRegions))}
	for _, r := range c.RemoteRegions {
		pc.remote[r] = true
	}
	pc.tierPrefix = make([]int64, len(c.Tiers)+1)
	for i, t := range c.Tiers {
		units := (t.Upper - t.Lower) / c.BillingUnit
		pc.tierPrefix[i+1] = pc.tierPrefix[i] + units*t.PricePerUnit
	}
	return pc
}

// locate 返回满足 Start <= t 的最后一份合同下标，不存在时返回 -1。
func (s *contractSet) locate(t int64) int {
	i := sort.Search(len(s.items), func(i int) bool { return s.items[i].contract.Start > t })
	return i - 1
}

// find 查找覆盖时刻 t 的合同，未覆盖返回 nil。
func (s *contractSet) find(t int64) *pricedContract {
	i := s.locate(t)
	if i < 0 || s.items[i].contract.End <= t {
		return nil
	}
	return &s.items[i]
}

// overlaps 判断区间 [start, end) 是否与已有合同重叠（相接不算重叠）。
func (s *contractSet) overlaps(start, end int64) bool {
	i := s.locate(start)
	if i >= 0 && s.items[i].contract.End > start {
		return true
	}
	// 后继合同起点落在区间内也算重叠。
	if i+1 < len(s.items) && s.items[i+1].contract.Start < end {
		return true
	}
	return false
}

// add 插入合同，调用前须保证无重叠。
func (s *contractSet) add(pc pricedContract) {
	i := s.locate(pc.contract.Start) + 1
	s.items = append(s.items, pricedContract{})
	copy(s.items[i+1:], s.items[i:])
	s.items[i] = pc
}
