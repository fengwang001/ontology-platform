package cancel

// Ledger 记录一笔订单取消落地后的四方账目，单位分。
type Ledger struct {
	UserRefundCash  int64 // 用户收到的现金退款
	CouponRestored  int64 // 用户恢复的优惠券额
	MerchantGain    int64
	RiderGain       int64
	PlatformGain    int64 // 可为负
	MerchantPenalty int64 // 商家支付的违约金（独立于退款的转移）
	RiderComp       int64 // 骑手获得的空跑补偿（独立于退款的转移）
	UserBorne       int64 // 用户承担的备餐损失（商品款折算部分）
	CouponConsumed  int64 // 优惠券视为已消耗、归商家的部分
	CouponPlatform  int64 // 优惠券未消耗、归平台的部分
}

// UserGain 是用户侧总所得（现金退款 + 恢复的优惠券）。
func (l Ledger) UserGain() int64 { return l.UserRefundCash + l.CouponRestored }

// Conserved 校验四方之和恰等于 用户实付 + 优惠券抵扣额。
func (l Ledger) Conserved(a Amounts) bool {
	return l.UserGain()+l.MerchantGain+l.RiderGain+l.PlatformGain == a.Paid()+a.Coupon
}

// landingKind 区分取消落地的资金场景；裁决模块决定使用哪一种。
type landingKind uint8

const (
	landFullMerchant     landingKind = iota // 商家承担全额退款（商家取消）
	landFullPlatform                        // 平台承担全额退款（平台取消、骑手迟到）
	landFullWindowExpiry                    // 争议窗口未成立，商家承担退款、无违约金
	landFullPreAccept                       // 接单前用户取消，平台承担退款
	landUserFault                           // 备餐声明成立，用户承担部分损失
)

// settleLedger 依据落地场景做退款拆分。约定：支付后平台持有全部资金池
// （用户实付现金 + 优惠券抵扣额），四方账目为各自相对于该时点的最终余额，
// 因此四者之和恒等于 Paid + Coupon。平台余额可为负。
func settleLedger(a Amounts, p Params, kind landingKind, riderAssigned bool) Ledger {
	paid := a.Paid()
	pot := paid + a.Coupon
	comp := int64(0)
	if riderAssigned {
		comp = p.RiderCompensation
	}

	if kind == landUserFault {
		// 用户有责：商品款按损失比率折算（向下取整），优惠券不恢复。
		borne := a.Goods * p.LossBasisPoints / 10000
		borneCash := borne
		if borneCash > paid {
			borneCash = paid // 退款额不为负：用户承担现金以实付为上限
		}
		consumed := int64(0)
		if a.Goods > 0 {
			consumed = a.Coupon * borne / a.Goods // 向下取整，余数归平台
		}
		l := Ledger{
			UserRefundCash: paid - borneCash,
			UserBorne:      borne,
			CouponConsumed: consumed,
			CouponPlatform: a.Coupon - consumed,
			MerchantGain:   borneCash + consumed,
			PlatformGain:   a.Coupon - consumed,
		}
		return l
	}

	l := Ledger{
		UserRefundCash: paid,
		CouponRestored: a.Coupon,
		RiderComp:      comp,
		RiderGain:      comp,
	}
	switch kind {
	case landFullMerchant:
		// 商家承担退款 P，付违约金 M，派了骑手再付空跑补偿 r。
		l.MerchantPenalty = p.MerchantPenalty
		l.MerchantGain = -(paid + p.MerchantPenalty + comp)
		l.PlatformGain = pot - l.UserGain() - l.MerchantGain - l.RiderGain
	case landFullPlatform:
		// 平台承担退款，并向已派骑手支付空跑补偿。
		l.MerchantGain = 0
		l.PlatformGain = pot - l.UserGain() - l.RiderGain
	case landFullWindowExpiry:
		// 商家承担退款（无违约金），骑手空跑补偿由商家出。
		l.MerchantGain = -(paid + comp)
		l.PlatformGain = pot - l.UserGain() - l.MerchantGain - l.RiderGain
	case landFullPreAccept:
		// 商家尚未接单，平台把资金池全额退还。
		l.MerchantGain = 0
		l.PlatformGain = pot - l.UserGain() - l.RiderGain
	}
	return l
}
