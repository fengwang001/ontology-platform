package baggage

// BreakReason 提取断点原因。
type BreakReason string

const (
	BreakCustoms BreakReason = "customs"
	BreakPNR     BreakReason = "pnr_change"
	BreakLayover BreakReason = "layover_out_of_range"
)

type breakpoint struct {
	at     int // 断点位于第 at 个航段之后（中转站为 segs[at].To）
	reason BreakReason
}

type consignment struct {
	segments []Segment
	pnr      string
}

// findBreakpoints 扫描航段之间的中转站，任一条件不成立即为提取点：
// 非清关、前后航段同一订座记录、停留时长落在 [MinConnect, MaxConnect] 闭区间。
// 复杂度 O(段数)，机场信息 O(1) 直查。
func findBreakpoints(segs []Segment, cfg Config, reg *Registry) []breakpoint {
	var bps []breakpoint
	for i := 0; i+1 < len(segs); i++ {
		ap, _ := reg.airport(segs[i].To)
		lay := int64(segs[i+1].DepartsAt - segs[i].ArrivesAt)
		switch {
		case ap.Customs:
			bps = append(bps, breakpoint{at: i, reason: BreakCustoms})
		case segs[i].PNR != segs[i+1].PNR:
			bps = append(bps, breakpoint{at: i, reason: BreakPNR})
		case lay < cfg.MinConnect || lay > cfg.MaxConnect:
			bps = append(bps, breakpoint{at: i, reason: BreakLayover})
		}
	}
	return bps
}

// splitConsignments 按断点把航段切成若干段独立托运。
// 每段托运有且仅有一个订座记录（断点保证段内 PNR 一致）。
func splitConsignments(segs []Segment, bps []breakpoint) []consignment {
	out := make([]consignment, 0, len(bps)+1)
	start := 0
	for _, bp := range bps {
		out = append(out, consignment{segments: segs[start : bp.at+1], pnr: segs[start].PNR})
		start = bp.at + 1
	}
	out = append(out, consignment{segments: segs[start:], pnr: segs[start].PNR})
	return out
}

// buildClaimPoints 计算每个订座记录旅客的提取点（含各段托运起点与终点）。
// 清关/停留断点对所有经过的人生效；订座变更断点只涉及该两个记录，
// 天然由“属于自己 PNR 的段托运端点”表达，无需特判。
func buildClaimPoints(segs []Segment, bps []breakpoint) map[string][]string {
	legs := splitConsignments(segs, bps)
	out := make(map[string][]string)
	for _, leg := range legs {
		if len(out[leg.pnr]) == 0 {
			out[leg.pnr] = append(out[leg.pnr], leg.segments[0].From)
		}
		out[leg.pnr] = append(out[leg.pnr], leg.segments[len(leg.segments)-1].To)
	}
	return out
}
