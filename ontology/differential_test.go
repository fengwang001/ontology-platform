package ontology

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
)

const differentialRuns = 2000

// diffLog 同时写入测试日志（-v）与临时日志文件，包含每组的输入、输出与判定依据。
var diffLogFile *os.File

func TestMain(m *testing.M) {
	f, err := os.CreateTemp("", "ontology-diff-*.log")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create diff log:", err)
		os.Exit(1)
	}
	diffLogFile = f
	code := m.Run()
	fmt.Println("differential log:", f.Name())
	_ = f.Close()
	os.Exit(code)
}

func dlprintf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if testing.Verbose() {
		fmt.Println(line)
	}
	fmt.Fprintln(diffLogFile, line)
}

func sortedMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := "{"
	for i, k := range keys {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s=%q", k, m[k])
	}
	return s + "}"
}

func trailString(trail []*evalResult) string {
	s := ""
	for _, r := range trail {
		panicMark := ""
		if r.panic {
			panicMark = " PANIC"
		}
		pick := r.picked
		if pick == "" {
			pick = "-"
		}
		s += fmt.Sprintf("[%s subset=%q A=%v H=%v c=%d%s -> %s] ",
			r.stage, r.subsetID, r.all, r.healthy, r.counter, panicMark, pick)
	}
	return s
}

// TestDifferentialRandom 2000 组随机主机集合/操作序列，与朴素实现逐步对照。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))

	for run := 0; run < differentialRuns; run++ {
		// 随机配置。
		keyPool := []string{"v", "z", "r"}
		pt := rng.Intn(101) // 0..100
		policy := FallbackPolicy(rng.Intn(3))

		// 随机生成 1~3 个键集合互不相同的选择器。
		var sels []SelectorSpec
		usedSets := map[string]bool{}
		nSel := 1 + rng.Intn(3)
		for len(sels) < nSel {
			size := 1 + rng.Intn(len(keyPool))
			perm := rng.Perm(len(keyPool))[:size]
			keys := make([]string, size)
			for i, p := range perm {
				keys[i] = keyPool[p]
			}
			id := canonicalKeys(keys)
			if usedSets[id] {
				continue
			}
			usedSets[id] = true
			spec := SelectorSpec{Keys: keys}
			// 50% 概率带 FK：随机非空真子集。
			if size >= 2 && rng.Intn(2) == 0 {
				subSize := 1 + rng.Intn(size-1)
				subPerm := rng.Perm(size)[:subSize]
				for _, p := range subPerm {
					spec.FallbackKeys = append(spec.FallbackKeys, keys[p])
				}
			}
			sels = append(sels, spec)
		}

		var def map[string]string
		if policy == FallbackDefaultSubset {
			def = map[string]string{"r": []string{"1", "2"}[rng.Intn(2)]}
		}

		s, err := NewHostSelector(sels, policy, def, pt)
		if err != nil {
			t.Fatalf("run %d: constructor: %v", run, err)
		}
		n := newNaive(sels, policy, def, pt)

		dlprintf("=== run %d pt=%d policy=%d def=%s ===", run, pt, policy, sortedMapOrEmpty(def))
		for _, sp := range sels {
			dlprintf("  selector keys=%v fk=%v", sp.Keys, sp.FallbackKeys)
		}

		// 随机主机集合：1~8 台，标签取自较小值域以制造命中与落空。
		vals := []string{"1", "2", ""}
		nHosts := 1 + rng.Intn(8)
		hostIDs := make([]string, 0, nHosts)
		for i := 0; i < nHosts; i++ {
			id := fmt.Sprintf("h%02d", i)
			labels := map[string]string{}
			for _, k := range keyPool {
				// 约 1/4 概率缺键。
				if rng.Intn(4) != 0 {
					labels[k] = vals[rng.Intn(len(vals))]
				}
			}
			healthy := rng.Intn(2) == 0
			errS := s.AddHost(id, labels, healthy)
			errN := n.addHost(id, labels, healthy)
			if fmt.Sprint(errS) != fmt.Sprint(errN) {
				t.Fatalf("run %d AddHost %s: %v vs %v", run, id, errS, errN)
			}
			hostIDs = append(hostIDs, id)
			dlprintf("  AddHost id=%s labels=%s healthy=%v -> %v", id, sortedMap(labels), healthy, errS)
		}

		// 随机操作序列。
		nOps := 5 + rng.Intn(20)
		for op := 0; op < nOps; op++ {
			switch rng.Intn(5) {
			case 0, 1, 2: // Route
				labels := map[string]string{}
				// 随机键子集（可能为空），值可能不存在于主机标签中。
				reqKeys := keyPool[:rng.Intn(len(keyPool)+1)]
				if rng.Intn(7) == 0 {
					labels[""] = "bad" // 非法标签
				}
				for _, k := range reqKeys {
					if rng.Intn(6) == 0 {
						labels[k] = "zz" // 制造落空的值
					} else {
						labels[k] = vals[rng.Intn(len(vals))]
					}
				}
				got, errS := s.Route(labels)
				want, errN, trail := n.route(labels)
				dlprintf("  Route labels=%s -> got=%q(%v) want=%q(%v) basis: %s",
					sortedMap(labels), got, errS, want, errN, trailString(trail))
				if got != want || fmt.Sprint(errS) != fmt.Sprint(errN) {
					t.Fatalf("run %d op %d Route %s: got (%q,%v) want (%q,%v)",
						run, op, labels, got, errS, want, errN)
				}
			case 3: // SetHealth
				id := hostIDs[rng.Intn(len(hostIDs))]
				h := rng.Intn(2) == 0
				errS := s.SetHealth(id, h)
				errN := n.setHealth(id, h)
				dlprintf("  SetHealth id=%s healthy=%v -> %v", id, h, errS)
				if fmt.Sprint(errS) != fmt.Sprint(errN) {
					t.Fatalf("run %d SetHealth: %v vs %v", run, errS, errN)
				}
			case 4: // RemoveHost 后再以可能不同状态加回，保持集合规模
				id := hostIDs[rng.Intn(len(hostIDs))]
				errS := s.RemoveHost(id)
				errN := n.removeHost(id)
				dlprintf("  RemoveHost id=%s -> %v", id, errS)
				if fmt.Sprint(errS) != fmt.Sprint(errN) {
					t.Fatalf("run %d RemoveHost: %v vs %v", run, errS, errN)
				}
				labels := map[string]string{}
				for _, k := range keyPool {
					if rng.Intn(4) != 0 {
						labels[k] = vals[rng.Intn(len(vals))]
					}
				}
				healthy := rng.Intn(2) == 0
				if err := s.AddHost(id, labels, healthy); err != nil {
					t.Fatalf("run %d re-add: %v", run, err)
				}
				if err := n.addHost(id, labels, healthy); err != nil {
					t.Fatalf("run %d naive re-add: %v", run, err)
				}
				dlprintf("  AddHost(id) id=%s labels=%s healthy=%v -> ok", id, sortedMap(labels), healthy)
			}
		}
	}
	dlprintf("differential runs complete: %d", differentialRuns)
}

func sortedMapOrEmpty(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	return sortedMap(m)
}
