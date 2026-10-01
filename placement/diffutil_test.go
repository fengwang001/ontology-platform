package placement

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

var diffLabelKeys = []string{"app", "tier", "role", "color"}
var diffLabelVals = []string{"db", "web", "cache", "x", "y"}

func diffRandLabels(r *rand.Rand, density float64) map[string]string {
	m := map[string]string{}
	for _, k := range diffLabelKeys {
		if r.Float64() < density {
			m[k] = diffLabelVals[r.Intn(len(diffLabelVals))]
		}
	}
	return m
}

func diffRandSelector(r *rand.Rand) Selector {
	if r.Intn(10) < 3 { // 30% 空选择器，匹配全部
		return Selector{}
	}
	s := Selector{}
	s[diffLabelKeys[r.Intn(len(diffLabelKeys))]] = diffLabelVals[r.Intn(len(diffLabelVals))]
	return s
}

func diffRandTopology(r *rand.Rand) TopologyKey {
	if r.Intn(2) == 0 {
		return TopologyNode
	}
	return TopologyZone
}

// diffRandPod 生成随机 Pod；allowInvalid 为真时可能注入非法字段。
func diffRandPod(r *rand.Rand, id string, allowInvalid bool) Pod {
	p := Pod{ID: id, Labels: diffRandLabels(r, 0.75)}
	if r.Intn(10) < 7 {
		for i := 0; i < r.Intn(2)+1; i++ {
			p.Affinity = append(p.Affinity, AffinityTerm{
				Selector:    diffRandSelector(r),
				Topology:    diffRandTopology(r),
				MinRequired: 1 + r.Intn(3),
			})
		}
	}
	if r.Intn(10) < 7 {
		for i := 0; i < r.Intn(2)+1; i++ {
			p.AntiAffinity = append(p.AntiAffinity, AntiAffinityTerm{
				Selector: diffRandSelector(r),
				Topology: diffRandTopology(r),
			})
		}
	}
	if allowInvalid {
		switch r.Intn(20) {
		case 0:
			p.Labels[""] = "badkey"
		case 1:
			p.ID = ""
		case 2:
			if len(p.Affinity) > 0 {
				p.Affinity[0].MinRequired = 101
			}
		case 3:
			if len(p.AntiAffinity) > 0 {
				p.AntiAffinity[0].Topology = "rack"
			}
		}
	}
	return p
}

type diffOp struct {
	kind   string
	pod    Pod
	node   string
	zone   string
	id     string
	labels map[string]string
}

func diffPodString(x Pod) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{id=%q labels=%v aff=[", x.ID, x.Labels)
	for _, term := range x.Affinity {
		fmt.Fprintf(&b, "(sel=%v,topo=%s,m=%d)", map[string]string(term.Selector), term.Topology, term.MinRequired)
	}
	b.WriteString("] anti=[")
	for _, term := range x.AntiAffinity {
		fmt.Fprintf(&b, "(sel=%v,topo=%s)", map[string]string(term.Selector), term.Topology)
	}
	b.WriteString("])")
	return b.String()
}

func prodToNaive(err error) naiveResult {
	if err == nil {
		return naiveResult{}
	}
	pe := err.(*PlacementError)
	return naiveResult{reason: pe.Reason, termIndex: pe.TermIndex, blocker: pe.BlockerID}
}

func diffSameResult(a, b naiveResult) bool {
	if a.reason != b.reason {
		return false
	}
	if (a.reason == ReasonAffinityNotSatisfied || a.reason == ReasonAntiAffinityConflict) &&
		a.termIndex != b.termIndex {
		return false
	}
	if (a.reason == ReasonAntiAffinityConflict || a.reason == ReasonRejectedByExistingPod) &&
		a.blocker != b.blocker {
		return false
	}
	return true
}

func diffResultString(rr naiveResult) string {
	if rr.reason == "" {
		return "OK"
	}
	s := string(rr.reason)
	if rr.termIndex >= 0 {
		s += fmt.Sprintf("[term=%d]", rr.termIndex)
	}
	if rr.blocker != "" {
		s += "[blocker=" + rr.blocker + "]"
	}
	return s
}

func uniqueSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func removeString(in []string, v string) []string {
	out := in[:0]
	for _, s := range in {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func runDiffOp(f *Filter, ns *naiveState, d *diffOp) (naiveResult, naiveResult) {
	switch d.kind {
	case "AddNode":
		return prodToNaive(f.AddNode(Node{Name: d.node, Zone: d.zone})), ns.addNode(d.node, d.zone)
	case "RemoveNode":
		return prodToNaive(f.RemoveNode(d.node)), ns.removeNode(d.node)
	case "Place":
		return prodToNaive(f.Place(d.pod, d.node)), ns.place(d.pod, d.node, false)
	case "Reserve":
		return prodToNaive(f.Reserve(d.pod, d.node)), ns.place(d.pod, d.node, true)
	case "Commit":
		return prodToNaive(f.Commit(d.id)), ns.commit(d.id)
	case "Cancel":
		return prodToNaive(f.Cancel(d.id)), ns.cancel(d.id)
	case "Remove":
		return prodToNaive(f.Remove(d.id)), ns.remove(d.id)
	case "Relabel":
		return prodToNaive(f.Relabel(d.id, d.labels)), ns.relabel(d.id, d.labels)
	default:
		panic("unknown op " + d.kind)
	}
}
