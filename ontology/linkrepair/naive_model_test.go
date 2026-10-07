package linkrepair

import (
	"sort"
)

// naiveVerdict 是朴素参照模型对单条记录的判定。
// 该模型刻意用与生产实现不同的、"按规则逐步独立判定"的写法：
// 先逐条打阶段标签，再以集合运算重建保留集合，便于交叉对照。
type naiveVerdict struct {
	position int
	reason   Reason
}

// naiveRepair 是独立于 [Repair] 的参照实现：
//
//  1. 逐条：结构无效 -> malformed；
//  2. 逐条：引用不可用 -> referenced_object_unavailable；
//  3. 剩余记录按内容键归组：非首现副本先打上 duplicate 候选标签；
//  4. 仅用规范副本，按 (链接类型,端,锚点) 重建局部范围，
//     对方标识字典序排序后取前 max 个得出胜出集合；
//  5. 内容键规范副本若任一端未胜出，该内容键全部记录改判 cardinality_conflict
//     （基数优先于重复的固定次序），否则首现保留、其余 duplicate。
func naiveRepair(snap Snapshot) []naiveVerdict {
	out := make([]naiveVerdict, len(snap.Records))
	for i := range out {
		out[i] = naiveVerdict{position: i}
	}

	known := map[string]LinkType{}
	for _, t := range snap.LinkTypes {
		known[t.ID] = t
	}

	type alive struct {
		pos int
		rec RawRecord
	}
	live := map[int]alive{}
	for i, r := range snap.Records {
		if r.LinkTypeID == "" {
			out[i].reason = ReasonMalformed
			continue
		}
		if _, ok := known[r.LinkTypeID]; !ok {
			out[i].reason = ReasonMalformed
			continue
		}
		if r.SourceID == "" || r.TargetID == "" {
			out[i].reason = ReasonMalformed
			continue
		}
		if _, ok := snap.AvailableObjects[r.SourceID]; !ok {
			out[i].reason = ReasonReferencedUnavailable
			continue
		}
		if _, ok := snap.AvailableObjects[r.TargetID]; !ok {
			out[i].reason = ReasonReferencedUnavailable
			continue
		}
		live[i] = alive{i, r}
	}

	// 内容键 -> 成员位置（保持首次出现顺序）。
	groups := map[edgeKey][]int{}
	var keys []edgeKey
	for i := 0; i < len(snap.Records); i++ {
		a, ok := live[i]
		if !ok {
			continue
		}
		k := edgeKeyOf(a.rec)
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], i)
	}

	// 规范副本 = 最小下标；构造两端局部范围。
	sourceWinner := map[edgeKey]bool{}
	targetWinner := map[edgeKey]bool{}
	sourceScope := map[string]map[string][]edgeKey{} // linkType -> anchor -> keys
	targetScope := map[string]map[string][]edgeKey{}
	for _, k := range keys {
		sourceWinner[k] = true
		targetWinner[k] = true
		addScope := func(root map[string]map[string][]edgeKey, lt, anchor string, k edgeKey) {
			if root[lt] == nil {
				root[lt] = map[string][]edgeKey{}
			}
			root[lt][anchor] = append(root[lt][anchor], k)
		}
		addScope(sourceScope, k.linkType, k.source, k)
		addScope(targetScope, k.linkType, k.target, k)
	}
	decide := func(root map[string]map[string][]edgeKey, maxOf func(LinkType) int, win map[edgeKey]bool, otherOf func(edgeKey) string) {
		for lt, byAnchor := range root {
			for anchor, ks := range byAnchor {
				_ = anchor
				max := maxOf(known[lt])
				sorted := append([]edgeKey(nil), ks...)
				sort.Slice(sorted, func(i, j int) bool {
					return otherOf(sorted[i]) < otherOf(sorted[j])
				})
				for rank, k := range sorted {
					if max > 0 && rank >= max {
						win[k] = false
					}
				}
			}
		}
	}
	decide(sourceScope, func(t LinkType) int { return t.MaxA }, sourceWinner, func(k edgeKey) string { return k.target })
	decide(targetScope, func(t LinkType) int { return t.MaxB }, targetWinner, func(k edgeKey) string { return k.source })

	for _, k := range keys {
		members := groups[k]
		first := members[0]
		if !sourceWinner[k] || !targetWinner[k] {
			for _, pos := range members {
				out[pos].reason = ReasonCardinalityConflict
			}
			continue
		}
		out[first].reason = ReasonNone
		for _, pos := range members[1:] {
			out[pos].reason = ReasonDuplicate
		}
	}
	return out
}
