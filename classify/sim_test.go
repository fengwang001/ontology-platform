package classify

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/catalog"
	"ontology/lineage"
)

// naive 是"每次变更后从零全量重算"的朴素模拟，用作对照基准。
// 它与 Engine 实现相互独立：只按题面定义直接计算。
type naive struct {
	now    int64
	nmax   int
	cols   map[string]bool
	base   map[string]int
	floor  map[string]int
	capL   map[string]int
	capU   map[string]int64
	capS   map[string]bool
	out    map[string]map[string]lineage.Kind
	in     map[string]map[string]lineage.Kind
	eff    map[string]int
	expire []string // 最近一次被接受操作中失效的上限列
}

func newNaive(nmax int) *naive {
	return &naive{
		nmax:  nmax,
		cols:  map[string]bool{},
		base:  map[string]int{},
		floor: map[string]int{},
		capL:  map[string]int{},
		capU:  map[string]int64{},
		capS:  map[string]bool{},
		out:   map[string]map[string]lineage.Kind{},
		in:    map[string]map[string]lineage.Kind{},
		eff:   map[string]int{},
	}
}

func validNowSim(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

// expire 让 until <= now 的上限失效并记录失效列。
func (n *naive) expireCaps(now int64) {
	n.expire = n.expire[:0]
	for c := range n.cols {
		if n.capS[c] && n.capU[c] <= now {
			n.capS[c] = false
			n.expire = append(n.expire, c)
		}
	}
}

// recompute 按 Kahn 拓扑序从零全量重算所有列的 eff。
func (n *naive) recompute() {
	indeg := map[string]int{}
	var ready []string
	for c := range n.cols {
		indeg[c] = len(n.in[c])
		if indeg[c] == 0 {
			ready = append(ready, c)
		}
	}
	for len(ready) > 0 {
		u := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		raw := n.base[u]
		for src, k := range n.in[u] {
			if v := k.Map(n.eff[src]); v > raw {
				raw = v
			}
		}
		eff := raw
		if n.capS[u] && n.capL[u] < eff {
			eff = n.capL[u]
		}
		if n.floor[u] > eff {
			eff = n.floor[u]
		}
		n.eff[u] = eff
		for dst := range n.out[u] {
			indeg[dst]--
			if indeg[dst] == 0 {
				ready = append(ready, dst)
			}
		}
	}
}

// diff 对比重算前后的 eff，生成 Changed（exclude 为新登记列，不入 Changed）。
func (n *naive) diff(old map[string]int, exclude string) []Change {
	var changes []Change
	for c := range n.cols {
		if c == exclude {
			continue
		}
		if old[c] != n.eff[c] {
			changes = append(changes, Change{Col: c, Old: old[c], New: n.eff[c]})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Col < changes[j].Col })
	return changes
}

// mutate 是被接受操作的公共尾部：推进时钟、失效上限、重算、生成 Changed。
func (n *naive) mutate(now int64, exclude string, apply func()) []Change {
	old := make(map[string]int, len(n.eff))
	for c, v := range n.eff {
		old[c] = v
	}
	n.now = now
	n.expireCaps(now)
	apply()
	n.recompute()
	return n.diff(old, exclude)
}

func (n *naive) reachable(from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for v := range n.out[u] {
			if v == to {
				return true
			}
			if !seen[v] {
				seen[v] = true
				stack = append(stack, v)
			}
		}
	}
	return false
}

// 以下朴素操作与 Engine 的校验次序完全一致，错误直接复用 classify 的哨兵。

func (n *naive) addColumn(name string, base int, now int64) ([]Change, error) {
	if !catalog.ValidName(name) || !catalog.ValidLevel(base) || !validNowSim(now) {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	if n.cols[name] {
		return nil, ErrExists
	}
	if len(n.cols) >= n.nmax {
		return nil, ErrLimit
	}
	return n.mutate(now, name, func() {
		n.cols[name] = true
		n.base[name] = base
	}), nil
}

func (n *naive) setBase(name string, base int, now int64) ([]Change, error) {
	if !catalog.ValidName(name) || !catalog.ValidLevel(base) || !validNowSim(now) {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	if !n.cols[name] {
		return nil, ErrNotFound
	}
	return n.mutate(now, "", func() { n.base[name] = base }), nil
}

func (n *naive) addEdge(src, dst string, kind lineage.Kind, now int64) ([]Change, error) {
	if !catalog.ValidName(src) || !catalog.ValidName(dst) || !lineage.ValidKind(kind) || src == dst || !validNowSim(now) {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	if !n.cols[src] || !n.cols[dst] {
		return nil, ErrNotFound
	}
	if _, ok := n.out[src][dst]; ok {
		return nil, ErrExists
	}
	if len(n.in[dst]) >= lineage.MaxInDegree {
		return nil, ErrLimit
	}
	if n.reachable(dst, src) {
		return nil, ErrCycle
	}
	return n.mutate(now, "", func() {
		if n.out[src] == nil {
			n.out[src] = map[string]lineage.Kind{}
		}
		if n.in[dst] == nil {
			n.in[dst] = map[string]lineage.Kind{}
		}
		n.out[src][dst] = kind
		n.in[dst][src] = kind
	}), nil
}

func (n *naive) removeEdge(src, dst string, now int64) ([]Change, error) {
	if !catalog.ValidName(src) || !catalog.ValidName(dst) || src == dst || !validNowSim(now) {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	if !n.cols[src] || !n.cols[dst] {
		return nil, ErrNotFound
	}
	if _, ok := n.out[src][dst]; !ok {
		return nil, ErrEdgeNotFound
	}
	return n.mutate(now, "", func() {
		delete(n.out[src], dst)
		delete(n.in[dst], src)
	}), nil
}

func (n *naive) raise(name string, l int, now int64) ([]Change, error) {
	if !catalog.ValidName(name) || !catalog.ValidLevel(l) || !validNowSim(now) {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	if !n.cols[name] {
		return nil, ErrNotFound
	}
	return n.mutate(now, "", func() { n.floor[name] = l }), nil
}

func (n *naive) declass(name string, l int, until, now int64) ([]Change, error) {
	if !catalog.ValidName(name) || !catalog.ValidLevel(l) || !validNowSim(now) || until <= now {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	if !n.cols[name] {
		return nil, ErrNotFound
	}
	return n.mutate(now, "", func() {
		n.capL[name] = l
		n.capU[name] = until
		n.capS[name] = true
	}), nil
}

func (n *naive) tick(now int64) ([]Change, error) {
	if !validNowSim(now) {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClock
	}
	return n.mutate(now, "", func() {}), nil
}

// why 按题面次序直接判定决定 eff 的来源。
func (n *naive) why(name string) Reason {
	raw := n.base[name]
	for src, k := range n.in[name] {
		if v := k.Map(n.eff[src]); v > raw {
			raw = v
		}
	}
	minCR := raw
	if n.capS[name] && n.capL[name] < minCR {
		minCR = n.capL[name]
	}
	if n.floor[name] > minCR {
		return Reason{Kind: ReasonFloor}
	}
	if n.capS[name] && n.capL[name] < raw {
		return Reason{Kind: ReasonCap}
	}
	baseWins := true
	best, bestSrc := -1, ""
	for src, k := range n.in[name] {
		v := k.Map(n.eff[src])
		if v > n.base[name] {
			baseWins = false
		}
		if v > best || (v == best && src < bestSrc) {
			best, bestSrc = v, src
		}
	}
	if baseWins {
		return Reason{Kind: ReasonBase}
	}
	return Reason{Kind: ReasonEdge, Src: bestSrc}
}

// errClass 把错误映射到可比较的类别名。
func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalid):
		return "ErrInvalid"
	case errors.Is(err, ErrClock):
		return "ErrClock"
	case errors.Is(err, ErrNotFound):
		return "ErrNotFound"
	case errors.Is(err, ErrExists):
		return "ErrExists"
	case errors.Is(err, ErrEdgeNotFound):
		return "ErrEdgeNotFound"
	case errors.Is(err, ErrLimit):
		return "ErrLimit"
	case errors.Is(err, ErrCycle):
		return "ErrCycle"
	default:
		return fmt.Sprintf("unknown(%v)", err)
	}
}

func makeNamePool(rng *rand.Rand, n int) []string {
	alphabet := []byte("abzABZ019\x01\x7f\x80\xc3\xff")
	seen := map[string]bool{}
	pool := make([]string, 0, n)
	for len(pool) < n {
		l := 1 + rng.Intn(8)
		b := make([]byte, l)
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(b)
		if !seen[s] {
			seen[s] = true
			pool = append(pool, s)
		}
	}
	return pool
}

type recOp struct {
	desc    string
	apply   func(e *Engine) ([]Change, error)
	changed []Change
	errCls  string
}

// TestRandomSimulation 1500 组随机操作序列，与从零全量重算的朴素模拟逐步对照，
// 并校验 evals 上界与重放确定性。日志打印每步输入、输出与判定依据。
func TestRandomSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(1380))
	for seq := 0; seq < 1500; seq++ {
		runSequence(t, rng, seq)
		if t.Failed() {
			t.Fatalf("seq %d 失败，停止后续序列", seq)
		}
	}
}

func runSequence(t *testing.T, rng *rand.Rand, seq int) {
	nmax := 6 + rng.Intn(12)
	eng, err := New(nmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	n := newNaive(nmax)
	pool := makeNamePool(rng, 20)
	steps := 25 + rng.Intn(30)
	var replay []recOp

	existing := func() []string {
		cols := make([]string, 0, len(n.cols))
		for c := range n.cols {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		return cols
	}
	pickName := func() string {
		r := rng.Intn(100)
		cols := existing()
		switch {
		case r < 3:
			return ""
		case r < 5:
			return string(make([]byte, 129))
		case r < 75 && len(cols) > 0:
			return cols[rng.Intn(len(cols))]
		default:
			return pool[rng.Intn(len(pool))]
		}
	}
	randLevel := func() int { return rng.Intn(7) - 1 } // -1..5，含非法值

	for step := 0; step < steps; step++ {
		now := n.now + int64(rng.Intn(10)) - 3
		if now < 0 {
			now = 0
		}
		var desc, direct string
		var gotC []Change
		var gotErr, wantErr error
		var wantC []Change
		var apply func(e *Engine) ([]Change, error)
		applied := false // WhatIf 分支已在分支内应用过变更

		switch w := rng.Intn(100); {
		case w < 22: // AddColumn
			name, base := pickName(), randLevel()
			desc = fmt.Sprintf("AddColumn(%q,%d,%d)", name, base, now)
			direct = name
			apply = func(e *Engine) ([]Change, error) { return e.AddColumn(name, base, now) }
			wantC, wantErr = n.addColumn(name, base, now)
		case w < 42: // AddEdge
			src, dst := pickName(), pickName()
			kind := lineage.Kind(rng.Intn(5))
			desc = fmt.Sprintf("AddEdge(%q,%q,%d,%d)", src, dst, kind, now)
			direct = dst
			apply = func(e *Engine) ([]Change, error) { return e.AddEdge(src, dst, kind, now) }
			wantC, wantErr = n.addEdge(src, dst, kind, now)
		case w < 54: // SetBase
			name, base := pickName(), randLevel()
			desc = fmt.Sprintf("SetBase(%q,%d,%d)", name, base, now)
			direct = name
			apply = func(e *Engine) ([]Change, error) { return e.SetBase(name, base, now) }
			wantC, wantErr = n.setBase(name, base, now)
		case w < 64: // Raise
			name, l := pickName(), randLevel()
			desc = fmt.Sprintf("Raise(%q,%d,%d)", name, l, now)
			direct = name
			apply = func(e *Engine) ([]Change, error) { return e.Raise(name, l, now) }
			wantC, wantErr = n.raise(name, l, now)
		case w < 74: // Declass
			name, l := pickName(), randLevel()
			until := now + int64(rng.Intn(4))
			desc = fmt.Sprintf("Declass(%q,%d,%d,%d)", name, l, until, now)
			direct = name
			apply = func(e *Engine) ([]Change, error) { return e.Declass(name, l, until, now) }
			wantC, wantErr = n.declass(name, l, until, now)
		case w < 84: // RemoveEdge
			src, dst := pickName(), pickName()
			desc = fmt.Sprintf("RemoveEdge(%q,%q,%d)", src, dst, now)
			direct = dst
			apply = func(e *Engine) ([]Change, error) { return e.RemoveEdge(src, dst, now) }
			wantC, wantErr = n.removeEdge(src, dst, now)
		case w < 94 || len(n.cols) == 0: // Tick
			desc = fmt.Sprintf("Tick(%d)", now)
			apply = func(e *Engine) ([]Change, error) { return e.Tick(now) }
			wantC, wantErr = n.tick(now)
		default: // WhatIf 与随后真实 SetBase（不触发上限失效）逐项一致
			cols := existing()
			name := cols[rng.Intn(len(cols))]
			base := rng.Intn(5)
			nowUsed := n.now
			desc = fmt.Sprintf("WhatIf(%q,%d)+SetBase(@%d)", name, base, nowUsed)
			whatIf, err := eng.WhatIf(name, base)
			if err != nil {
				t.Fatalf("seq=%d step=%d WhatIf: %v", seq, step, err)
			}
			gotC, gotErr = eng.SetBase(name, base, nowUsed)
			wantC, wantErr = n.setBase(name, base, nowUsed)
			if gotErr != nil {
				t.Fatalf("seq=%d step=%d SetBase after WhatIf: %v", seq, step, gotErr)
			}
			if !reflect.DeepEqual(whatIf, gotC) {
				t.Errorf("seq=%d step=%d %s: WhatIf=%v 与 SetBase=%v 不一致",
					seq, step, desc, whatIf, gotC)
			}
			direct = name
			applied = true
			apply = func(e *Engine) ([]Change, error) { return e.SetBase(name, base, nowUsed) }
		}

		if apply != nil && !applied {
			gotC, gotErr = apply(eng)
		}
		t.Logf("seq=%d step=%d op=%s -> changed=%v err=%v", seq, step, desc, gotC, gotErr)

		if gotCls, wantCls := errClass(gotErr), errClass(wantErr); gotCls != wantCls {
			t.Fatalf("seq=%d step=%d %s: 引擎错误类别 %s != 朴素模拟 %s",
				seq, step, desc, gotCls, wantCls)
		}
		if gotErr == nil {
			if !reflect.DeepEqual(gotC, wantC) {
				t.Fatalf("seq=%d step=%d %s: Changed=%v, 朴素模拟=%v",
					seq, step, desc, gotC, wantC)
			}
			// evals 上界：S + 所有 eff 变化列的出边数之和。
			s := len(n.expire)
			if direct != "" {
				counted := false
				for _, x := range n.expire {
					if x == direct {
						counted = true
					}
				}
				if !counted {
					s++
				}
			}
			outSum := 0
			for _, ch := range gotC {
				outSum += len(eng.g.OutEdges(ch.Col))
			}
			if eng.evals > s+outSum {
				t.Fatalf("seq=%d step=%d %s: evals=%d 超过上界 S=%d + 出边和=%d",
					seq, step, desc, eng.evals, s, outSum)
			}
		}

		// 全列 Eff / Why 与朴素模拟对照（Why 即判定依据）。
		for _, c := range existing() {
			ge, err := eng.Eff(c)
			if err != nil || ge != n.eff[c] {
				t.Fatalf("seq=%d step=%d %s: Eff(%q)=%d,%v, 朴素模拟=%d",
					seq, step, desc, c, ge, err, n.eff[c])
			}
			gw, err := eng.Why(c)
			nw := n.why(c)
			if err != nil || gw != nw {
				t.Fatalf("seq=%d step=%d %s: Why(%q)=%+v,%v, 朴素判定=%+v",
					seq, step, desc, c, gw, err, nw)
			}
		}
		if cols := existing(); len(cols) > 0 {
			c := cols[rng.Intn(len(cols))]
			gw, _ := eng.Why(c)
			t.Logf("seq=%d step=%d 判定依据 Why(%q)=%+v eff=%d", seq, step, c, gw, n.eff[c])
		}

		if apply != nil {
			replay = append(replay, recOp{desc, apply, gotC, errClass(gotErr)})
		}
	}

	// 重放同一操作序列，Changed 序列与错误类别必须完全一致。
	eng2, err := New(nmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, rec := range replay {
		gotC, gotErr := rec.apply(eng2)
		if errClass(gotErr) != rec.errCls || !reflect.DeepEqual(gotC, rec.changed) {
			t.Fatalf("seq=%d 重放第 %d 步 %s: got (%v,%s), want (%v,%s)",
				seq, i, rec.desc, gotC, errClass(gotErr), rec.changed, rec.errCls)
		}
	}
}
