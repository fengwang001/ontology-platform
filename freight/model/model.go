package model

// Time 为离散时刻（约定的时间戳整数），相等性与先后均可精确判定。
type Time int64

// ServiceClass 服务等级。
type ServiceClass string

const (
	Standard ServiceClass = "标准"
	Express  ServiceClass = "加急"
)

// Region 区域标识。
type Region string

// Route 线路：起点区域 + 终点区域。
type Route struct {
	From Region
	To   Region
}

// Dimensions 三边尺寸（毫米）。
type Dimensions struct {
	Length int64
	Width  int64
	Height int64
}

// WeightTier 重量阶梯：[Lower, Upper) 左闭右开，PricePerUnit 为每计重单位单价（分）。
type WeightTier struct {
	Lower        int64
	Upper        int64 // 0 表示正无穷（最后一档）
	PricePerUnit int64
}

// Contract 承运商运价合同。所有金额为整数分，所有重量为克，所有尺寸为毫米。
type Contract struct {
	ID        string
	CarrierID string
	Route     Route
	Class     ServiceClass

	EffectiveAt Time
	ExpiresAt   Time // 生效区间 [EffectiveAt, ExpiresAt) 左闭右开

	VolumeFactor        int64
	BillingUnit         int64
	Tiers               []WeightTier
	FuelRatePerMille    int64
	RemoteRegions       []Region
	RemoteSurcharge     int64
	SideThreshold       int64
	WeightThreshold     int64
	OversizeSurcharge   int64
	OverweightSurcharge int64
	MinimumCharge       int64
	MaxWeight           int64
	MaxSide             int64
}

// Waybill 运单。重量为克，尺寸为毫米，揽收时刻决定适用合同。
type Waybill struct {
	Number    string
	CarrierID string // 指定承运商；多承运商询价时由询价入口逐家代入
	Route     Route
	Class     ServiceClass
	PickupAt  Time
	Weight    int64
	Dim       Dimensions
}

// TierLine 一档阶梯的计价明细。
type TierLine struct {
	Lower        int64
	Upper        int64
	Units        int64
	PricePerUnit int64
	Amount       int64
}

// FeeBreakdown 一次计价的完整可复现明细。
type FeeBreakdown struct {
	WaybillNumber string
	CarrierID     string
	ContractID    string
	PickupAt      Time

	ActualWeight     int64
	Volume           int64
	RawVolumeWeight  int64
	BillingUnit      int64
	VolumeWeight     int64
	ChargeableWeight int64

	BaseFreight      int64
	TierLines        []TierLine
	MinimumCharge    int64
	BaseAfterMinimum int64

	FuelRatePerMille    int64
	FuelSurcharge       int64
	Remote              bool
	RemoteSurcharge     int64
	Oversize            bool
	OversizeSurcharge   int64
	Overweight          bool
	OverweightSurcharge int64
	Total               int64
}

// Validate 校验合同参数。返回 nil 表示合法。校验不修改合同内容，
// 偏远清单的排序去重在 store.Add 中通过拷贝完成。
func (c *Contract) Validate() error {
	bad := func(format string, args ...any) error {
		return errf(CodeInvalidArgument, format, args...)
	}
	if c.ID == "" {
		return bad("合同编号为空")
	}
	if c.CarrierID == "" {
		return bad("承运商编号为空")
	}
	if c.Route.From == "" || c.Route.To == "" {
		return bad("线路起点或终点为空")
	}
	if c.Class != Standard && c.Class != Express {
		return bad("服务等级非法: %q", c.Class)
	}
	if c.EffectiveAt >= c.ExpiresAt {
		return bad("生效区间必须左闭右开且非空: [%d,%d)", c.EffectiveAt, c.ExpiresAt)
	}
	if c.VolumeFactor <= 0 {
		return bad("体积折算系数必须为正: %d", c.VolumeFactor)
	}
	if c.BillingUnit <= 0 {
		return bad("计重单位必须为正: %d", c.BillingUnit)
	}
	if len(c.Tiers) == 0 {
		return bad("重量阶梯为空")
	}
	for i := range c.Tiers {
		t := c.Tiers[i]
		if t.Lower < 0 || t.PricePerUnit < 0 {
			return bad("第 %d 档下界或单价为负", i)
		}
		if t.Upper != 0 && t.Upper <= t.Lower {
			return bad("第 %d 档区间非正长: [%d,%d)", i, t.Lower, t.Upper)
		}
		if i > 0 {
			prev := c.Tiers[i-1]
			if prev.Upper == 0 || prev.Upper != t.Lower {
				return bad("阶梯必须首尾相接且无空档: 第 %d 档上界 %d != 第 %d 档下界 %d",
					i-1, prev.Upper, i, t.Lower)
			}
		}
	}
	if c.Tiers[0].Lower != 0 {
		return bad("首档下界必须为 0, 实际 %d", c.Tiers[0].Lower)
	}
	if c.Tiers[len(c.Tiers)-1].Upper != 0 {
		return bad("末档必须以 0 表示正无穷上界")
	}
	for i := 0; i+1 < len(c.Tiers); i++ {
		if c.Tiers[i].Lower%c.BillingUnit != 0 {
			return bad("第 %d 档下界 %d 不是计重单位 %d 的整数倍", i, c.Tiers[i].Lower, c.BillingUnit)
		}
	}
	lastLower := c.Tiers[len(c.Tiers)-1].Lower
	if lastLower%c.BillingUnit != 0 {
		return bad("末档下界 %d 不是计重单位 %d 的整数倍", lastLower, c.BillingUnit)
	}
	if c.FuelRatePerMille < 0 {
		return bad("燃油附加费千分比为负: %d", c.FuelRatePerMille)
	}
	if c.RemoteSurcharge < 0 || c.OversizeSurcharge < 0 || c.OverweightSurcharge < 0 ||
		c.MinimumCharge < 0 {
		return bad("附加费或最低收费为负")
	}
	if c.SideThreshold < 0 || c.WeightThreshold < 0 {
		return bad("计费阈值为负")
	}
	if c.MaxWeight <= 0 {
		return bad("可承运最大重量必须为正: %d", c.MaxWeight)
	}
	if c.MaxSide < 0 {
		return bad("最大单边尺寸为负: %d", c.MaxSide)
	}
	for _, r := range c.RemoteRegions {
		if r == "" {
			return bad("偏远区域清单含空区域")
		}
	}
	return nil
}

// Validate 校验运单参数。
func (w *Waybill) Validate() error {
	bad := func(format string, args ...any) error {
		return errf(CodeInvalidArgument, format, args...)
	}
	if w.Number == "" {
		return bad("运单号为空")
	}
	if w.CarrierID == "" {
		return bad("承运商编号为空")
	}
	if w.Route.From == "" || w.Route.To == "" {
		return bad("线路起点或终点为空")
	}
	if w.Class != Standard && w.Class != Express {
		return bad("服务等级非法: %q", w.Class)
	}
	if w.Weight <= 0 {
		return bad("实际重量必须为正: %d", w.Weight)
	}
	if w.Dim.Length < 0 || w.Dim.Width < 0 || w.Dim.Height < 0 {
		return bad("尺寸为负")
	}
	return nil
}
