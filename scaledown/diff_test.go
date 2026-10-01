package scaledown

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// op 是重放用的单条操作；两种实现按完全相同的序列执行。
type op struct {
	kind string // addnode addpod removepod tick
	name string
	ca   int64
	ma   int64
	id   string
	node string
	pc   int64
	pm   int64
	podk string
	now  int64
}

// 生成单个随机场景。刻意制造低利用节点、空间紧张目标与多轮持续。
func genScenario(rng *rand.Rand) (int, int64, int, []op) {
	p := 1 + rng.Intn(100)
	var t int64 = int64(1 + rng.Intn(8))
	minNodes := rng.Intn(3)

	var ops []op
	nNodes := 1 + rng.Intn(6)
	nodeNames := make([]string, 0, nNodes)
	for i := 0; i < nNodes; i++ {
		name := fmt.Sprintf("n%02d", i)
		nodeNames = append(nodeNames, name)
		// 小容量更易出现放不下；偶尔大容量。
		capv := int64(20 + rng.Intn(200))
		ops = append(ops, op{kind: "addnode", name: name, ca: capv, ma: capv + int64(rng.Intn(20))})
	}

	// 偶尔制造字节序相似的节点名。
	if rng.Intn(3) == 0 {
		name := []string{"a", "b", "aa", "ab", "a0"}[rng.Intn(5)]
		if !contains(nodeNames, name) {
			nodeNames = append(nodeNames, name)
			ops = append(ops, op{kind: "addnode", name: name,
				ca: int64(20 + rng.Intn(200)), ma: int64(20 + rng.Intn(200))})
		}
	}

	nPods := rng.Intn(24)
	for i := 0; i < nPods; i++ {
		node := nodeNames[rng.Intn(len(nodeNames))]
		id := fmt.Sprintf("p%03d", i)
		roll := rng.Intn(10)
		k := KindNormal
		switch {
		case roll < 2:
			k = KindDaemon
		case roll == 2:
			k = KindPinned
		}
		// 请求偏小以制造低利用；偶有 0 请求（另一维非 0）。
		pc := int64(rng.Intn(60))
		pm := int64(rng.Intn(60))
		if rng.Intn(8) == 0 {
			pc = 0
		}
		if rng.Intn(8) == 0 {
			pm = 0
		}
		ops = append(ops, op{kind: "addpod", id: id, node: node, pc: pc, pm: pm, podk: k})
	}

	// 穿插删除与多轮 tick。now 单调不减，偶尔相等。
	now := int64(rng.Intn(3))
	nTicks := 1 + rng.Intn(10)
	for i := 0; i < nTicks; i++ {
		if rng.Intn(3) == 0 {
			ops = append(ops, op{kind: "removepod", id: fmt.Sprintf("p%03d", rng.Intn(nPods+2))})
		}
		ops = append(ops, op{kind: "tick", now: now})
		now += int64(rng.Intn(4))
	}

	// 再补一批 Pod（落在可能已缩容后的集群）与 tick。
	for i := nPods; i < nPods+1+rng.Intn(6); i++ {
		node := nodeNames[rng.Intn(len(nodeNames))]
		ops = append(ops, op{kind: "addpod", id: fmt.Sprintf("p%03d", i), node: node,
			pc: int64(rng.Intn(50)), pm: int64(rng.Intn(50)), podk: KindNormal})
	}
	for i := 0; i < 1+rng.Intn(5); i++ {
		ops = append(ops, op{kind: "tick", now: now})
		now += int64(rng.Intn(5))
	}

	// 少量“必然非法”操作，验证拒绝行为与状态不变。
	if rng.Intn(2) == 0 {
		ops = append(ops, op{kind: "addnode", name: "", ca: 1, ma: 1})
		ops = append(ops, op{kind: "addpod", id: "", node: "x", pc: 1, pm: 1, podk: KindNormal})
		ops = append(ops, op{kind: "tick", now: -1})
		ops = append(ops, op{kind: "tick", now: now - 100})
	}
	return p, t, minNodes, ops
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func runActual(t *testing.T, p int, dur int64, min int, ops []op) (*Scaler, []string, []string) {
	t.Helper()
	s, err := New(p, dur, min)
	if err != nil {
		t.Fatalf("actual New: %v", err)
	}
	var log, results []string
	for _, o := range ops {
		switch o.kind {
		case "addnode":
			err = s.AddNode(o.name, o.ca, o.ma)
			log = append(log, fmt.Sprintf("AddNode(%q,%d,%d)->%v", o.name, o.ca, o.ma, err))
		case "addpod":
			err = s.AddPod(Pod{ID: o.id, Node: o.node, PC: o.pc, PM: o.pm, Kind: o.podk})
			log = append(log, fmt.Sprintf("AddPod(%q@%q %s %d/%d)->%v", o.id, o.node, o.podk, o.pc, o.pm, err))
		case "removepod":
			err = s.RemovePod(o.id)
			log = append(log, fmt.Sprintf("RemovePod(%q)->%v", o.id, err))
		case "tick":
			var res TickResult
			res, err = s.Tick(o.now)
			log = append(log, fmt.Sprintf("Tick(%d)->removed=%q mig=%v since=%v",
				o.now, res.Removed, res.Migrations, s.Since()))
			results = append(results, fmt.Sprintf("%d:%s|%s", o.now, res.Removed, migs(res.Migrations)))
		}
	}
	return s, log, results
}

func runNaive(t *testing.T, p int, dur int64, min int, ops []op) (*naiveSim, []string, []string) {
	t.Helper()
	m := naiveNew(p, dur, min)
	var log, results []string
	for _, o := range ops {
		switch o.kind {
		case "addnode":
			err := m.addNode(o.name, o.ca, o.ma)
			log = append(log, fmt.Sprintf("AddNode(%q,%d,%d)->%v", o.name, o.ca, o.ma, err))
		case "addpod":
			err := m.addPod(Pod{ID: o.id, Node: o.node, PC: o.pc, PM: o.pm, Kind: o.podk})
			log = append(log, fmt.Sprintf("AddPod(%q@%q %s %d/%d)->%v", o.id, o.node, o.podk, o.pc, o.pm, err))
		case "removepod":
			err := m.removePod(o.id)
			log = append(log, fmt.Sprintf("RemovePod(%q)->%v", o.id, err))
		case "tick":
			res, err := m.tick(o.now)
			log = append(log, fmt.Sprintf("Tick(%d)->removed=%q mig=%v since=%v",
				o.now, res.Removed, res.Migrations, m.since()))
			results = append(results, fmt.Sprintf("%d:%s|%s", o.now, res.Removed, migs(res.Migrations)))
			_ = err
		}
	}
	return m, log, results
}

func migs(ms []Migration) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, m.PodID+"->"+m.Target)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func actualSnapshot(s *Scaler) snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	nodeNames := make([]string, 0, len(s.nodes))
	for n := range s.nodes {
		nodeNames = append(nodeNames, n)
	}
	sort.Strings(nodeNames)
	var ns []string
	for _, n := range nodeNames {
		st := s.nodes[n]
		ns = append(ns, n+":"+itoa(st.ca)+"/"+itoa(st.ma))
	}
	var lines []string
	for _, p := range s.pods {
		lines = append(lines, p.id+"@"+p.node+"#"+p.kind+":"+itoa(p.pc)+"/"+itoa(p.pm))
	}
	sort.Strings(lines)
	var sl []string
	for _, n := range nodeNames {
		if st := s.nodes[n]; st.hasSince {
			sl = append(sl, n+"="+itoa(st.since))
		}
	}
	return snapshot{nodes: joinComma(ns), pods: joinComma(lines), since: joinComma(sl)}
}

// TestRandomDifferential 用 2000 组随机场景与朴素参照实现逐步对照。
func TestRandomDifferential(t *testing.T) {
	const N = 2000
	for seed := int64(1); seed <= N; seed++ {
		rng := rand.New(rand.NewSource(seed))
		p, dur, min, ops := genScenario(rng)

		s, aLog, aResults := runActual(t, p, dur, min, ops)
		m, nLog, nResults := runNaive(t, p, dur, min, ops)

		fail := func(msg string) {
			t.Fatalf("seed=%d %s\nP=%d T=%d Min=%d\n--- actual ---\n%s\n--- naive ---\n%s\nops=%v",
				seed, msg, p, dur, min,
				strings.Join(aLog, "\n"), strings.Join(nLog, "\n"), ops)
		}
		if len(aResults) != len(nResults) {
			fail("tick result count differs")
		}
		for i := range aResults {
			if aResults[i] != nResults[i] {
				fail(fmt.Sprintf("tick[%d] differs: actual=%s naive=%s", i, aResults[i], nResults[i]))
			}
		}
		as, ns := actualSnapshot(s), m.snapshot()
		if as != ns {
			fail(fmt.Sprintf("final state differs:\nactual=%+v\nnaive =%+v", as, ns))
		}

		// 判定依据日志（-v 时可见全部 2000 组的输入、输出与 since）。
		t.Logf("seed=%d P=%d T=%d Min=%d ops=%d removals=%s",
			seed, p, dur, min, len(ops), strings.Join(aResults, " ;; "))
		if seed <= 3 {
			t.Logf("seed=%d full trace:\n%s", seed, strings.Join(aLog, "\n"))
		}
	}
}
