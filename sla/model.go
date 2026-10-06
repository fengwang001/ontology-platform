package sla

// Party 是延误责任方。
type Party int

const (
	PartyNone Party = iota
	PartyMerchant
	PartyRider
	PartyPlatform
	PartyUser
)

func (p Party) String() string {
	switch p {
	case PartyMerchant:
		return "merchant"
	case PartyRider:
		return "rider"
	case PartyPlatform:
		return "platform"
	case PartyUser:
		return "user"
	default:
		return "none"
	}
}

// Config 是系统构造参数。所有时间均为非负整数秒。
type Config struct {
	PromiseDuration  int64 // 承诺时长（接受 → 原始承诺送达时刻）
	MerchantPrep     int64 // 商家出餐容许时长
	PlatformDispatch int64 // 平台派单容许时长
	RiderPickup      int64 // 骑手取货容许时长
	UserAddrExtend   int64 // 用户改址固定延展量
	ExtendCap        int64 // 延展累计上限
	ClaimWindow      int64 // 赔付申请窗口（送达后 [deliverAt, deliverAt+ClaimWindow)）
	TierThresholds   []int64
	TierPayouts      []int64
}

// validate 检查构造参数合法性并返回档位的规整副本。
func (c Config) validate() error {
	if c.PromiseDuration < 0 || c.MerchantPrep < 0 || c.PlatformDispatch < 0 ||
		c.RiderPickup < 0 || c.UserAddrExtend < 0 || c.ExtendCap < 0 || c.ClaimWindow < 0 {
		return errf(CodeInvalidParam, "durations must be non-negative")
	}
	if len(c.TierThresholds) == 0 || len(c.TierThresholds) != len(c.TierPayouts) {
		return errf(CodeInvalidParam, "tiers must be non-empty and thresholds/payouts equal length")
	}
	for i, t := range c.TierThresholds {
		if t < 0 || c.TierPayouts[i] < 0 {
			return errf(CodeInvalidParam, "tier values must be non-negative")
		}
		if i > 0 && t <= c.TierThresholds[i-1] {
			return errf(CodeInvalidParam, "tier thresholds must be strictly increasing")
		}
	}
	return nil
}

// Tier 是延误档位（仅内部/测试使用）。
type Tier struct {
	Threshold int64
	Payout    int64
}

// LedgerEntry 是一笔已生效的裁决账目。
type LedgerEntry struct {
	OrderID   string
	At        int64 // 裁决发生时刻
	Amount    int64 // 实际赔付额（用户责任时为 0）
	Delay     int64 // 延误时长
	Party     Party // 裁决责任方
	Automatic bool  // 是否为平台自动裁决
}

func addInt64(a, b int64) (int64, bool) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, false
	}
	return s, true
}
