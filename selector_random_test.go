package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是完全按题目规则逐步写成的朴素参考模型。
type naiveModel struct {
	selectors    []naiveSelector
	policy       FallbackPolicy
	defLabels    map[string]string
	pt           int
	hosts        map[string]map[string]string
	healthy      map[string]bool
	subsetCounts map[string]int
	globalCount  int
	defaultCount int
}

type naiveSelector struct {
	keys     []string
	fallback []string
}

func sortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func subsetID(keys []string, labels map[string]string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}

func cloneLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func newNaiveModel(specs []SelectorSpec, policy FallbackPolicy, defLabels map[string]string, pt int) *naiveModel {
	m := &naiveModel{
		policy:       policy,
		defLabels:    cloneLabels(defLabels),
		pt:           pt,
		hosts:        map[string]map[string]string{},
		healthy:      map[string]bool{},
		subsetCounts: map[string]int{},
	}
	for _, spec := range specs {
		var keys, fallback []string
		for k := range spec.KeySet {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if spec.FallbackKeys != nil {
			for k := range spec.FallbackKeys {
				fallback = append(fallback, k)
			}
			sort.Strings(fallback)
		}
		m.selectors = append(m.selectors, naiveSelector{keys: keys, fallback: fallback})
	}
	return m
}

func sameKeySet(a, b []string) bool {
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

func (m *naiveModel) matchingHosts(keys []string, labels map[string]string, onlyHealthy bool) []string {
	var result []string
	for id, hLabels := range m.hosts {
		if onlyHealthy && !m.healthy[id] {
			continue
		}
		ok := true
		for _, k := range keys {
			if v, exists := hLabels[k]; !exists || v != labels[k] {
				ok = false
				break
			}
		}
		if ok {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

// evaluate 对 (键集合, 标签) 做一次评估并在成功时推进该子集计数。
func (m *naiveModel) evaluate(keys []string, labels map[string]string, log *strings.Builder) (string, bool) {
	all := m.matchingHosts(keys, labels, false)
	healthyList := m.matchingHosts(keys, labels, true)
	id := subsetID(keys, labels)
	c := m.subsetCounts[id]
	fmt.Fprintf(log, "      评估子集{%s}: A=%v H=%v c=%d; ", id, all, healthyList, c)
	if len(all) > 0 && len(healthyList)*100 < m.pt*len(all) {
		picked := all[c%len(all)]
		m.subsetCounts[id] = c + 1
		fmt.Fprintf(log, "恐慌 %d*100<%d*%d -> A[%d%%%d]=%s c->%d\n",
			len(healthyList), m.pt, len(all), c, len(all), picked, c+1)
		return picked, true
	}
	if len(healthyList) > 0 {
		picked := healthyList[c%len(healthyList)]
		m.subsetCounts[id] = c + 1
		fmt.Fprintf(log, "非恐慌 -> H[%d%%%d]=%s c->%d\n",
			c, len(healthyList), picked, c+1)
		return picked, true
	}
	fmt.Fprintf(log, "落空 (|A|=%d,|H|=%d)\n", len(all), len(healthyList))
	return "", false
}

func (m *naiveModel) route(labels map[string]string, log *strings.Builder) (string, error) {
	fmt.Fprintf(log, "  Route(%v)\n", labels)
	reqKeys := sortedLabelKeys(labels)
	matched := false
	if len(reqKeys) > 0 {
		for _, sel := range m.selectors {
			if sameKeySet(reqKeys, sel.keys) {
				matched = true
				fmt.Fprintf(log, "    键集合精确相等 %v\n", sel.keys)
				if id, ok := m.evaluate(sel.keys, labels, log); ok {
					return id, nil
				}
				if len(sel.fallback) > 0 {
					fmt.Fprintf(log, "    降键一次 -> %v（不递归）\n", sel.fallback)
					if id, ok := m.evaluate(sel.fallback, labels, log); ok {
						return id, nil
					}
				}
				break
			}
		}
	}
	fmt.Fprintf(log, "    全局回退 policy=%d matchedSelector=%v\n", m.policy, matched)
	switch m.policy {
	case FallbackNone:
		if matched {
			return "", ErrNoHealthyHost
		}
		return "", ErrNoSelector
	case FallbackAnyEndpoint:
		cand := make([]string, 0)
		for id := range m.hosts {
			if m.healthy[id] {
				cand = append(cand, id)
			}
		}
		sort.Strings(cand)
		if len(cand) == 0 {
			return "", ErrNoHealthyHost
		}
		picked := cand[m.globalCount%len(cand)]
		m.globalCount++
		fmt.Fprintf(log, "      任意端点候选=%v g=%d -> %s\n", cand, m.globalCount-1, picked)
		return picked, nil
	case FallbackDefaultSubset:
		cand := make([]string, 0)
		for id, hLabels := range m.hosts {
			if !m.healthy[id] {
				continue
			}
			ok := true
			for k, v := range m.defLabels {
				if hv, exists := hLabels[k]; !exists || hv != v {
					ok = false
					break
				}
			}
			if ok {
				cand = append(cand, id)
			}
		}
		sort.Strings(cand)
		if len(cand) == 0 {
			return "", ErrNoHealthyHost
		}
		picked := cand[m.defaultCount%len(cand)]
		m.defaultCount++
		fmt.Fprintf(log, "      默认子集候选=%v d=%d -> %s\n", cand, m.defaultCount-1, picked)
		return picked, nil
	}
	return "", ErrUnsupportedPolicy
}

// routeOutcome 记录一次路由的可复现结果。
type routeOutcome struct {
	id  string
	err error
}

// TestRandomNaiveComparison 对 2000 组随机配置、主机集合与操作序列与朴素模型逐步对照，
// 并把相同操作序列在全新实例上重放，验证选择序列完全一致。
func TestRandomNaiveComparison(t *testing.T) {
	const totalCases = 2000
	for tc := 0; tc < totalCases; tc++ {
		seed := int64(1000 + tc)
		rng := rand.New(rand.NewSource(seed))
		specs, policy, defLabels, pt := randomConfig(rng)

		real, err := NewHostSelector(specs, policy, defLabels, pt)
		if err != nil {
			t.Fatalf("case %d: 随机配置应合法却被拒: %v", tc, err)
		}
		model := newNaiveModel(specs, policy, defLabels, pt)

		var log strings.Builder
		fmt.Fprintf(&log, "===== case %d seed=%d pt=%d policy=%d selectors=%v default=%v =====\n",
			tc, seed, pt, policy, selectorSummary(specs), defLabels)

		ops := make([]op, 8+rng.Intn(25))
		knownIDs := map[string]bool{}
		firstRun := make([]routeOutcome, 0, len(ops))
		for step := range ops {
			ops[step] = randomOp(rng, knownIDs)
			o := ops[step]
			fmt.Fprintf(&log, "  step %d %s\n", step, opSummary(o))

			gotID, gotErr := replayOp(real, o)
			var wantID string
			var wantErr error
			switch o.kind {
			case "add":
				if o.id == "" {
					wantErr = ErrEmptyID
				} else if hasEmptyKey(o.labels) {
					wantErr = ErrInvalidLabel
				} else if _, exists := model.hosts[o.id]; exists {
					wantErr = ErrDuplicateHost
				} else {
					model.hosts[o.id] = cloneLabels(o.labels)
					model.healthy[o.id] = o.healthy
					knownIDs[o.id] = true
				}
			case "health":
				if _, exists := model.hosts[o.id]; !exists {
					wantErr = ErrHostNotFound
				} else {
					model.healthy[o.id] = o.healthy
				}
			case "remove":
				if _, exists := model.hosts[o.id]; !exists {
					wantErr = ErrHostNotFound
				} else {
					delete(model.hosts, o.id)
					delete(model.healthy, o.id)
				}
			case "route":
				if hasEmptyKey(o.labels) {
					wantErr = ErrInvalidLabel
					fmt.Fprintf(&log, "      标签含空键 -> ErrInvalidLabel（不推进任何计数）\n")
				} else {
					wantID, wantErr = model.route(o.labels, &log)
				}
			}

			if !sameOutcome(gotID, gotErr, wantID, wantErr) {
				fmt.Fprintf(&log, "      实际输出: id=%q err=%v; 朴素模型: id=%q err=%v\n",
					gotID, gotErr, wantID, wantErr)
				t.Fatalf("case %d step %d 与朴素模型不一致:\n%s", tc, step, log.String())
			}
			if o.kind == "route" {
				firstRun = append(firstRun, routeOutcome{id: gotID, err: gotErr})
				if gotErr != nil {
					fmt.Fprintf(&log, "      => 报错 %v\n", gotErr)
				} else {
					fmt.Fprintf(&log, "      => 选中 %s\n", gotID)
				}
			}
		}

		if tc%100 == 0 {
			t.Logf("\n%s", log.String())
		}

		// 确定性重放：相同操作序列在新实例上重放，路由结果序列必须完全一致。
		real2, err := NewHostSelector(specs, policy, defLabels, pt)
		if err != nil {
			t.Fatalf("case %d: 重放构造失败: %v", tc, err)
		}
		idx := 0
		for _, o := range ops {
			gotID, gotErr := replayOp(real2, o)
			if o.kind != "route" {
				continue
			}
			want := firstRun[idx]
			idx++
			if !sameOutcome(gotID, gotErr, want.id, want.err) {
				t.Fatalf("case %d: 重放不确定: 第 %d 次路由首次=(%q,%v) 重放=(%q,%v)",
					tc, idx, want.id, want.err, gotID, gotErr)
			}
		}
	}
}

func sameOutcome(gotID string, gotErr error, wantID string, wantErr error) bool {
	if (gotErr == nil) != (wantErr == nil) {
		return false
	}
	if gotErr != nil {
		return errors.Is(gotErr, wantErr)
	}
	return gotID == wantID
}

// op 是可序列化重放的操作。
type op struct {
	kind    string
	id      string
	labels  map[string]string
	healthy bool
}

func replayOp(s *HostSelector, o op) (string, error) {
	switch o.kind {
	case "add":
		return "", s.AddHost(o.id, o.labels, o.healthy)
	case "health":
		return "", s.SetHealth(o.id, o.healthy)
	case "remove":
		return "", s.RemoveHost(o.id)
	case "route":
		return s.Route(o.labels)
	}
	return "", fmt.Errorf("unknown op %s", o.kind)
}

func opSummary(o op) string {
	switch o.kind {
	case "add":
		return fmt.Sprintf("AddHost(id=%q labels=%v healthy=%v)", o.id, o.labels, o.healthy)
	case "health":
		return fmt.Sprintf("SetHealth(id=%q healthy=%v)", o.id, o.healthy)
	case "remove":
		return fmt.Sprintf("RemoveHost(id=%q)", o.id)
	case "route":
		return fmt.Sprintf("Route(labels=%v)", o.labels)
	}
	return o.kind
}

func selectorSummary(specs []SelectorSpec) string {
	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		var keys, fk []string
		for k := range spec.KeySet {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for k := range spec.FallbackKeys {
			fk = append(fk, k)
		}
		sort.Strings(fk)
		parts = append(parts, fmt.Sprintf("{%v FK=%v}", keys, fk))
	}
	return strings.Join(parts, "; ")
}

var (
	randomKeyPool  = []string{"k1", "k2", "k3"}
	randomHostKeys = []string{"k1", "k2", "k3", "tier"}
	randomValues   = []string{"", "v1", "v2", "z1", "z2", "gold", "silver", "x"}
)

// randomConfig 生成保证合法的随机配置。
func randomConfig(rng *rand.Rand) ([]SelectorSpec, FallbackPolicy, map[string]string, int) {
	n := 1 + rng.Intn(3)
	used := map[string]bool{}
	specs := make([]SelectorSpec, 0, n)
	for len(specs) < n {
		size := 1 + rng.Intn(len(randomKeyPool))
		perm := rng.Perm(len(randomKeyPool))[:size]
		keys := map[string]struct{}{}
		sorted := make([]string, 0, size)
		for _, idx := range perm {
			keys[randomKeyPool[idx]] = struct{}{}
			sorted = append(sorted, randomKeyPool[idx])
		}
		sort.Strings(sorted)
		sig := strings.Join(sorted, "|")
		if used[sig] {
			continue
		}
		used[sig] = true
		spec := SelectorSpec{KeySet: keys}
		if len(keys) >= 2 && rng.Intn(2) == 0 {
			fkSize := 1 + rng.Intn(len(keys)-1)
			spec.FallbackKeys = map[string]struct{}{}
			for j := 0; j < fkSize; j++ {
				spec.FallbackKeys[sorted[j]] = struct{}{}
			}
		}
		specs = append(specs, spec)
	}
	policy := FallbackPolicy(rng.Intn(3))
	var def map[string]string
	if policy == FallbackDefaultSubset {
		def = map[string]string{"tier": []string{"gold", "silver"}[rng.Intn(2)]}
	}
	pt := []int{0, 1, 50, 99, 100}[rng.Intn(5)]
	return specs, policy, def, pt
}

func randomHostLabels(rng *rand.Rand) map[string]string {
	n := 1 + rng.Intn(len(randomHostKeys))
	perm := rng.Perm(len(randomHostKeys))[:n]
	labels := map[string]string{}
	for _, idx := range perm {
		labels[randomHostKeys[idx]] = randomValues[rng.Intn(len(randomValues))]
	}
	return labels
}

func randomRequestLabels(rng *rand.Rand) map[string]string {
	switch rng.Intn(8) {
	case 0:
		return map[string]string{} // K 为空
	case 1:
		return map[string]string{"": "bad"} // 空键非法
	case 2:
		// 恰好一个选择器键。
		return map[string]string{"k1": randomValues[rng.Intn(len(randomValues))]}
	case 3:
		// 恰好两个选择器键。
		return map[string]string{
			"k1": randomValues[rng.Intn(len(randomValues))],
			"k2": randomValues[rng.Intn(len(randomValues))],
		}
	default:
		n := 1 + rng.Intn(len(randomHostKeys))
		perm := rng.Perm(len(randomHostKeys))[:n]
		labels := map[string]string{}
		for _, idx := range perm {
			labels[randomHostKeys[idx]] = randomValues[rng.Intn(len(randomValues))]
		}
		return labels
	}
}

// randomOp 生成随机操作；knownIDs 跟踪当前（逻辑上）已登记的 id。
func randomOp(rng *rand.Rand, knownIDs map[string]bool) op {
	ids := make([]string, 0, len(knownIDs))
	for id := range knownIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	switch rng.Intn(10) {
	case 0, 1, 2, 3: // add：含空 id、重复 id 与空键的拒绝分支
		if len(ids) > 0 && rng.Intn(5) == 0 {
			return op{kind: "add", id: ids[rng.Intn(len(ids))],
				labels: randomHostLabels(rng), healthy: rng.Intn(2) == 0}
		}
		id := fmt.Sprintf("h%03d", rng.Intn(12))
		if rng.Intn(20) == 0 {
			id = ""
		}
		labels := randomHostLabels(rng)
		if rng.Intn(20) == 0 {
			labels[""] = "bad"
		}
		return op{kind: "add", id: id, labels: labels, healthy: rng.Intn(5) != 0}
	case 4, 5: // health
		if len(ids) > 0 {
			return op{kind: "health", id: ids[rng.Intn(len(ids))], healthy: rng.Intn(2) == 0}
		}
		return op{kind: "health", id: "ghost", healthy: rng.Intn(2) == 0}
	case 6: // remove
		if len(ids) > 0 {
			id := ids[rng.Intn(len(ids))]
			delete(knownIDs, id)
			return op{kind: "remove", id: id}
		}
		return op{kind: "remove", id: "ghost"}
	default: // route
		return op{kind: "route", labels: randomRequestLabels(rng)}
	}
}
