package placement

import (
	"fmt"
	"sort"
	"strings"
)

// naiveModel is an independent, deliberately simple reimplementation of the
// specification: maps with linear scans and full recomputation per decision.
type naiveModel struct {
	q           int
	nodes       map[string]string
	pods        map[string]*naivePod
	reservedIDs int
}

type naivePod struct {
	pod      *Pod
	node     string
	reserved bool
}

func newNaive(q int) *naiveModel {
	return &naiveModel{q: q, nodes: map[string]string{}, pods: map[string]*naivePod{}}
}

func naiveSelectorValid(sel Selector) bool {
	if sel == nil {
		return false
	}
	for k := range sel {
		if k == "" {
			return false
		}
	}
	return true
}

func naivePodValid(p *Pod) bool {
	if p == nil || p.ID == "" {
		return false
	}
	for k := range p.Labels {
		if k == "" {
			return false
		}
	}
	for _, a := range p.Affinity {
		if !naiveSelectorValid(a.Selector) || (a.Topology != "node" && a.Topology != "zone") ||
			a.MinMatching < 1 || a.MinMatching > 100 {
			return false
		}
	}
	for _, a := range p.AntiAffinity {
		if !naiveSelectorValid(a.Selector) || (a.Topology != "node" && a.Topology != "zone") {
			return false
		}
	}
	return true
}

func naiveMatches(sel Selector, labels map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (m *naiveModel) sameDomain(topo, nodeA, nodeB string) bool {
	if topo == "node" {
		return nodeA == nodeB
	}
	return m.nodes[nodeA] == m.nodes[nodeB]
}

// check returns nil when p is placeable on node, otherwise the first failing
// reason in the required reporting order.
func (m *naiveModel) check(p *Pod, node string) *Reject {
	for i, term := range p.Affinity {
		clusterMatch := 0
		domainMatch := 0
		for _, q := range m.pods {
			if naiveMatches(term.Selector, q.pod.Labels) {
				clusterMatch++
				if m.sameDomain(term.Topology, q.node, node) {
					domainMatch++
				}
			}
		}
		ok := domainMatch >= term.MinMatching
		if !ok && clusterMatch == 0 && naiveMatches(term.Selector, p.Labels) {
			ok = true
		}
		if !ok {
			return &Reject{Code: ReasonAffinityUnsatisfied, Index: i}
		}
	}
	for i, term := range p.AntiAffinity {
		ids := make([]string, 0)
		for id, q := range m.pods {
			if m.sameDomain(term.Topology, q.node, node) && naiveMatches(term.Selector, q.pod.Labels) {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			return &Reject{Code: ReasonAntiAffinityConflict, Index: i, BlockingPod: ids[0]}
		}
	}
	blocker := ""
	for qid, q := range m.pods {
		for _, term := range q.pod.AntiAffinity {
			if m.sameDomain(term.Topology, q.node, node) && naiveMatches(term.Selector, p.Labels) {
				if blocker == "" || qid < blocker {
					blocker = qid
				}
				break
			}
		}
	}
	if blocker != "" {
		return &Reject{Code: ReasonRepelled, BlockingPod: blocker}
	}
	return nil
}

func (m *naiveModel) addPod(p *Pod, node string, reserve bool) *Reject {
	if _, ok := m.pods[p.ID]; ok {
		return &Reject{Code: ReasonPodExists, BlockingPod: p.ID}
	}
	if _, ok := m.nodes[node]; !ok {
		return &Reject{Code: ReasonNodeNotFound, BlockingPod: node}
	}
	if rj := m.check(p, node); rj != nil {
		return rj
	}
	if reserve && m.reservedIDs >= m.q {
		return reject(ReasonReservationsFull)
	}
	copied := &Pod{
		ID:           p.ID,
		Labels:       map[string]string{},
		Affinity:     append([]AffinityTerm(nil), p.Affinity...),
		AntiAffinity: append([]AntiAffinityTerm(nil), p.AntiAffinity...),
	}
	for k, v := range p.Labels {
		copied.Labels[k] = v
	}
	for i := range copied.Affinity {
		copied.Affinity[i].Selector = Selector(map[string]string(p.Affinity[i].Selector))
	}
	for i := range copied.AntiAffinity {
		copied.AntiAffinity[i].Selector = Selector(map[string]string(p.AntiAffinity[i].Selector))
	}
	m.pods[p.ID] = &naivePod{pod: copied, node: node, reserved: reserve}
	if reserve {
		m.reservedIDs++
	}
	return nil
}

type op struct {
	name   string
	id     string
	node   string
	zone   string
	pod    *Pod
	labels map[string]string
}

func (m *naiveModel) apply(operation op) (summary string) {
	switch operation.name {
	case "AddNode":
		if operation.node == "" || operation.zone == "" {
			return reasonString(reject(ReasonInvalidArgs))
		}
		if _, ok := m.nodes[operation.node]; ok {
			return reasonString(&Reject{Code: ReasonNodeExists, BlockingPod: operation.node})
		}
		m.nodes[operation.node] = operation.zone
	case "RemoveNode":
		if _, ok := m.nodes[operation.node]; !ok {
			return reasonString(&Reject{Code: ReasonNodeNotFound, BlockingPod: operation.node})
		}
		for _, q := range m.pods {
			if q.node == operation.node {
				return reasonString(&Reject{Code: ReasonNodeNotEmpty, BlockingPod: operation.node})
			}
		}
		delete(m.nodes, operation.node)
	case "Place", "Reserve":
		if !naivePodValid(operation.pod) {
			return reasonString(reject(ReasonInvalidArgs))
		}
		return reasonString(m.addPod(operation.pod, operation.node, operation.name == "Reserve"))
	case "Feasible":
		if !naivePodValid(operation.pod) {
			return reasonString(reject(ReasonInvalidArgs))
		}
		if _, ok := m.pods[operation.pod.ID]; ok {
			return reasonString(&Reject{Code: ReasonPodExists, BlockingPod: operation.pod.ID})
		}
		names := make([]string, 0)
		for name := range m.nodes {
			if m.check(operation.pod, name) == nil {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return "ok feasible=[" + strings.Join(names, ",") + "]"
	case "Commit":
		q, ok := m.pods[operation.id]
		if !ok {
			return reasonString(&Reject{Code: ReasonPodNotFound, BlockingPod: operation.id})
		}
		if !q.reserved {
			return reasonString(&Reject{Code: ReasonNotReserved, BlockingPod: operation.id})
		}
		q.reserved = false
		m.reservedIDs--
	case "Cancel":
		q, ok := m.pods[operation.id]
		if !ok {
			return reasonString(&Reject{Code: ReasonPodNotFound, BlockingPod: operation.id})
		}
		if !q.reserved {
			return reasonString(&Reject{Code: ReasonNotReserved, BlockingPod: operation.id})
		}
		delete(m.pods, operation.id)
		m.reservedIDs--
	case "Remove":
		q, ok := m.pods[operation.id]
		if !ok {
			return reasonString(&Reject{Code: ReasonPodNotFound, BlockingPod: operation.id})
		}
		if q.reserved {
			return reasonString(&Reject{Code: ReasonStillReserved, BlockingPod: operation.id})
		}
		delete(m.pods, operation.id)
	case "Relabel":
		for k := range operation.labels {
			if k == "" {
				return reasonString(reject(ReasonInvalidArgs))
			}
		}
		q, ok := m.pods[operation.id]
		if !ok {
			return reasonString(&Reject{Code: ReasonPodNotFound, BlockingPod: operation.id})
		}
		blocker := ""
		for qid, r := range m.pods {
			if qid == operation.id {
				continue
			}
			for _, term := range r.pod.AntiAffinity {
				if m.sameDomain(term.Topology, r.node, q.node) && naiveMatches(term.Selector, operation.labels) {
					if blocker == "" || qid < blocker {
						blocker = qid
					}
					break
				}
			}
		}
		if blocker != "" {
			return reasonString(&Reject{Code: ReasonRepelled, BlockingPod: blocker})
		}
		labels := make(map[string]string, len(operation.labels))
		for k, v := range operation.labels {
			labels[k] = v
		}
		q.pod.Labels = labels
	}
	return "ok"
}

func reasonString(err *Reject) string {
	if err == nil {
		return "ok"
	}
	switch err.Code {
	case ReasonAffinityUnsatisfied:
		return fmt.Sprintf("REJECT affinity-unsatisfied term#%d", err.Index)
	case ReasonAntiAffinityConflict:
		return fmt.Sprintf("REJECT anti-affinity-conflict term#%d blocker=%s", err.Index, err.BlockingPod)
	case ReasonRepelled:
		return "REJECT repelled blocker=" + err.BlockingPod
	default:
		return "REJECT " + err.Error()
	}
}
