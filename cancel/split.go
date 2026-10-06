package cancel

// 金额拆分与四方账目。
//
// 价值总盘 V = 用户实付 P + 优惠券面额 C。
// 守恒式：UserRefund + Merchant + Rider + Platform == P + C。

// splitRefund 根据裁决计算退款拆分。
func splitRefund(amt Amounts, d decision) Refund {
	paid := amt.Paid()
	if d.fullRefund {
		return Refund{CashRefund: paid, CouponRestored: amt.Coupon, FullRefund: true}
	}
	// 部分退款（仅争议成立、用户有责）。
	bears := d.userBears
	if bears > amt.Goods {
		bears = amt.Goods
	}
	cash := paid - bears
	if cash < 0 {
		// 实付为零（或小于承担额）时现金退款不为负：用户最多承担实付现金。
		cash = 0
	}
	// 优惠券折算：bears 占商品款比率折算，向下取整；余数归平台。
	var cm int64
	if amt.Coupon > 0 {
		cm = amt.Coupon * bears / amt.Goods
		if cm > amt.Coupon {
			cm = amt.Coupon
		}
	}
	return Refund{
		CashRefund:       cash,
		CouponRestored:   0,
		CouponToMerchant: cm,
		UserBears:        bears,
		FullRefund:       false,
	}
}

// settleLedger 依据裁决与退款拆分构造四方账目。
//
// 记账规则：
//   - 用户所得 = 现金退款 + 恢复优惠券面额；
//   - 骑手所得 = 空跑补偿（骑手取消时为 0）；
//   - 商家所得：
//     用户有责（争议成立）= 用户承担部分 L - 优惠券折算归商家部分 cm；
//     商家承担退款      = -退款总额 - 违约金 - 骑手补偿；
//     平台承担退款      = 0（垫付体现在平台兜底项）；
//   - 平台兜底：Platform = V - UserRefund - Merchant - Rider。
//     优惠券不恢复时面额 C 全部留存在平台侧；其中 cm 以券形式归商家，
//     折算余数与向下取整余量均归平台。
func settleLedger(amt Amounts, d decision, r Refund, cfg Config) Ledger {
	paid := amt.Paid()
	total := paid + amt.Coupon
	l := Ledger{UserRefund: r.CashRefund + r.CouponRestored}

	if d.riderComp {
		l.Rider = cfg.RiderComp
	}
	switch {
	case d.liable == PartyUser:
		// 争议成立：商家获得用户承担 L 中现金部分；券部分 cm 以券形式归商家。
		l.Merchant = r.UserBears - r.CouponToMerchant
	case d.bearer == PartyMerchant:
		// 商家承担全部退款成本（含恢复券面额），外加违约金/骑手补偿。
		penalty := cfg.MerchantPenalty
		if !d.penalty {
			penalty = 0
		}
		l.Merchant = -l.UserRefund - penalty - l.Rider
	default:
		// 平台承担退款：平台从代收资金中退还并保留券成本，无需商家扣款。
		l.Merchant = 0
	}
	l.Platform = total - l.UserRefund - l.Merchant - l.Rider
	return l
}
