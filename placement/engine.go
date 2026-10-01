package placement

import "sort"

// sameDomain 报告两个节点在给定拓扑键下是否同域。
// 两个节点名均必须存在。
func (f *Filter) sameDomain(nodeA, nodeB string, key TopologyKey) bool {
	if key == TopologyNode {
		return nodeA == nodeB
	}
	return f.mu.nodes[nodeA] == f.mu.nodes[nodeB]
}

// sortedPlaced 返回按 ID 字节序升序排列的已放置 Pod，
// 保证冲突/排斥的最小阻挡者选取可复现。
func (f *Filter) sortedPlaced() []*placedPod {
	out := make([]*placedPod, 0, len(f.mu.pods))
	for _, p := range f.mu.pods {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pod.ID < out[j].pod.ID })
	return out
}

// minBlocker 返回所有以 candidate 为候选、节点 n 上的拒绝原因。
// 按规定顺序只返回第一个原因：
//  1. 亲和不满足（带第一条未满足项下标）
//  2. 新 Pod 自身反亲和冲突（带第一条冲突项下标与最小阻挡 Pod ID）
//  3. 被已有 Pod 对称排斥（带字节序最小排斥者 ID）
//
// candidate 的规格假定已通过 validatePod。
func (f *Filter) checkPlacement(candidate Pod, n string) *PlacementError {
	all := f.sortedPlaced()

	// (1) 必需亲和（含首个成员豁免，逐项独立判断）。
	for ti, term := range candidate.Affinity {
		var clusterMatches, sameDomainCount int
		for _, p := range all {
			if !matches(p.pod.Labels, term.Selector) {
				continue
			}
			clusterMatches++
			if f.sameDomain(p.node, n, term.Topology) {
				sameDomainCount++
			}
		}
		if sameDomainCount >= term.MinRequired {
			continue
		}
		// 全集群没有任何匹配的已放置 Pod，且候选自身匹配：首个成员豁免。
		if clusterMatches == 0 && matches(candidate.Labels, term.Selector) {
			continue
		}
		return &PlacementError{
			Reason:    ReasonAffinityNotSatisfied,
			Detail:    "affinity term not satisfied",
			TermIndex: ti,
		}
	}

	// (2) 候选自身的必需反亲和：不存在同域匹配者。
	for ti, term := range candidate.AntiAffinity {
		var blocker string
		for _, p := range all {
			if !matches(p.pod.Labels, term.Selector) {
				continue
			}
			if !f.sameDomain(p.node, n, term.Topology) {
				continue
			}
			blocker = p.pod.ID // all 已按 ID 升序，首个即最小
			break
		}
		if blocker != "" {
			return &PlacementError{
				Reason:    ReasonAntiAffinityConflict,
				Detail:    "anti-affinity conflict",
				TermIndex: ti,
				BlockerID: blocker,
			}
		}
	}

	// (3) 对称排斥：任一已放置 Pod q 的反亲和项匹配候选标签且同域。
	// 候选自身匹配自身选择器不产生自我排斥（已放置集合中没有 candidate）。
	var blocker string
	for _, p := range all {
		rejects := false
		for _, term := range p.pod.AntiAffinity {
			if matches(candidate.Labels, term.Selector) &&
				f.sameDomain(p.node, n, term.Topology) {
				rejects = true
				break
			}
		}
		if rejects {
			blocker = p.pod.ID // all 已按 ID 升序，首个即最小
			break
		}
	}
	if blocker != "" {
		return &PlacementError{
			Reason:    ReasonRejectedByExistingPod,
			Detail:    "rejected by existing pod's anti-affinity",
			TermIndex: -1,
			BlockerID: blocker,
		}
	}
	return nil
}

// checkRelabel 检查把目标 Pod 的标签替换为 newLabels 后，
// 是否存在另一已放置 Pod q 的某条反亲和项匹配新标签且同域。
// 只检查这一方向（其他 Pod 排斥目标）；亲和不重新校验。
// 返回字节序最小的排斥者 ID，没有则为空串。
func (f *Filter) checkRelabel(target *placedPod, newLabels map[string]string) string {
	for _, p := range f.sortedPlaced() {
		if p == target {
			continue
		}
		for _, term := range p.pod.AntiAffinity {
			if matches(newLabels, term.Selector) &&
				f.sameDomain(p.node, target.node, term.Topology) {
				return p.pod.ID
			}
		}
	}
	return ""
}
