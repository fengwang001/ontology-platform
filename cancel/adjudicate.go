package cancel

// Actor 表示取消发起方。
type Actor int

const (
	ActorUser Actor = iota
	ActorMerchant
	ActorPlatform
	ActorRider
)

// decision 是责任裁决的内部结果。
type decision struct {
	liable     Party
	bearer     Party // 退款承担方：PartyMerchant 或 PartyPlatform
	fullRefund bool
	userBears  int64 // 用户有责时按比率折算的商品款承担部分
	penalty    bool  // 商家违约金
	riderComp  bool  // 骑手空跑补偿
	riderAbort bool  // 骑手取消（不落地，回到商家已接单）
	reason     string
}

// adjudicate 依据阶段、发起方与当前时刻裁决责任。
// 调用方需保证未处于争议待决/终态，并已完成迟到等前置校验。
// claimHeld 仅用于争议窗口落地：true 表示商家声明已开始备餐成立。
func adjudicate(cfg Config, stage Stage, actor Actor, amt Amounts, now int64, promise int64, claimHeld bool) decision {
	switch actor {
	case ActorUser:
		return adjudicateUser(cfg, stage, amt, now, promise, claimHeld)
	case ActorMerchant:
		d := decision{liable: PartyMerchant, bearer: PartyMerchant, fullRefund: true,
			riderComp: stage == StageAssigned || stage == StagePicked,
			penalty:   true}
		if d.riderComp {
			d.reason = "merchant_cancel: full refund by merchant, penalty paid, rider comp for dispatched rider"
		} else {
			d.reason = "merchant_cancel: full refund by merchant, penalty paid, no rider yet"
		}
		return d
	case ActorPlatform:
		d := decision{liable: PartyPlatform, bearer: PartyPlatform, fullRefund: true,
			riderComp: stage == StageAssigned || stage == StagePicked}
		if d.riderComp {
			d.reason = "platform_cancel: full refund by platform, rider comp for dispatched rider"
		} else {
			d.reason = "platform_cancel: full refund by platform, no rider yet"
		}
		return d
	case ActorRider:
		// 仅已派未取货允许；阶段校验由 service 完成。
		return decision{liable: PartyRider, riderAbort: true,
			reason: "rider_cancel: no landing, order returns to accepted for reassignment"}
	}
	return decision{reason: "unknown"}
}

func adjudicateUser(cfg Config, stage Stage, amt Amounts, now, promise int64, claimHeld bool) decision {
	// 争议窗口落地路径。
	if stage == StageAccepted || stage == StageAssigned {
		if claimHeld {
			bears := amt.Goods * cfg.PrepLossBP / 10000
			return decision{liable: PartyUser, bearer: PartyPlatform, fullRefund: false, userBears: bears,
				reason: "dispute held: prep started, user bears goods*loss_bp/10000 (floor), refund advanced by platform"}
		}
		return decision{liable: PartyUserNoFault, bearer: PartyMerchant, fullRefund: true,
			reason: "dispute not held: no claim in window or merchant waived, full refund borne by merchant"}
	}
	switch stage {
	case StagePaid:
		return decision{liable: PartyUserNoFault, bearer: PartyMerchant, fullRefund: true,
			reason: "user cancel before acceptance: no fault, full refund borne by merchant"}
	case StagePicked:
		// 迟到判定：now > promise + tolerance，恰等不算。
		if now > promise+cfg.LateTolerance {
			return decision{liable: PartyPlatform, bearer: PartyPlatform, fullRefund: true, riderComp: true,
				reason: "user cancel after pickup with late rider: platform liable, full refund, rider comp"}
		}
		return decision{reason: "not late: user cancel after pickup rejected"}
	}
	return decision{reason: "invalid state for user cancel"}
}
