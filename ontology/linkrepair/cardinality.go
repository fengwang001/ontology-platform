package linkrepair

import "sort"

// edgeKey 是链接记录的"内容键"：同一链接类型下指向同一对对象实例的
// 记录视为内容完全相同（重复）。记录自身标识不参与内容相等判定，
// 因为损坏的重复副本可能携带不同或丢失的实例标识。
type edgeKey struct {
	linkType string
	source   string
	target   string
}

// edgeGroup 收集同一个内容键下的全部候选记录（重复副本）。
type edgeGroup struct {
	key   edgeKey
	first int   // 副本中最小的输入下标，作为规范代表（稳定、与执行次数无关）
	dupes []int // 其余副本的输入下标，升序
}

// endGroup 是一个基数裁决局部范围：某链接类型、某一端上的
// 单个对象实例，以及从该实例出发的不同对方实例边。
type endGroup struct {
	linkType     string
	endName      string // "A" 或 "B"，仅用于报告
	anchor       string // 该端对象实例
	max          int    // 该端声明的数量上限；<=0 表示无上限
	counterparts []counterpartEdge
}

// counterpartEdge 是局部范围内指向某个对方实例的规范边。
type counterpartEdge struct {
	otherID string // 对方实例标识
	edgePos int    // 规范记录的输入下标
}

// droppedOutcome 描述一条通过阶段 1、2 却未被保留的候选的最终判定。
type droppedOutcome struct {
	reason Reason
	detail string
}

// arbiter 负责基数约束冲突裁决与重复记录去重（阶段 3）。
type arbiter struct {
	types map[string]LinkType
}

// newArbiter 依据链接类型定义构建裁决器。
func newArbiter(types []LinkType) *arbiter {
	m := make(map[string]LinkType, len(types))
	for _, t := range types {
		m[t.ID] = t
	}
	return &arbiter{types: m}
}

// arbitrate 对通过阶段 1、2 的候选记录进行去重与基数裁决。
//
// candidates 可为阶段 1/2 之后的稀疏列表，元素自带原始输入下标，
// 返回的 kept/dropped 均以该原始下标为键。
//
// 裁决规则固定，不依赖 map 遍历顺序，也不依赖候选的输入顺序：
//
//  1. 去重：按内容键分组，输入下标最小者为规范代表；其余副本为重复。
//  2. 基数：按 (链接类型, 端, 锚点对象) 划分互不相交的局部范围，
//     不同链接类型、不同端、不同对象的裁决完全独立。
//  3. 在每个局部范围内，将不同对方实例按标识字典序升序排列，
//     这是唯一且固定不变的优先规则；max>0 时保留前 max 个，
//     其余（含其全部副本）判定为基数约束冲突。
//  4. 一条规范边只有在所属两个端局部范围中同时胜出才最终保留；
//     任一端落败即舍弃，原因仍为基数冲突。
//  5. 重复副本的归类取决于其规范边：规范边保留则副本为重复；
//     规范边在基数裁决中落败，则该内容整体因基数冲突被舍弃，
//     不存在"重复一条本不该存在的记录"这一更高优先级归类
//     （判定优先级为结构 -> 引用 -> 基数 -> 重复，见设计说明）。
func (a *arbiter) arbitrate(candidates []candidateRecord) (kept map[int]struct{}, dropped map[int]droppedOutcome) {
	kept = make(map[int]struct{})
	dropped = make(map[int]droppedOutcome)

	groups, order := groupByContent(candidates)

	// 登记两个端的局部范围，随后在每个范围内独立裁决。
	sourceEnds := make(map[string]map[string]*endGroup)
	targetEnds := make(map[string]map[string]*endGroup)
	sourceWin := make(map[int]bool)
	targetWin := make(map[int]bool)
	for _, key := range order {
		g := groups[key]
		t := a.types[key.linkType]
		sg := lookupEndGroup(sourceEnds, key.linkType, "A", key.source, t.MaxA)
		sg.counterparts = append(sg.counterparts, counterpartEdge{otherID: key.target, edgePos: g.first})
		tg := lookupEndGroup(targetEnds, key.linkType, "B", key.target, t.MaxB)
		tg.counterparts = append(tg.counterparts, counterpartEdge{otherID: key.source, edgePos: g.first})
	}
	for _, byAnchor := range sourceEnds {
		for _, eg := range byAnchor {
			a.decideEndGroup(eg, sourceWin)
		}
	}
	for _, byAnchor := range targetEnds {
		for _, eg := range byAnchor {
			a.decideEndGroup(eg, targetWin)
		}
	}

	// 收口：规范边两端同时胜出才保留。
	for _, key := range order {
		g := groups[key]
		if sourceWin[g.first] && targetWin[g.first] {
			kept[g.first] = struct{}{}
			for _, pos := range g.dupes {
				dropped[pos] = droppedOutcome{
					reason: ReasonDuplicate,
					detail: "duplicate of record at position " + itoa(g.first) +
						" (same link type and same object pair)",
				}
			}
			continue
		}

		detail := a.conflictDetail(groups, g, sourceWin, targetWin)
		outcome := droppedOutcome{reason: ReasonCardinalityConflict, detail: detail}
		dropped[g.first] = outcome
		for _, pos := range g.dupes {
			dropped[pos] = outcome
		}
	}
	return kept, dropped
}

// groupByContent 归并内容相同的记录，返回内容键 -> 分组的映射，
// 以及按内容键字典序排列的稳定遍历顺序。
func groupByContent(candidates []candidateRecord) (map[edgeKey]*edgeGroup, []edgeKey) {
	groups := make(map[edgeKey]*edgeGroup, len(candidates))
	for _, c := range candidates {
		key := edgeKeyOf(c.record)
		g, ok := groups[key]
		if !ok {
			groups[key] = &edgeGroup{key: key, first: c.position}
			continue
		}
		if c.position < g.first {
			g.dupes = append(g.dupes, g.first)
			g.first = c.position
		} else {
			g.dupes = append(g.dupes, c.position)
		}
	}
	order := make([]edgeKey, 0, len(groups))
	for key := range groups {
		order = append(order, key)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.linkType != b.linkType {
			return a.linkType < b.linkType
		}
		if a.source != b.source {
			return a.source < b.source
		}
		return a.target < b.target
	})
	for _, g := range groups {
		sort.Ints(g.dupes)
	}
	return groups, order
}

// decideEndGroup 在一个局部范围内执行固定优先规则裁决。
//
// 仅当范围真正超限（max>0 且候选数>max）时才进行字典序排序：
// 无上限或未超限的范围直接全部胜出，不产生任何排序开销，
// 因而总工作量中的排序部分只与真正发生冲突的局部范围相关。
// 超限时对方实例按标识字典序升序排列，前 max 个胜出。
// 结果写入 win（键为规范边的输入下标）。
func (a *arbiter) decideEndGroup(eg *endGroup, win map[int]bool) {
	if eg.max <= 0 || len(eg.counterparts) <= eg.max {
		for _, ce := range eg.counterparts {
			win[ce.edgePos] = true
		}
		return
	}
	sort.Slice(eg.counterparts, func(i, j int) bool {
		return eg.counterparts[i].otherID < eg.counterparts[j].otherID
	})
	for i, ce := range eg.counterparts {
		if i < eg.max {
			win[ce.edgePos] = true
		}
	}
}

// conflictDetail 为落败规范边生成确定性的、可复核的裁决依据说明。
// 优先报告第一个落败的端（A 端先于 B 端，顺序固定）。
func (a *arbiter) conflictDetail(groups map[edgeKey]*edgeGroup, g *edgeGroup, sourceWin, targetWin map[int]bool) string {
	t := a.types[g.key.linkType]
	if !sourceWin[g.first] && t.MaxA > 0 {
		return cardinalityDetail(g.key.linkType, "A", g.key.source, g.key.target, t.MaxA, groups, true)
	}
	if !targetWin[g.first] && t.MaxB > 0 {
		return cardinalityDetail(g.key.linkType, "B", g.key.target, g.key.source, t.MaxB, groups, false)
	}
	// 理论上不会到达：落败必然源于某个带正向上限的端超上限。
	return "cardinality conflict in link type " + g.key.linkType
}

// cardinalityDetail 重建局部范围的排序名次用于说明：
// 锚点 anchor 的对方实例按字典序排列，被舍弃边指向 other、名次为 rank。
func cardinalityDetail(linkType, endName, anchor, other string, max int, groups map[edgeKey]*edgeGroup, sourceSide bool) string {
	others := make([]string, 0)
	for key := range groups {
		if key.linkType != linkType {
			continue
		}
		if sourceSide && key.source == anchor {
			others = append(others, key.target)
		} else if !sourceSide && key.target == anchor {
			others = append(others, key.source)
		}
	}
	sort.Strings(others)
	rank := 0
	for i, id := range others {
		if id == other {
			rank = i + 1
			break
		}
	}
	return "cardinality conflict: end " + endName + " object " + anchor +
		" of link type " + linkType + " allows at most " + itoa(max) +
		" counterpart(s); counterpart " + other + " ranks " + itoa(rank) +
		" by fixed lexicographic priority and is discarded"
}

// lookupEndGroup 取得或创建一个端局部范围。
func lookupEndGroup(roots map[string]map[string]*endGroup, linkType, endName, anchor string, max int) *endGroup {
	byLink := roots[linkType]
	if byLink == nil {
		byLink = make(map[string]*endGroup)
		roots[linkType] = byLink
	}
	eg := byLink[anchor]
	if eg == nil {
		eg = &endGroup{linkType: linkType, endName: endName, anchor: anchor, max: max}
		byLink[anchor] = eg
	}
	return eg
}

// candidateRecord 是进入阶段 3 的记录：结构有效且两端引用均可用。
type candidateRecord struct {
	position int
	record   RawRecord
}

func edgeKeyOf(r RawRecord) edgeKey {
	return edgeKey{r.LinkTypeID, r.SourceID, r.TargetID}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [21]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
