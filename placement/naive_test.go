package placement

import (
	"sort"
)

// naiveState 是按题目规则逐步直接写成的朴素参考模型，
// 刻意不复用 production 代码的任何判定函数。
type naiveState struct {
	Q        int
	nodes    map[string]string
	pods     map[string]naivePod
	reserved map[string]bool
}

type naivePod struct {
	labels       map[string]string
	affinity     []AffinityTerm
	antiAffinity []AntiAffinityTerm
	node         string
	reserved     bool
}

func newNaive(Q int) *naiveState {
	return &naiveState{
		Q:        Q,
		nodes:    map[string]string{},
		pods:     map[string]naivePod{},
		reserved: map[string]bool{},
	}
}

func nMatches(labels map[string]string, s Selector) bool {
	for k, v := range s {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (ns *naiveState) validPod(x Pod) bool {
	if x.ID == "" {
		return false
	}
	for k := range x.Labels {
		if k == "" {
			return false
		}
	}
	for _, term := range x.Affinity {
		for k := range term.Selector {
			if k == "" {
				return false
			}
		}
		if term.Topology != TopologyNode && term.Topology != TopologyZone {
			return false
		}
		if term.MinRequired < 1 || term.MinRequired > 100 {
			return false
		}
	}
	for _, term := range x.AntiAffinity {
		for k := range term.Selector {
			if k == "" {
				return false
			}
		}
		if term.Topology != TopologyNode && term.Topology != TopologyZone {
			return false
		}
	}
	return true
}

func (ns *naiveState) domain(node string, key TopologyKey) string {
	if key == TopologyNode {
		return "n:" + node
	}
	return "z:" + ns.nodes[node]
}

type naiveResult struct {
	reason    Reason
	termIndex int
	blocker   string
	feasible  []string
}

// check 按 (1) 亲和+豁免、(2) 自身反亲和、(3) 对称排斥 的顺序逐节点判定。
func (ns *naiveState) check(x Pod, n string) naiveResult {
	ids := make([]string, 0, len(ns.pods))
	for id := range ns.pods {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for ti, term := range x.Affinity {
		cluster, same := 0, 0
		for _, id := range ids {
			q := ns.pods[id]
			if !nMatches(q.labels, term.Selector) {
				continue
			}
			cluster++
			if ns.domain(q.node, term.Topology) == ns.domain(n, term.Topology) {
				same++
			}
		}
		if same >= term.MinRequired {
			continue
		}
		if cluster == 0 && nMatches(x.Labels, term.Selector) {
			continue
		}
		return naiveResult{reason: ReasonAffinityNotSatisfied, termIndex: ti}
	}

	for ti, term := range x.AntiAffinity {
		for _, id := range ids {
			q := ns.pods[id]
			if nMatches(q.labels, term.Selector) &&
				ns.domain(q.node, term.Topology) == ns.domain(n, term.Topology) {
				return naiveResult{reason: ReasonAntiAffinityConflict, termIndex: ti, blocker: id}
			}
		}
	}

	for _, id := range ids {
		q := ns.pods[id]
		for _, term := range q.antiAffinity {
			if nMatches(x.Labels, term.Selector) &&
				ns.domain(q.node, term.Topology) == ns.domain(n, term.Topology) {
				return naiveResult{reason: ReasonRejectedByExistingPod, blocker: id}
			}
		}
	}
	return naiveResult{}
}

func (ns *naiveState) addNode(name, zone string) naiveResult {
	if name == "" || zone == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if _, ok := ns.nodes[name]; ok {
		return naiveResult{reason: ReasonNodeExists}
	}
	ns.nodes[name] = zone
	return naiveResult{}
}

func (ns *naiveState) removeNode(name string) naiveResult {
	if name == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if _, ok := ns.nodes[name]; !ok {
		return naiveResult{reason: ReasonNodeNotFound}
	}
	for _, p := range ns.pods {
		if p.node == name {
			return naiveResult{reason: ReasonNodeInUse}
		}
	}
	delete(ns.nodes, name)
	return naiveResult{}
}

func (ns *naiveState) place(x Pod, n string, reserve bool) naiveResult {
	if !ns.validPod(x) {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if _, ok := ns.pods[x.ID]; ok {
		return naiveResult{reason: ReasonPodExists}
	}
	if _, ok := ns.nodes[n]; !ok {
		return naiveResult{reason: ReasonNodeNotFound}
	}
	if r := ns.check(x, n); r.reason != "" {
		return r
	}
	if reserve && len(ns.reserved) >= ns.Q {
		return naiveResult{reason: ReasonReservationFull}
	}
	ns.pods[x.ID] = naivePod{
		labels:       cloneLabels(x.Labels),
		affinity:     append([]AffinityTerm(nil), x.Affinity...),
		antiAffinity: append([]AntiAffinityTerm(nil), x.AntiAffinity...),
		node:         n,
		reserved:     reserve,
	}
	if reserve {
		ns.reserved[x.ID] = true
	}
	return naiveResult{}
}

func (ns *naiveState) commit(id string) naiveResult {
	if id == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	p, ok := ns.pods[id]
	if !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	if !p.reserved {
		return naiveResult{reason: ReasonNotReserved}
	}
	p.reserved = false
	ns.pods[id] = p
	delete(ns.reserved, id)
	return naiveResult{}
}

func (ns *naiveState) cancel(id string) naiveResult {
	if id == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	p, ok := ns.pods[id]
	if !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	if !p.reserved {
		return naiveResult{reason: ReasonNotReserved}
	}
	delete(ns.pods, id)
	delete(ns.reserved, id)
	return naiveResult{}
}

func (ns *naiveState) remove(id string) naiveResult {
	if id == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	p, ok := ns.pods[id]
	if !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	if p.reserved {
		return naiveResult{reason: ReasonStillReserved}
	}
	delete(ns.pods, id)
	return naiveResult{}
}

func (ns *naiveState) relabel(id string, labels map[string]string) naiveResult {
	if id == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	for k := range labels {
		if k == "" {
			return naiveResult{reason: ReasonInvalidArgument}
		}
	}
	p, ok := ns.pods[id]
	if !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	ids := make([]string, 0, len(ns.pods))
	for qid := range ns.pods {
		if qid != id {
			ids = append(ids, qid)
		}
	}
	sort.Strings(ids)
	for _, qid := range ids {
		q := ns.pods[qid]
		for _, term := range q.antiAffinity {
			if nMatches(labels, term.Selector) &&
				ns.domain(q.node, term.Topology) == ns.domain(p.node, term.Topology) {
				return naiveResult{reason: ReasonRejectedByExistingPod, blocker: qid}
			}
		}
	}
	p.labels = cloneLabels(labels)
	ns.pods[id] = p
	return naiveResult{}
}

func (ns *naiveState) feasible(x Pod) naiveResult {
	if !ns.validPod(x) {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if _, ok := ns.pods[x.ID]; ok {
		return naiveResult{reason: ReasonPodExists}
	}
	out := []string{}
	for n := range ns.nodes {
		if ns.check(x, n).reason == "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return naiveResult{feasible: out}
}
