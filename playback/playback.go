// Package playback 提供离线许可首播计时与过期判定的纯规则。
package playback

// Reason 标识过期原因。
type Reason int

const (
	ReasonTitleOffShelf Reason = iota
	ReasonPlaybackEnded
	ReasonRentalEnded
)

// State 标识许可所处阶段。
type State int

const (
	StateNotPlayed State = iota
	StatePlaying
	StateExpired
)

// Exp 返回许可在当前 titleEnd 下的过期时刻：
// 未开播时为 min(rentalEnd, titleEnd)；
// 开播后为 min(firstPlay + lp, titleEnd)，不再受 rentalEnd 约束。
func Exp(rentalEnd, firstPlay, lp, titleEnd int64) int64 {
	end := rentalEnd
	if firstPlay != 0 {
		end = firstPlay + lp
	}
	if titleEnd < end {
		end = titleEnd
	}
	return end
}

// Valid 报告 now 时刻许可是否有效。恰等即过期，仅 now < exp 有效。
func Valid(now, exp int64) bool { return now < exp }

// ExpiredReason 按固定次序返回第一个成立的过期原因：
// 已下架（now >= titleEnd）> 播放期满（已开播）> 租期满（未开播）。
// 调用方须已确认许可在 now 时刻过期。
func ExpiredReason(now, titleEnd, firstPlay int64) Reason {
	if now >= titleEnd {
		return ReasonTitleOffShelf
	}
	if firstPlay != 0 {
		return ReasonPlaybackEnded
	}
	return ReasonRentalEnded
}

// Info 为只读查询/播放判定的结构化结果。
type Info struct {
	State     State
	Exp       int64
	Reason    Reason // State == StateExpired 时有效
	FirstPlay int64  // 已开播时为首播时刻，否则为 0
}

// Evaluate 依据记录字段实时计算 now 时刻的阶段信息。
func Evaluate(now, rentalEnd, firstPlay, lp, titleEnd int64) Info {
	exp := Exp(rentalEnd, firstPlay, lp, titleEnd)
	if Valid(now, exp) {
		if firstPlay != 0 {
			return Info{State: StatePlaying, Exp: exp, FirstPlay: firstPlay}
		}
		return Info{State: StateNotPlayed, Exp: exp}
	}
	return Info{
		State:     StateExpired,
		Exp:       exp,
		Reason:    ExpiredReason(now, titleEnd, firstPlay),
		FirstPlay: firstPlay,
	}
}

func (r Reason) String() string {
	switch r {
	case ReasonTitleOffShelf:
		return "title-off-shelf"
	case ReasonPlaybackEnded:
		return "playback-ended"
	default:
		return "rental-ended"
	}
}
