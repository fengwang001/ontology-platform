package fanout

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/matrix"
)

// simCombo 与 simExec 是按题目规则逐步写成的朴素模拟，用于对照被测实现。

type simCombo struct {
	pairs map[string]string
	base  bool
}

func simExpand(cfg matrix.Config) []simCombo {
	var keys []string
	var lists [][]string
	for _, ax := range cfg.Axes {
		keys = append(keys, ax.Key)
		lists = append(lists, ax.Values)
	}
	var combos []simCombo
	var walk func(ax int, m map[string]string)
	walk = func(ax int, m map[string]string) {
		if ax == len(keys) {
			cp := make(map[string]string, len(m))
			for k, v := range m {
				cp[k] = v
			}
			combos = append(combos, simCombo{pairs: cp, base: true})
			return
		}
		for _, v := range lists[ax] {
			m[keys[ax]] = v
			walk(ax+1, m)
		}
	}
	walk(0, map[string]string{})

	var kept []simCombo
	for _, c := range combos {
		excluded := false
		for _, item := range cfg.Exclude {
			all := true
			for k, v := range item {
				if c.pairs[k] != v {
					all = false
					break
				}
			}
			if all {
				excluded = true
				break
			}
		}
		if !excluded {
			kept = append(kept, c)
		}
	}

	axisSet := map[string]bool{}
	for _, k := range keys {
		axisSet[k] = true
	}
	var appended []simCombo
	for _, inc := range cfg.Include {
		matched := false
		for i := range kept {
			ok := true
			for k, v := range inc {
				if axisSet[k] && kept[i].pairs[k] != v {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			matched = true
			for k, v := range inc {
				if !axisSet[k] {
					kept[i].pairs[k] = v
				}
			}
		}
		if !matched {
			cp := make(map[string]string, len(inc))
			for k, v := range inc {
				cp[k] = v
			}
			appended = append(appended, simCombo{pairs: cp})
		}
	}
	return append(kept, appended...)
}

func canonical(pairs map[string]string) string {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%s=%s", k, pairs[k])
	}
	return sb.String()
}

func comboPairs(c matrix.Combo) map[string]string {
	m := make(map[string]string, len(c.Keys()))
	for _, k := range c.Keys() {
		v, _ := c.Get(k)
		m[k] = v
	}
	return m
}

type simExec struct {
	states   []State
	runs     []int
	exp      []bool
	p        int
	failFast bool
	maxRound int
	round    int
	started  bool
	done     bool
	result   State
}

func newSimExec(combos []simCombo, p int, failFast bool, a int) *simExec {
	s := &simExec{
		states:   make([]State, len(combos)),
		runs:     make([]int, len(combos)),
		exp:      make([]bool, len(combos)),
		p:        p,
		failFast: failFast,
		maxRound: a,
	}
	for i, c := range combos {
		s.exp[i] = c.pairs["experimental"] == "true"
	}
	return s
}

func (s *simExec) checkDone() {
	if s.done || !s.started {
		return
	}
	for _, st := range s.states {
		if st == Running || st == Pending {
			return
		}
	}
	s.done = true
	res := Succeeded
	for i, st := range s.states {
		if st == Failed && !s.exp[i] {
			res = Failed
			break
		}
	}
	if res != Failed {
		for _, st := range s.states {
			if st == Cancelled {
				res = Cancelled
				break
			}
		}
	}
	s.result = res
}

func (s *simExec) Start() error {
	if s.started {
		return ErrState
	}
	s.started = true
	s.round = 1
	launched := 0
	for i := range s.states {
		if launched >= s.p {
			break
		}
		s.states[i] = Running
		s.runs[i]++
		launched++
	}
	s.checkDone()
	return nil
}

func (s *simExec) Finish(i int, ok bool) error {
	if i < 0 || i >= len(s.states) {
		return ErrInvalidParam
	}
	if s.states[i] != Running {
		return ErrState
	}
	if ok {
		s.states[i] = Succeeded
	} else {
		s.states[i] = Failed
	}
	if !ok && s.failFast && !s.exp[i] {
		for j := range s.states {
			if s.states[j] == Running || s.states[j] == Pending {
				s.states[j] = Cancelled
			}
		}
	} else {
		for j := range s.states {
			if s.states[j] == Pending {
				s.states[j] = Running
				s.runs[j]++
				break
			}
		}
	}
	s.checkDone()
	return nil
}

func (s *simExec) CancelAll() {
	if !s.started || s.done {
		return
	}
	for j := range s.states {
		if s.states[j] == Running || s.states[j] == Pending {
			s.states[j] = Cancelled
		}
	}
	s.checkDone()
}

func (s *simExec) Rerun() error {
	if !s.done || s.result == Succeeded {
		return ErrState
	}
	if s.round >= s.maxRound {
		return ErrRerunExhausted
	}
	s.round++
	for j := range s.states {
		if s.states[j] != Succeeded {
			s.states[j] = Pending
		}
	}
	s.done = false
	s.result = Pending
	launched := 0
	for j := range s.states {
		if launched >= s.p {
			break
		}
		if s.states[j] == Pending {
			s.states[j] = Running
			s.runs[j]++
			launched++
		}
	}
	return nil
}

func randomConfig(r *rand.Rand) Config {
	axisKeys := []string{"os", "ver", "arch"}
	valPool := []string{"linux", "win", "mac", "1", "2", "true", "x"}
	extraKeys := []string{"tag", "experimental", "extra"}

	nAx := 1 + r.Intn(len(axisKeys))
	axPerm := r.Perm(len(axisKeys))
	var axes []matrix.Axis
	var usedVals [][]string
	for i := 0; i < nAx; i++ {
		nV := 1 + r.Intn(4)
		vPerm := r.Perm(len(valPool))
		vals := make([]string, nV)
		for j := range vals {
			vals[j] = valPool[vPerm[j]]
		}
		axes = append(axes, matrix.Axis{Key: axisKeys[axPerm[i]], Values: vals})
		usedVals = append(usedVals, vals)
	}
	randVal := func() string { return valPool[r.Intn(len(valPool))] }

	var exclude []map[string]string
	for i, n := 0, r.Intn(4); i < n; i++ {
		item := map[string]string{}
		pick := r.Perm(nAx)[:1+r.Intn(nAx)]
		for _, ai := range pick {
			item[axes[ai].Key] = randVal()
		}
		exclude = append(exclude, item)
	}

	var include []map[string]string
	for i, n := 0, r.Intn(5); i < n; i++ {
		item := map[string]string{}
		nPairs := 1 + r.Intn(3)
		keyPool := append(append([]string{}, axisKeys[:nAx]...), extraKeys...)
		for _, ki := range r.Perm(len(keyPool))[:nPairs] {
			k := keyPool[ki]
			if k == "experimental" && r.Intn(10) < 7 {
				item[k] = "true"
			} else {
				item[k] = randVal()
			}
		}
		include = append(include, item)
	}

	return Config{
		Matrix:   matrix.Config{Axes: axes, Exclude: exclude, Include: include},
		P:        1 + r.Intn(4),
		FailFast: r.Intn(2) == 0,
		A:        1 + r.Intn(3),
	}
}

type op struct {
	kind string
	i    int
	ok   bool
}

func randomOps(r *rand.Rand, n int) []op {
	var ops []op
	for i, count := 0, 5+r.Intn(25); i < count; i++ {
		switch r.Intn(10) {
		case 0, 1:
			ops = append(ops, op{kind: "start"})
		case 2, 3, 4, 5, 6:
			ops = append(ops, op{kind: "finish", i: r.Intn(n+2) - 1, ok: r.Intn(100) < 65})
		case 7:
			ops = append(ops, op{kind: "cancel"})
		default:
			ops = append(ops, op{kind: "rerun"})
		}
	}
	return ops
}

func classify(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalidParam):
		return "invalid"
	case errors.Is(err, ErrState):
		return "state"
	case errors.Is(err, ErrRerunExhausted):
		return "exhausted"
	default:
		return "unknown:" + err.Error()
	}
}

func realStates(e *Executor) []State {
	out := make([]State, e.Len())
	for i := range out {
		out[i], _ = e.State(i)
	}
	return out
}

func realRuns(e *Executor) []int {
	out := make([]int, e.Len())
	for i := range out {
		out[i], _ = e.Runs(i)
	}
	return out
}

func equalSlice[T comparable](a, b []T) bool {
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

func resultBasis(s *simExec) string {
	if !s.done {
		return "本轮未终结"
	}
	for i, st := range s.states {
		if st == Failed && !s.exp[i] {
			return fmt.Sprintf("作业 %d 非试验性失败 → Failed", i)
		}
	}
	for i, st := range s.states {
		if st == Cancelled {
			return fmt.Sprintf("作业 %d 被取消 → Cancelled", i)
		}
	}
	return "无非试验性失败且无取消 → Succeeded"
}

func TestRandomAgainstSimulation(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	for g := 0; g < 1500; g++ {
		cfg := randomConfig(r)
		e, err := New(cfg)
		if err != nil {
			if !errors.Is(err, ErrEmpty) && !errors.Is(err, ErrTooLarge) {
				t.Fatalf("group %d: 合法生成器产出配置却报错 %v", g, err)
			}
			t.Logf("group %d: 展开被拒绝 err=%v 判定依据=exclude 全部剔除或无匹配 include", g, err)
			continue
		}

		// 展开对照：被测实现 vs 朴素模拟。
		simCombos := simExpand(cfg.Matrix)
		var gotExp, wantExp []string
		for i := 0; i < e.Len(); i++ {
			gotExp = append(gotExp, canonical(comboPairs(e.Job(i))))
		}
		for _, c := range simCombos {
			wantExp = append(wantExp, canonical(c.pairs))
		}
		if !equalSlice(gotExp, wantExp) {
			t.Fatalf("group %d: 展开不一致\n got: %q\nwant: %q\ncfg: %+v", g, gotExp, wantExp, cfg)
		}

		sim := newSimExec(simCombos, cfg.P, cfg.FailFast, cfg.A)
		ops := randomOps(r, e.Len())
		for step, o := range ops {
			var errA, errB error
			switch o.kind {
			case "start":
				errA, errB = e.Start(), sim.Start()
			case "finish":
				errA, errB = e.Finish(o.i, o.ok), sim.Finish(o.i, o.ok)
			case "cancel":
				e.CancelAll()
				sim.CancelAll()
			case "rerun":
				errA, errB = e.Rerun(), sim.Rerun()
			}
			if ca, cb := classify(errA), classify(errB); ca != cb {
				t.Fatalf("group %d step %d (%+v): 错误类别 %s != %s\ncfg: %+v",
					g, step, o, ca, cb, cfg)
			}
			if got, want := realStates(e), sim.states; !equalSlice(got, want) {
				t.Fatalf("group %d step %d (%+v): 状态 %v != %v\ncfg: %+v",
					g, step, o, got, want, cfg)
			}
			if got, want := realRuns(e), sim.runs; !equalSlice(got, want) {
				t.Fatalf("group %d step %d (%+v): 运行次数 %v != %v\ncfg: %+v",
					g, step, o, got, want, cfg)
			}
			if e.Round() != sim.round {
				t.Fatalf("group %d step %d (%+v): 轮次 %d != %d", g, step, o, e.Round(), sim.round)
			}
			gotRes, gotDone := e.Result()
			if gotDone != sim.done || (gotDone && gotRes != sim.result) {
				t.Fatalf("group %d step %d (%+v): 结果 (%v,%v) != (%v,%v)",
					g, step, o, gotRes, gotDone, sim.result, sim.done)
			}
			// 不变量：Running 数不超过 P；存在 Pending 时 Running 数恰为 P。
			if e.running > e.p {
				t.Fatalf("group %d step %d: Running 数 %d 超过 P=%d", g, step, e.running, e.p)
			}
			if e.started && !e.done && len(e.pending) > 0 && e.running != e.p {
				t.Fatalf("group %d step %d: 存在 Pending 时 Running 数 %d != P=%d",
					g, step, e.running, e.p)
			}
		}
		t.Logf("group %d: 输入 cfg=%+v ops=%d 步 输出 状态=%v 轮次=%d 结果=(%v,%v) 判定依据=%s",
			g, cfg, len(ops), sim.states, sim.round, sim.result, sim.done, resultBasis(sim))
	}
}

// TestFinishScansAtMostOne 证明单次 Finish 为寻找下一个待启动作业而检视的
// 作业数不超过 1，与 n 无关（n=16 与 n=256 两档对照）。
func TestFinishScansAtMostOne(t *testing.T) {
	vals := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("v%d", i)
		}
		return out
	}
	cases := []struct {
		name string
		cfg  matrix.Config
		n    int
	}{
		{"n=16", matrix.Config{Axes: []matrix.Axis{
			{Key: "a", Values: vals(4)},
			{Key: "b", Values: vals(4)},
		}}, 16},
		{"n=256", matrix.Config{Axes: []matrix.Axis{
			{Key: "a", Values: vals(8)},
			{Key: "b", Values: vals(8)},
			{Key: "c", Values: vals(4)},
		}}, 256},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := mustNew(t, Config{Matrix: tc.cfg, P: 1, A: 1})
			if err := e.Start(); err != nil {
				t.Fatalf("Start 报错: %v", err)
			}
			prev := e.scanned
			for {
				if _, done := e.Result(); done {
					break
				}
				running := -1
				for i := 0; i < e.Len(); i++ {
					if s, _ := e.State(i); s == Running {
						running = i
						break
					}
				}
				if running < 0 {
					t.Fatalf("未终结但无 Running 作业")
				}
				if err := e.Finish(running, true); err != nil {
					t.Fatalf("Finish(%d) 报错: %v", running, err)
				}
				if delta := e.scanned - prev; delta > 1 {
					t.Fatalf("单次 Finish 检视 %d 个作业, 超过 1", delta)
				}
				prev = e.scanned
			}
			if e.scanned != tc.n-1 {
				t.Fatalf("总检视数 = %d, want %d（每次 Finish 恰检视 1 个）", e.scanned, tc.n-1)
			}
			t.Logf("输入 n=%d 输出 scanned=%d 判定依据=单次 Finish 检视数 ≤1 与 n 无关", tc.n, e.scanned)
		})
	}
}
