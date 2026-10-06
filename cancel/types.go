package cancel

// Stage 为订单履约阶段，严格按数值递增。
type Stage int

const (
	StagePaid      Stage = iota // 已支付
	StageAccepted               // 商家已接单
	StageAssigned               // 骑手已派
	StagePicked                 // 已取货
	StageDelivered              // 已送达
)

func (s Stage) String() string {
	switch s {
	case StagePaid:
		return "paid"
	case StageAccepted:
		return "accepted"
	case StageAssigned:
		return "assigned"
	case StagePicked:
		return "picked"
	case StageDelivered:
		return "delivered"
	default:
		return "?"
	}
}

// Party 为责任方。
type Party int

const (
	PartyNone        Party = iota // 无落地取消（如骑手取消回到待改派）
	PartyUser                     // 用户有责
	PartyUserNoFault              // 用户无责（取消落地，退款由商家或平台承担）
	PartyMerchant                 // 商家
	PartyPlatform                 // 平台
	PartyRider                    // 骑手
)

func (p Party) String() string {
	switch p {
	case PartyNone:
		return "none"
	case PartyUser:
		return "user"
	case PartyUserNoFault:
		return "user_no_fault"
	case PartyMerchant:
		return "merchant"
	case PartyPlatform:
		return "platform"
	case PartyRider:
		return "rider"
	default:
		return "?"
	}
}

// Amounts 为订单金额构成，单位均为分（非负整数）。
type Amounts struct {
	Goods    int64 // 商品款
	Packing  int64 // 包装费
	Delivery int64 // 配送费
	Coupon   int64 // 优惠券抵扣额（<= Goods）
}

// Paid 为用户实付：Goods + Packing + Delivery - Coupon，构造时保证非负。
func (a Amounts) Paid() int64 { return a.Goods + a.Packing + a.Delivery - a.Coupon }

// Order 为单笔订单的不可变标识与金额。
type Order struct {
	ID              string
	Amounts         Amounts
	PromiseDelivery int64 // 承诺送达时刻（秒）
}

// Refund 描述一次取消落地的退款拆分。
type Refund struct {
	CashRefund       int64 // 退还用户的现金（分）
	CouponRestored   int64 // 恢复可用的优惠券面额（分）
	CouponToMerchant int64 // 视为已消耗、归商家的优惠券部分
	UserBears        int64 // 用户按比率承担的商品款部分（名义值）
	FullRefund       bool  // 是否全额退款
}

// Ledger 为取消落地后四方账目（带符号，单位分）。
// 对用户/商家/骑手：正数为所得；对平台：正数为平台净所得，可为负。
// 守恒：UserRefund + Merchant + Rider + Platform == 用户实付 + 优惠券抵扣额。
type Ledger struct {
	UserRefund int64 // 用户所得：现金退款 + 恢复优惠券面额
	Merchant   int64
	Rider      int64
	Platform   int64
}

// CancelResult 为取消类操作的结果。
type CancelResult struct {
	OrderID string
	Land    bool  // 取消是否落地（骑手取消不落地；争议提交不落地）
	Liable  Party // 责任方（未落地时为 PartyNone）
	Refund  Refund
	Ledger  Ledger
	At      int64  // 生效时刻
	Reason  string // 判定依据（用于日志复现）
}

// Config 为系统构造参数。
type Config struct {
	DisputeWindow   int64 // 商家确认备餐的争议窗口时长（秒）
	LateTolerance   int64 // 骑手迟到判定容许时长（秒）
	PrepLossBP      int64 // 备餐损失比率（万分之一为单位，如 3000 表示 30%）
	MerchantPenalty int64 // 商家违约金额（分）
	RiderComp       int64 // 骑手空跑补偿额（分）
}
