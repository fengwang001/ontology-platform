package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// naiveSelector 是与生产实现独立、严格按规则逐条书写的朴素参照实现，
// 仅供随机对照测试使用。
type naiveSelector struct {
	selectors []naiveSel
	policy    FallbackPolicy
	def       map[string]string
	pt        int
	hosts     map[string]naiveHost
	counts    map[string]int64
	g, d      int64
}

type naiveHost struct {
	labels  map[string]string
	healthy bool
}

type naiveSel struct {
	keys map[string]bool
	fk   map[string]bool // nil 表示无 FK
}

func setEq(a map[string]bool, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, k := range b {
		if !a[k] {
			return false
		}
	}
	return true
}

func nSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nSubsetID(labels map[string]string, keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s;", k, labels[k])
	}
	return b.String()
}

func newNaive(selectors []SelectorSpec, policy FallbackPolicy, defaultLabels map[string]string, panicThreshold int) *naiveSelector {
	n := &naiveSelector{
		policy: policy,
		pt:     panicThreshold,
		hosts:  map[string]naiveHost{},
		counts: map[string]int64{},
	}
	for _, sp := range selectors {
		e := naiveSel{keys: map[string]bool{}}
		for _, k := range sp.Keys {
			e.keys[k] = true
		}
		if len(sp.FallbackKeys) > 0 {
			e.fk = map[string]bool{}
			for _, k := range sp.FallbackKeys {
				e.fk[k] = true
			}
		}
		n.selectors = append(n.selectors, e)
	}
	if defaultLabels != nil {
		n.def = map[string]string{}
		for k, v := range defaultLabels {
			n.def[k] = v
		}
	}
	return n
}

func (n *naiveSelector) addHost(id string, labels map[string]string, healthy bool) error {
	if id == "" {
		return ErrEmptyID
	}
	for k := range labels {
		if k == "" {
			return ErrEmptyLabelKey
		}
	}
	if _, ok := n.hosts[id]; ok {
		return ErrHostExists
	}
	cp := map[string]string{}
	for k, v := range labels {
		cp[k] = v
	}
	n.hosts[id] = naiveHost{labels: cp, healthy: healthy}
	return nil
}

func (n *naiveSelector) setHealth(id string, healthy bool) error {
	h, ok := n.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	h.healthy = healthy
	n.hosts[id] = h
	return nil
}

func (n *naiveSelector) removeHost(id string) error {
	if _, ok := n.hosts[id]; !ok {
		return ErrHostNotFound
	}
	delete(n.hosts, id)
	return nil
}

// evalResult 记录一次评估/回退的判定依据，供测试日志打印。
type evalResult struct {
	stage    string // direct | fallback-key | global-any | global-default
	subsetID string
	all      []string
	healthy  []string
	counter  int64
	panic    bool
	picked   string
}

func (n *naiveSelector) match(labels map[string]string, keys []string) (all, healthy []string) {
	for id, h := range n.hosts {
		ok := true
		for _, k := range keys {
			v, present := h.labels[k]
			if !present || v != labels[k] {
				ok = false
				break
			}
		}
		if ok {
			all = append(all, id)
			if h.healthy {
				healthy = append(healthy, id)
			}
		}
	}
	sort.Strings(all)
	sort.Strings(healthy)
	return
}

// evaluate 返回 (结果, 判定依据)。结果 picked=="" 且 err==nil 表示本次评估落空。
func (n *naiveSelector) evaluate(stage string, labels map[string]string, keys []string) (string, error, *evalResult) {
	all, healthy := n.match(labels, keys)
	r := &evalResult{
		stage:    stage,
		subsetID: nSubsetID(labels, keys),
		all:      all,
		healthy:  healthy,
	}
	if len(all) == 0 {
		return "", nil, r
	}
	id := r.subsetID
	r.counter = n.counts[id]
	var chosen string
	if len(healthy)*100 < n.pt*len(all) {
		r.panic = true
		chosen = all[r.counter%int64(len(all))]
	} else if len(healthy) > 0 {
		chosen = healthy[r.counter%int64(len(healthy))]
	}
	if chosen == "" {
		return "", nil, r
	}
	n.counts[id] = r.counter + 1
	r.picked = chosen
	return chosen, nil, r
}

func (n *naiveSelector) healthyList(filter func(naiveHost) bool) []string {
	var out []string
	for id, h := range n.hosts {
		if h.healthy && (filter == nil || filter(h)) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// route 返回选中 id、错误与本次路由涉及的全部判定依据（用于日志）。
func (n *naiveSelector) route(labels map[string]string) (string, error, []*evalResult) {
	for k := range labels {
		if k == "" {
			return "", ErrInvalidLabels, nil
		}
	}
	reqKeys := map[string]bool{}
	for k := range labels {
		reqKeys[k] = true
	}

	if len(reqKeys) > 0 {
		reqSorted := nSorted(reqKeys)
		for _, sel := range n.selectors {
			if setEq(sel.keys, reqSorted) {
				picked, err, r1 := n.evaluate("direct", labels, reqSorted)
				trail := []*evalResult{r1}
				if picked != "" || err != nil {
					return picked, err, trail
				}
				if sel.fk != nil {
					fkSorted := nSorted(sel.fk)
					restricted := map[string]string{}
					for _, k := range fkSorted {
						if v, ok := labels[k]; ok {
							restricted[k] = v
						}
					}
					var r2 *evalResult
					picked, err, r2 = n.evaluate("fallback-key", restricted, fkSorted)
					trail = append(trail, r2)
					if picked != "" || err != nil {
						return picked, err, trail
					}
				}
				return n.globalFallback(false, trail)
			}
		}
	}
	return n.globalFallback(true, nil)
}

func (n *naiveSelector) globalFallback(noSelectorMatched bool, trail []*evalResult) (string, error, []*evalResult) {
	switch n.policy {
	case FallbackNone:
		if noSelectorMatched {
			return "", ErrNoMatchingSelector, trail
		}
		return "", ErrNoHealthyHost, trail
	case FallbackAny:
		cands := n.healthyList(nil)
		r := &evalResult{stage: "global-any", all: cands, healthy: cands, counter: n.g}
		trail = append(trail, r)
		if len(cands) == 0 {
			return "", ErrNoHealthyHost, trail
		}
		picked := cands[n.g%int64(len(cands))]
		n.g++
		r.picked = picked
		return picked, nil, trail
	case FallbackDefaultSubset:
		defKeys := make([]string, 0, len(n.def))
		for k := range n.def {
			defKeys = append(defKeys, k)
		}
		sort.Strings(defKeys)
		cands := n.healthyList(func(h naiveHost) bool {
			for _, k := range defKeys {
				if v, ok := h.labels[k]; !ok || v != n.def[k] {
					return false
				}
			}
			return true
		})
		r := &evalResult{stage: "global-default", subsetID: nSubsetID(n.def, defKeys), all: cands, healthy: cands, counter: n.d}
		trail = append(trail, r)
		if len(cands) == 0 {
			return "", ErrNoHealthyHost, trail
		}
		picked := cands[n.d%int64(len(cands))]
		n.d++
		r.picked = picked
		return picked, nil, trail
	}
	panic("unreachable")
}
