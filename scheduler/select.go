package scheduler

import (
	"maps"
	"slices"
)

// nodeMatches 判断节点标签是否全部满足组的必需标签。
func nodeMatches(n *nodeState, required map[string]string) bool {
	for key, want := range required {
		if n.labels[key] != want {
			return false
		}
	}
	return true
}

// zoneInfo 汇总一个合格区在一次选点中的判定信息。
type zoneInfo struct {
	zone      string
	count     int
	eligible  bool
	freeNode  *nodeState
	freeSlots int
}

// evalZones 计算组的所有合格区及其计数、可用节点。
//
// 合格区指至少有一个满足标签要求的节点的区（不论有无空槽）。
// 区计数为该组在区内已绑定与已预留（含绑定中）副本数之和；
// 已释放的预留不再占用计数与槽位。
func (impl *schedulerImpl) evalZones(group *groupState) map[string]*zoneInfo {
	zones := make(map[string]*zoneInfo)
	for _, nodeID := range slices.Sorted(maps.Keys(impl.core.nodes)) {
		n := impl.core.nodes[nodeID]
		if !nodeMatches(n, group.requiredLabels) {
			continue
		}
		z := zones[n.zone]
		if z == nil {
			z = &zoneInfo{zone: n.zone, eligible: true, freeSlots: -1}
			zones[n.zone] = z
		}
		free := n.slots - n.used
		if free > 0 && (z.freeNode == nil || free > z.freeSlots ||
			(free == z.freeSlots && n.id < z.freeNode.id)) {
			z.freeSlots = free
			z.freeNode = n
		}
	}
	for _, rep := range impl.core.replicas[group.id] {
		if rep.state == stateReleased {
			continue
		}
		z := zones[rep.zone]
		if z == nil {
			// 理论上不会发生：已存在副本的区必然有匹配节点。
			continue
		}
		z.count++
	}
	return zones
}

// chooseZone 在合格区中按规则选点：
//  1. 只考虑计数最小且放置后不超过允许偏斜的区；
//  2. 区内必须有满足标签且有空槽的节点；
//  3. 区并列时按区标识升序，保证确定性。
//
// 返回 (区信息, 原因)；无空槽节点时原因为 no_node_available，
// 偏斜不允许时原因为 skew_violated。
func chooseZone(zones map[string]*zoneInfo, skew int) (*zoneInfo, Reason) {
	names := slices.Sorted(maps.Keys(zones))
	minCount := -1
	for _, name := range names {
		if c := zones[name].count; minCount < 0 || c < minCount {
			minCount = c
		}
	}
	anyFree := false
	allowed := make([]*zoneInfo, 0)
	for _, name := range names {
		z := zones[name]
		if z.freeNode != nil {
			anyFree = true
		}
		if z.count != minCount {
			continue
		}
		if z.freeNode == nil {
			continue
		}
		// 放置后该组在所有合格区上的计数差：min 区加一，其余不动。
		if z.count+1-minCount <= skew {
			allowed = append(allowed, z)
		}
	}
	if len(allowed) > 0 {
		return allowed[0], ""
	}
	if !anyFree {
		return nil, ReasonNoNodeAvailable
	}
	return nil, ReasonSkewViolated
}
