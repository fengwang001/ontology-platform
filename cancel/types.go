package cancel

// Package cancel 实现即时配送订单的取消责任判定与退款分摊：
// 订单沿 已支付->已接单->已派->已取货->已送达 推进；用户、商家、平台、
// 骑手在不同阶段的取消按争议窗口与迟到规则裁决责任方，再把用户实付与
// 优惠券抵扣额在用户、商家、骑手、平台四方之间守恒拆分。
//
// 主要入口：
//   - NewSystem 构造系统（校验争议窗口、迟到容许、损失比率等参数）；
//   - CreateOrder/Accept/Assign/Pickup/Deliver 推进订单阶段；
//   - UserCancel/MerchantCancel/PlatformCancel/RiderCancel 发起取消；
//   - ClaimPreparation/WaiveClaim 处理备餐争议；
//   - Advance 推进全局时钟并让争议窗口到期落地。
//
// 所有错误均为 *CancelError，可用 ErrKind 程序化区分；被拒绝的操作不改变
// 任何阶段、账目与时钟。

// Stage 是订单履约阶段，只能按次序单调推进。
type Stage uint8

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
		return "unknown"
	}
}

// Party 标识责任方 / 账目主体。None 表示订单仍存活、尚无责任裁决。
type Party uint8

const (
	PartyNone Party = iota
	PartyUser
	PartyMerchant
	PartyPlatform
	PartyRider
)

func (p Party) String() string {
	switch p {
	case PartyUser:
		return "user"
	case PartyMerchant:
		return "merchant"
	case PartyPlatform:
		return "platform"
	case PartyRider:
		return "rider"
	default:
		return "none"
	}
}

// Amounts 为订单金额构成，单位均为非负整数分。
type Amounts struct {
	Goods    int64 // 商品款
	Packing  int64 // 包装费
	Delivery int64 // 配送费
	Coupon   int64 // 优惠券抵扣额（不超过商品款）
}

// Paid 是用户实付 = 商品款 + 包装费 + 配送费 - 优惠券抵扣额。
func (a Amounts) Paid() int64 { return a.Goods + a.Packing + a.Delivery - a.Coupon }

// Params 为系统构造参数，对全部订单生效。
type Params struct {
	DisputeWindow     int64 // 商家确认备餐的争议窗口时长（秒）
	LateTolerance     int64 // 骑手迟到判定容许时长（秒），当前时刻 > 承诺时刻 + 该值才算迟到
	LossBasisPoints   int64 // 备餐损失比率，单位万分之一，取值 [0,10000]
	MerchantPenalty   int64 // 商家违约金额（分）
	RiderCompensation int64 // 骑手空跑补偿额（分）
}

// OrderSpec 描述一笔新订单。
type OrderSpec struct {
	Amounts      Amounts
	PromisedTime int64 // 承诺送达时刻（整数秒）
}
