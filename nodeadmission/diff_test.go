package nodeadmission

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

// ---- Independent naive simulation, written directly from the spec ----

type simCode int

const (
	simOK simCode = iota
	simInvalidConfig
	simInvalidArgument
	simClockBack
	simPodExists
	simNodeNotFound
	simUntolerated
	simCapacity
	simNodeExists
	simTaintMissing
)

type simTaint struct {
	key, value, effect string
	addedAt            int64
}

type simPod struct {
	node        string
	tols        []Toleration
	terminating bool
	releaseAt   int64
}

type simNode struct {
	maxPods int64
	taints  []*simTaint
	pods    map[string]*simPod
}

type sim struct {
	grace, rate int64
	lastNow     int64
	nodes       map[string]*simNode
	pods        map[string]*simPod
}

func newSim(g, r int64) *sim {
	return &sim{grace: g, rate: r, lastNow: -1,
		nodes: map[string]*simNode{}, pods: map[string]*simPod{}}
}

func validEffectSpec(e string) bool {
	return e == NoSchedule || e == PreferNoSchedule || e == NoExecute
}

func validTolSpec(t Toleration) bool {
	if t.Operator != OpEqual && t.Operator != OpExists {
		return false
	}
	if t.Effect != "" && !validEffectSpec(t.Effect) {
		return false
	}
	if t.Key == "" {
		if t.Operator != OpExists || t.Value != "" {
			return false
		}
	} else if t.Operator == OpExists && t.Value != "" {
		return false
	}
	if t.Seconds != -1 {
		if t.Effect != NoExecute || t.Seconds < 0 || t.Seconds > 1e15 {
			return false
		}
	}
	return true
}

func matchSpec(tol Toleration, k, v, eff string) bool {
	if tol.Effect != "" && tol.Effect != eff {
		return false
	}
	if tol.Key != "" && tol.Key != k {
		return false
	}
	if tol.Operator == OpExists {
		return true
	}
	return tol.Value == v
}

func (s *sim) release(ns *simNode, now int64) {
	for id, p := range ns.pods {
		if p.terminating && now >= p.releaseAt {
			delete(ns.pods, id)
			delete(s.pods, id)
		}
	}
}

func (s *sim) addNode(name string, max int64) simCode {
	if name == "" || max < 1 || max > 1e6 {
		return simInvalidArgument
	}
	if _, ok := s.nodes[name]; ok {
		return simNodeExists
	}
	s.nodes[name] = &simNode{maxPods: max, pods: map[string]*simPod{}}
	return simOK
}

func (s *sim) taint(node string, tn Taint, now int64) simCode {
	if tn.Key == "" || !validEffectSpec(tn.Effect) || now < 0 || now > 1e15 {
		return simInvalidArgument
	}
	if now < s.lastNow {
		return simClockBack
	}
	ns, ok := s.nodes[node]
	if !ok {
		return simNodeNotFound
	}
	for _, ts := range ns.taints {
		if ts.key == tn.Key && ts.effect == tn.Effect {
			ts.value = tn.Value
			s.lastNow = now
			return simOK
		}
	}
	ns.taints = append(ns.taints, &simTaint{tn.Key, tn.Value, tn.Effect, now})
	s.lastNow = now
	return simOK
}

func (s *sim) untaint(node, key, eff string, now int64) simCode {
	if key == "" || !validEffectSpec(eff) || now < 0 || now > 1e15 {
		return simInvalidArgument
	}
	if now < s.lastNow {
		return simClockBack
	}
	ns, ok := s.nodes[node]
	if !ok {
		return simNodeNotFound
	}
	for i, ts := range ns.taints {
		if ts.key == key && ts.effect == eff {
			ns.taints = append(ns.taints[:i], ns.taints[i+1:]...)
			s.lastNow = now
			return simOK
		}
	}
	return simTaintMissing
}

func (s *sim) deadline(ns *simNode, tols []Toleration) (int64, bool) {
	best := int64(0)
	have := false
	for _, ts := range ns.taints {
		if ts.effect != NoExecute {
			continue
		}
		d := ts.addedAt
		inf := false
		matched := false
		for _, tol := range tols {
			if matchSpec(tol, ts.key, ts.value, ts.effect) {
				matched = true
				if tol.Seconds == -1 {
					inf = true
				} else {
					d = ts.addedAt + tol.Seconds*1000
				}
				break
			}
		}
		if matched && inf {
			continue
		}
		if !have || d < best {
			best, have = d, true
		}
	}
	return best, have
}

func (s *sim) schedule(id, node string, tols []Toleration, now int64) simCode {
	if id == "" || now < 0 || now > 1e15 {
		return simInvalidArgument
	}
	for _, t := range tols {
		if !validTolSpec(t) {
			return simInvalidArgument
		}
	}
	if now < s.lastNow {
		return simClockBack
	}
	if _, ok := s.pods[id]; ok {
		return simPodExists
	}
	ns, ok := s.nodes[node]
	if !ok {
		return simNodeNotFound
	}
	s.release(ns, now)
	var bad []*simTaint
	bad = append(bad, ns.taints...)
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].key != bad[j].key {
			return bad[i].key < bad[j].key
		}
		return bad[i].effect < bad[j].effect
	})
	for _, ts := range bad {
		if ts.effect != NoSchedule && ts.effect != NoExecute {
			continue
		}
		ok := false
		for _, tol := range tols {
			if matchSpec(tol, ts.key, ts.value, ts.effect) {
				ok = true
				break
			}
		}
		if !ok {
			return simUntolerated
		}
	}
	if int64(len(ns.pods)) >= ns.maxPods {
		return simCapacity
	}
	cp := append([]Toleration(nil), tols...)
	ns.pods[id] = &simPod{node: node, tols: cp}
	s.pods[id] = ns.pods[id]
	s.lastNow = now
	return simOK
}

func (s *sim) tick(now int64) ([]string, simCode) {
	if now < 0 || now > 1e15 {
		return nil, simInvalidArgument
	}
	if now < s.lastNow {
		return nil, simClockBack
	}
	s.lastNow = now
	for _, ns := range s.nodes {
		s.release(ns, now)
	}
	type cand struct {
		id string
		at int64
	}
	var all []cand
	for _, ns := range s.nodes {
		var due []cand
		for id, p := range ns.pods {
			if p.terminating {
				continue
			}
			at, ev := s.deadline(ns, p.tols)
			if ev && at <= now {
				due = append(due, cand{id, at})
			}
		}
		sort.Slice(due, func(i, j int) bool {
			if due[i].at != due[j].at {
				return due[i].at < due[j].at
			}
			return due[i].id < due[j].id
		})
		if int64(len(due)) > s.rate {
			due = due[:s.rate]
		}
		for _, c := range due {
			p := ns.pods[c.id]
			p.terminating = true
			p.releaseAt = now + s.grace
		}
		s.release(ns, now)
		all = append(all, due...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].at != all[j].at {
			return all[i].at < all[j].at
		}
		return all[i].id < all[j].id
	})
	out := make([]string, len(all))
	for i, c := range all {
		out[i] = c.id
	}
	return out, simOK
}

// ---- Random operation sequences ----

type opKind int

const (
	kAddNode opKind = iota
	kTaint
	kUntaint
	kSchedule
	kTick
)

type op struct {
	kind                opKind
	node, pod, key, val string
	eff, operator       string
	sec                 int64
	maxPods             int64
	now                 int64
	tols                []Toleration
	invalid             bool
}

var effects = []string{NoSchedule, PreferNoSchedule, NoExecute}
var operators = []string{OpEqual, OpExists}

func genSeq(rng *rand.Rand) ([]op, int64, int64) {
	g := int64(rng.Intn(3000))
	r := int64(1 + rng.Intn(4))
	if rng.Intn(10) == 0 {
		g = 0
	}
	n := 40 + rng.Intn(120)
	now := int64(0)
	keys := []string{"a", "b", "c"}
	vals := []string{"v1", "v2", ""}
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		kind := opKind(rng.Intn(5))
		o := op{kind: kind, node: fmt.Sprintf("n%d", rng.Intn(4)), pod: fmt.Sprintf("p%d", rng.Intn(10))}
		o.now = now
		if rng.Intn(8) == 0 {
			o.now = now - int64(1+rng.Intn(5)) // clock moved back
			o.invalid = true
		}
		switch kind {
		case kAddNode:
			o.maxPods = int64(1 + rng.Intn(4))
			if rng.Intn(15) == 0 {
				o.maxPods = 0
				o.invalid = true
			}
		case kTaint:
			o.key = keys[rng.Intn(len(keys))]
			o.val = vals[rng.Intn(len(vals))]
			o.eff = effects[rng.Intn(3)]
			if rng.Intn(20) == 0 {
				o.eff = "Weird"
				o.invalid = true
			}
		case kUntaint:
			o.key = keys[rng.Intn(len(keys))]
			o.eff = effects[rng.Intn(3)]
		case kSchedule:
			nt := rng.Intn(3)
			for j := 0; j < nt; j++ {
				tol := Toleration{
					Operator: operators[rng.Intn(2)],
					Effect:   effects[rng.Intn(4)%3],
				}
				if ki := rng.Intn(len(keys) + 1); ki < len(keys) {
					tol.Key = keys[ki]
				}
				if rng.Intn(8) == 0 {
					tol.Key = ""
					tol.Operator = OpExists
				}
				if tol.Operator == OpEqual {
					tol.Value = vals[rng.Intn(len(vals))]
				}
				if rng.Intn(7) == 0 {
					tol.Effect = ""
				}
				if tol.Effect == NoExecute && rng.Intn(2) == 0 {
					tol.Seconds = int64(rng.Intn(40))
				} else {
					tol.Seconds = -1
				}
				o.tols = append(o.tols, tol)
			}
		case kTick:
		}
		if rng.Intn(10) != 0 {
			now += int64(rng.Intn(3000))
		}
		ops = append(ops, o)
	}
	return ops, g, r
}

// ensure keys index access: keys[len(keys)] -> ""

func applySim(s *sim, o op) ([]string, simCode) {
	switch o.kind {
	case kAddNode:
		return nil, s.addNode(o.node, o.maxPods)
	case kTaint:
		return nil, s.taint(o.node, Taint{o.key, o.val, o.eff}, o.now)
	case kUntaint:
		return nil, s.untaint(o.node, o.key, o.eff, o.now)
	case kSchedule:
		return nil, s.schedule(o.pod, o.node, o.tols, o.now)
	default:
		return s.tick(o.now)
	}
}

func applyMgr(m *Manager, o op) ([]string, RejectCode) {
	var err error
	var out []string
	switch o.kind {
	case kAddNode:
		err = m.AddNode(o.node, o.maxPods)
	case kTaint:
		err = m.Taint(o.node, Taint{o.key, o.val, o.eff}, o.now)
	case kUntaint:
		err = m.Untaint(o.node, o.key, o.eff, o.now)
	case kSchedule:
		err = m.Schedule(o.pod, o.node, o.tols, o.now)
	default:
		out, err = m.Tick(o.now)
	}
	if err != nil {
		return nil, errCode(err)
	}
	return out, OK
}

var codeMap = map[simCode]RejectCode{
	simOK: OK, simInvalidConfig: ErrInvalidConfig, simInvalidArgument: ErrInvalidArgument,
	simClockBack: ErrClockMovedBack, simPodExists: ErrPodExists, simNodeNotFound: ErrNodeNotFound,
	simUntolerated: ErrUntoleratedTaint, simCapacity: ErrCapacity, simNodeExists: ErrNodeExists,
	simTaintMissing: ErrTaintNotFound,
}

func describe(o op) string {
	switch o.kind {
	case kAddNode:
		return fmt.Sprintf("AddNode(%q,%d)", o.node, o.maxPods)
	case kTaint:
		return fmt.Sprintf("Taint(%q,{%q,%q,%q},%d)", o.node, o.key, o.val, o.eff, o.now)
	case kUntaint:
		return fmt.Sprintf("Untaint(%q,%q,%q,%d)", o.node, o.key, o.eff, o.now)
	case kSchedule:
		return fmt.Sprintf("Schedule(%q,%q,%v,%d)", o.pod, o.node, o.tols, o.now)
	default:
		return fmt.Sprintf("Tick(%d)", o.now)
	}
}

func TestRandomDifferential(t *testing.T) {
	var dump strings.Builder
	rng := rand.New(rand.NewSource(20241001))
	for seq := 0; seq < 2000; seq++ {
		ops, g, r := genSeq(rng)
		m, err := NewManager(g, r)
		if err != nil {
			t.Fatalf("NewManager(%d,%d): %v", g, r, err)
		}
		s := newSim(g, r)
		fmt.Fprintf(&dump, "=== sequence %d G=%d R=%d ===\n", seq, g, r)
		for i, o := range ops {
			sout, sc := applySim(s, o)
			mout, mc := applyMgr(m, o)
			reason := "accepted"
			if sc != simOK {
				reason = fmt.Sprintf("rejected code=%d", sc)
			}
			if len(sout) > 0 {
				reason = fmt.Sprintf("evicted=%v (deadline<=now, rate limit applied)", sout)
			}
			fmt.Fprintf(&dump, "#%d 输入 %s; 输出 %v/%v; 判定依据: %s\n",
				i, describe(o), mout, mc, reason)
			if codeMap[sc] != mc {
				dump.WriteString("--- CODE MISMATCH ---\n")
				t.Fatalf("seq %d op %d %s: sim=%d mgr=%d\n%s", seq, i, describe(o), sc, mc, dump.String())
			}
			if sc == simOK {
				if len(sout) != len(mout) {
					t.Fatalf("seq %d op %d %s: eviction lists %v vs %v\n%s", seq, i, describe(o), sout, mout, dump.String())
				}
				for k := range sout {
					if sout[k] != mout[k] {
						t.Fatalf("seq %d op %d %s: order %v vs %v\n%s", seq, i, describe(o), sout, mout, dump.String())
					}
				}
			}
		}
		// Full state comparison: every node's running/terminating pods and
		// taint (key,effect,value,addedAt) set must match.
		for name, sn := range s.nodes {
			mn := m.nodes[name]
			if mn == nil {
				t.Fatalf("seq %d: node %s missing in manager\n%s", seq, name, dump.String())
			}
			if len(sn.pods) != len(mn.pods) {
				t.Fatalf("seq %d: node %s pod count sim=%d mgr=%d\n%s", seq, name, len(sn.pods), len(mn.pods), dump.String())
			}
			if len(sn.taints) != len(mn.taints) {
				t.Fatalf("seq %d: taint count mismatch\n%s", seq, dump.String())
			}
			for id, sp := range sn.pods {
				mp, ok := mn.pods[id]
				if !ok {
					t.Fatalf("seq %d: pod %s missing\n%s", seq, id, dump.String())
				}
				_ = mp
				mps := m.pods[id]
				if sp.terminating != mps.terminating || sp.releaseAt != mps.releaseAt {
					t.Fatalf("seq %d: pod %s lifecycle sim(t=%v,r=%d) mgr(t=%v,r=%d)\n%s",
						seq, id, sp.terminating, sp.releaseAt, mps.terminating, mps.releaseAt, dump.String())
				}
			}
			for _, stt := range sn.taints {
				mt, ok := mn.taints[taintMapKey(stt.key, stt.effect)]
				if !ok || mt.value != stt.value || mt.addedAt != stt.addedAt {
					t.Fatalf("seq %d: taint %s/%s mismatch\n%s", seq, stt.key, stt.effect, dump.String())
				}
			}
		}
		if seq%100 == 0 {
			dump.Reset()
		}
	}
	if err := os.WriteFile("/tmp/diff_run.log", []byte(dump.String()), 0o644); err == nil {
		t.Log("完整输入/输出/判定日志: /tmp/diff_run.log (最近100组)")
	}
}
