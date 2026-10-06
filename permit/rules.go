package permit

import "sort"

// 本文件只做"给定候选许可，当前活跃集合是否违反规则"的纯判定。
// 调用方负责：时钟/参数/存在性等前置校验、索引中只含已批活跃许可、
// 以及应急受理时先把将被抢占的常规许可从索引中摘除。

// checkResult 汇总针对某个候选常规许可的三类规则审查；Code 为最靠前的一类。
type checkResult struct {
	code        RuleCode // -1 语义用 err == nil 表达
	err         *RuleError
	sameWitness []string
	detWitness  []string
	corrWitness string
}

// examiner 持有全部活跃索引，负责同路段/绕行/走廊三类规则判定。
type examiner struct {
	net       *networkView
	segIndex  map[string]*activeIndex
	corrIndex map[string]*activeIndex
	permits   map[string]*Permit

	// liveAfter 为判定的在册下界：只统计 end > liveAfter 的许可。
	// 新申请取 min(操作时刻,候选开始)；顺延重审取应急受理时刻；延期取操作时刻。
	liveAfter Time
}

// live 过滤在在册下界之前（含）已结束的节点。
func (e *examiner) live(ns []*indexNode) []*indexNode {
	out := make([]*indexNode, 0, len(ns))
	for _, n := range ns {
		if n.end > e.liveAfter {
			out = append(out, n)
		}
	}
	return out
}

// checkRegular 对常规候选做三类规则审查，严格按 同路段 > 绕行 > 走廊 的次序只报一类。
// excludeID 用于延期时把本许可原时段排除在审查之外。
func (e *examiner) checkRegular(cand *Permit, excludeID string) *RuleError {
	iv := cand.Current
	seg0 := e.net.segments[cand.Segment]
	if cand.Lanes > seg0.Lanes {
		return ruleErr(ErrLanesExceed,
			"requested lanes "+itoa(cand.Lanes)+" > segment lanes "+itoa(seg0.Lanes))
	}

	// 规则一：同路段车道数之和（恰好等于允许；全封闭天然与任何相交封闭冲突）。
	same := e.live(e.segIndex[cand.Segment].overlaps(iv))
	sumLanes := cand.Lanes
	sameWitness := make([]string, 0, 4)
	for _, n := range same {
		if n.id == excludeID {
			continue
		}
		// 常规候选的同路段约束只在常规许可之间成立；相交的应急许可
		// 不阻挡常规申请——应急到来时由其抢占机制移开常规许可。
		if p := e.permits[n.id]; p != nil && p.Priority == Emergency {
			continue
		}
		sumLanes += n.lanes
		if len(sameWitness) < 8 {
			sameWitness = append(sameWitness, n.id)
		}
	}
	seg := seg0
	if sumLanes > seg.Lanes {
		return ruleErr(ErrSameSegment,
			"closed lanes "+itoa(sumLanes)+" exceed segment lanes "+itoa(seg.Lanes),
			sameWitness...)
	}

	// 规则二（方向 A）：本路段全封闭时，指定绕行路线上的任何路段不得有任何封闭许可。
	detWitness := map[string]struct{}{}
	if cand.Lanes == seg.Lanes {
		for _, d := range e.net.detourOf[cand.Segment] {
			for _, n := range e.live(e.segIndex[d].overlaps(iv)) {
				if n.id == excludeID {
					continue
				}
				// 绕行/走廊约束只在常规许可之间成立（见全局不变式），应急豁免这两类规则。
				if p := e.permits[n.id]; p != nil && p.Priority == Emergency {
					continue
				}
				detWitness[n.id] = struct{}{}
			}
		}
	}
	// 规则二（方向 B）：本路段有任何封闭时，以本路段为绕行路线一部分的其他路段不得全封闭。
	for up := range e.net.incomingDetour[cand.Segment] {
		upSeg := e.net.segments[up]
		// 方向 B 是对物理占用的对称保护：无论上游全封闭许可是常规还是应急，
		// 本路段一旦封闭都会让上游的指定绕行线失效。应急豁免的是"应急申请
		// 自身"的审查，而非应急存在后对后续常规申请的阻挡。
		for _, n := range e.live(e.segIndex[up].overlaps(iv)) {
			if n.id == excludeID {
				continue
			}
			p := e.permits[n.id]
			if p == nil {
				continue
			}
			if p.Lanes == upSeg.Lanes {
				detWitness[n.id] = struct{}{}
			}
		}
	}
	if len(detWitness) > 0 {
		return ruleErr(ErrDetour, "detour route would be blocked", keysOf(detWitness)...)
	}

	// 规则三：走廊同一时刻并发封闭许可数不得超过上限（恰等于允许）。
	if cap := e.net.corridorCap[seg.Corridor]; cap > 0 {
		over := e.live(e.corrIndex[seg.Corridor].overlaps(iv))
		// 仅统计非排除的已批常规许可；求候选时段内任一时刻的最大并发。
		maxConcurrent := 0
		events := make([]sweepEvent, 0, 2*len(over)+2)
		events = append(events, sweepEvent{iv.Start, 1}, sweepEvent{iv.End, -1})
		for _, n := range over {
			if n.id == excludeID {
				continue
			}
			events = append(events, sweepEvent{n.start, 1}, sweepEvent{n.end, -1})
		}
		sortEvents(events)
		cur := 0
		for _, x := range events {
			cur += x.d
			if cur > maxConcurrent {
				maxConcurrent = cur
			}
		}
		if maxConcurrent > cap {
			return ruleErr(ErrCorridorCap,
				"corridor "+seg.Corridor+" concurrent "+itoa(maxConcurrent)+" > cap "+itoa(cap),
				seg.Corridor)
		}
	}
	return nil
}

// checkEmergencySameSegment 为应急受理的唯一冲突校验：
// 只与其他应急许可一起受同路段车道数约束（常规许可由抢占机制移开）。
// 调用方须保证将被抢占的常规许可已从路段索引摘除。
func (e *examiner) checkEmergencySameSegment(cand *Permit, excludeID string) *RuleError {
	iv := cand.Current
	seg0 := e.net.segments[cand.Segment]
	if cand.Lanes > seg0.Lanes {
		return ruleErr(ErrLanesExceed,
			"requested lanes "+itoa(cand.Lanes)+" > segment lanes "+itoa(seg0.Lanes))
	}
	sum := cand.Lanes
	witness := make([]string, 0, 4)
	for _, n := range e.live(e.segIndex[cand.Segment].overlaps(iv)) {
		if n.id == excludeID {
			continue
		}
		p := e.permits[n.id]
		if p == nil || p.Priority != Emergency {
			continue // 常规许可此刻应已摘除；防御性跳过
		}
		sum += n.lanes
		if len(witness) < 8 {
			witness = append(witness, n.id)
		}
	}
	seg := seg0
	if sum > seg.Lanes {
		return ruleErr(ErrSameSegment,
			"emergency closed lanes "+itoa(sum)+" exceed segment lanes "+itoa(seg.Lanes),
			witness...)
	}
	return nil
}

// preemptTargets 返回应急候选在同路段需要抢占的常规许可：
// 逐份、按批准次序从晚到早摘除相交常规许可，直到剩余封闭车道数放得下应急许可。
// 规则上全封闭应急会抢占所有相交常规许可；部分封闭只抢占使车道和超限者。
func (e *examiner) preemptTargets(cand *Permit) []*Permit {
	seg := e.net.segments[cand.Segment]
	nodes := e.live(e.segIndex[cand.Segment].overlaps(cand.Current))
	overlapping := make([]*Permit, 0, len(nodes))
	for _, n := range nodes {
		p := e.permits[n.id]
		if p != nil && p.Status == StatusApproved && p.Priority == Regular {
			overlapping = append(overlapping, p)
		}
	}
	// 按批准次序从晚到早（ApproveOrder 降序）确定抢占者，保证确定性。
	sortPermitsDescOrder(overlapping)

	used := cand.Lanes
	targets := make([]*Permit, 0)
	for _, p := range overlapping {
		if used+p.Lanes > seg.Lanes {
			targets = append(targets, p)
		} else {
			used += p.Lanes
		}
	}
	// 抢占处理按原批准次序先后进行（升序），先处理者的结果影响后处理者。
	sortPermitsAscOrder(targets)
	return targets
}

type sweepEvent struct {
	t Time
	d int
}

// sortEvents 按时刻排序；同一时刻先处理结束(-1)再处理开始(+1)，
// 因为时段左闭右开，首尾相接不构成并发。
func sortEvents(events []sweepEvent) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		return events[i].d < events[j].d
	})
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortPermitsAscOrder(ps []*Permit) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].ApproveOrder < ps[j].ApproveOrder })
}

func sortPermitsDescOrder(ps []*Permit) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].ApproveOrder > ps[j].ApproveOrder })
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	if neg {
		x = -x
	}
	var b [24]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
