// Package freight 实现承运商运价合同的运费计算与结算。
package freight

// ServiceLevel 服务等级。
type ServiceLevel int

const (
	Standard ServiceLevel = iota // 标准
	Express                      // 加急
)

// Lane 线路：起点区域 -> 终点区域。
type Lane struct {
	Origin string
	Dest   string
}

// WeightTier 重量阶梯的一档：左闭右开重量区间 [Lower, Upper)，
// PricePerUnit 为该档内每计重单位的单价（整数分）。
type WeightTier struct {
	Lower        int64
	Upper        int64
	PricePerUnit int64
}

// Contract 运价合同。所有金额均为整数分，时间为左闭右开区间 [Start, End)。
type Contract struct {
	ID              string
	CarrierID       string
	Lane            Lane
	Level           ServiceLevel
	Start           int64
	End             int64
	VolumeDivisor   int64 // 体积折算系数：体积重量 = ceil(体积 / 系数)
	BillingUnit     int64 // 计重单位（计费重量向上取整到其整数倍）
	Tiers           []WeightTier
	FuelPermille    int64    // 燃油附加费比率（千分比）
	RemoteRegions   []string // 偏远区域清单
	RemoteFee       int64    // 偏远附加费定额（分）
	DimThreshold    int64    // 单边尺寸阈值
	DimExcessFee    int64    // 单边超限附加费定额（分）
	WeightThreshold int64    // 计费重量阈值
	WeightExcessFee int64    // 计费重量超限附加费定额（分）
	MinCharge       int64    // 最低收费（分）
	MaxWeight       int64    // 可承运最大实际重量
	MaxDim          int64    // 可承运最大单边尺寸
}

// Waybill 运单。
type Waybill struct {
	ID           string
	CarrierID    string
	Lane         Lane
	Level        ServiceLevel
	PickupTime   int64 // 揽收时刻，用于选择合同
	ActualWeight int64
	Volume       int64
	Dims         [3]int64 // 三条边尺寸
}

// QuoteRequest 多承运商询价请求（不含运单号与承运商）。
type QuoteRequest struct {
	Lane         Lane
	Level        ServiceLevel
	PickupTime   int64
	ActualWeight int64
	Volume       int64
	Dims         [3]int64
}

// FeeBreakdown 可复现的费用明细。
type FeeBreakdown struct {
	ContractID      string
	BillingWeight   int64 // 计费重量（取整到计重单位后）
	BaseFee         int64 // 累进阶梯基础运费（最低收费提升前）
	BaseAfterMin    int64 // 最低收费提升后的基础运费
	FuelFee         int64 // 燃油附加费（作用于 BaseAfterMin，向上取整）
	RemoteFee       int64 // 偏远附加费
	DimExcessFee    int64 // 单边尺寸超限附加费
	WeightExcessFee int64 // 计费重量超限附加费
	Total           int64 // 合计
}

// CarrierQuote 单个承运商的询价结果：成功时 Breakdown 非空，失败时 Reason 非空。
type CarrierQuote struct {
	CarrierID string
	Breakdown *FeeBreakdown
	Reason    *Error
}
