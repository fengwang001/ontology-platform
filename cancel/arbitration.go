package cancel

import "fmt"

// Decision 是一次取消落地的责任裁决与金额拆分结果。
type Decision struct {
	Liable  Party  // 责任方（谁承担退款）
	Reason  string // 判定依据（用于日志与复现）
	Ledger  Ledger // 落地后的四方账目（Pending 时为零值）
	Pending bool   // 取消未落地，进入/处于争议窗口
	EndsAt  int64  // 争议窗口右端点（Pending 时有效）
	Landed  bool   // 本次裁决是否完成取消落地
}

type disputeState uint8

const (
	disputeNone    disputeState = iota // 无争议
	disputeOpen                        // 争议窗口进行中
	disputeClaimed                     // 商家已在窗口内声明备餐
	disputeWaived                      // 商家放弃声明
)

type orderState struct {
	id         string
	amounts    Amounts
	promised   int64
	stage      Stage
	riderID    string
	pending    disputeState
	windowEnd  int64
	windowSeq  uint64
	landed     bool
	liable     Party
	ledger     Ledger
	reason     string
	riderCancs int
}

// adjudicate 对“取消请求”做责任裁决。调用方须已完成参数、时钟、订单存在性、
// 终态、争议待决等统一前置检查，并保证骑手取消的身份匹配。
// actor 为发起取消的一方；返回 Pending 表示进入争议窗口、本次未落地。
func adjudicate(o *orderState, p Params, actor Party, now int64) Decision {
	switch actor {
	case PartyUser:
		switch o.stage {
		case StagePaid:
			return land(o, p, PartyPlatform, landFullPreAccept, false,
				"user cancels before merchant acceptance: no fault, platform bears full refund")
		case StageAccepted, StageAssigned:
			return Decision{Pending: true, EndsAt: now + p.DisputeWindow,
				Reason: "user cancels after acceptance before pickup: dispute window opened"}
		case StagePicked:
			lateAt := o.promised + p.LateTolerance
			if now <= lateAt { // 恰等不算迟到
				// 调用方把 KindNotCancellable 作为唯一例外上抛。
				return Decision{Reason: fmt.Sprintf("not late: now=%d <= promised+tolerance=%d", now, lateAt)}
			}
			return land(o, p, PartyPlatform, landFullPlatform, true,
				"user cancels after pickup with late rider: platform at fault, full refund and rider compensation")
		}
	case PartyMerchant:
		return land(o, p, PartyMerchant, landFullMerchant, o.stage >= StageAssigned,
			"merchant cancels: merchant at fault, full refund plus penalty and rider compensation if assigned")
	case PartyPlatform:
		return land(o, p, PartyPlatform, landFullPlatform, o.stage >= StageAssigned,
			"platform cancels: platform at fault, full refund and rider compensation if assigned")
	}
	return Decision{}
}

func land(o *orderState, p Params, liable Party, kind landingKind, riderAssigned bool, reason string) Decision {
	l := settleLedger(o.amounts, p, kind, riderAssigned)
	o.landed = true
	o.liable = liable
	o.ledger = l
	o.reason = reason
	return Decision{Liable: liable, Reason: reason, Ledger: l, Landed: true}
}

// claimDispute 落地“商家在争议窗口内声明已开始备餐”。窗口右端点声明不允许。
func claimDispute(o *orderState, p Params, now int64) Decision {
	if o.pending != disputeOpen {
		return Decision{}
	}
	if now >= o.windowEnd { // 恰在右端点不允许
		return Decision{}
	}
	o.pending = disputeClaimed
	return land(o, p, PartyUser, landUserFault, false,
		"merchant claimed preparation within dispute window: user at fault, goods loss borne by user")
}

// waiveDispute 落地“商家主动放弃声明”，用户无责全额退款（不区分骑手）。
func waiveDispute(o *orderState, p Params, _ int64) Decision {
	if o.pending != disputeOpen {
		return Decision{}
	}
	o.pending = disputeWaived
	return land(o, p, PartyMerchant, landFullWindowExpiry, o.stage >= StageAssigned,
		"merchant waived preparation claim: no fault, merchant bears full refund")
}

// expireDispute 落地“争议窗口到期且商家未声明”。
func expireDispute(o *orderState, p Params) Decision {
	if o.pending != disputeOpen {
		return Decision{}
	}
	o.pending = disputeNone
	return land(o, p, PartyMerchant, landFullWindowExpiry, o.stage >= StageAssigned,
		"dispute window expired without claim: no fault, merchant bears full refund")
}
