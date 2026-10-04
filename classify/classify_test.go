package classify

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func mustChange(t *testing.T, got []ChangedItem, want []ChangedItem, ctx string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: Changed = %v, want %v", ctx, got, want)
	}
}

func mustErr(t *testing.T, err error, target error, ctx string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, want %v", ctx, err, target)
	}
}

// 题目给出的完整示例。
func TestWorkedExample(t *testing.T) {
	e := NewEngine(10)
	var ch []ChangedItem
	ch, _ = e.AddColumn("a", 4, 1)
	mustChange(t, ch, nil, "add a")
	e.AddColumn("b", 0, 2)
	e.AddColumn("c", 0, 3)
	e.AddColumn("d", 1, 4)
	ch, _ = e.AddEdge("a", "b", Mask, 5)
	mustChange(t, ch, []ChangedItem{{"b", 0, 3}}, "edge a->b")
	ch, _ = e.AddEdge("b", "c", Hash, 6)
	mustChange(t, ch, []ChangedItem{{"c", 0, 1}}, "edge b->c")
	ch, _ = e.AddEdge("a", "d", Agg, 7)
	mustChange(t, ch, []ChangedItem{{"d", 1, 2}}, "edge a->d")

	for col, want := range map[string]int{"a": 4, "b": 3, "c": 1, "d": 2} {
		if v, _ := e.Eff(col); v != want {
			t.Fatalf("eff %s = %d, want %d", col, v, want)
		}
	}
	if w, _ := e.Why("d"); w.Kind != "Edge" || w.Src != "a" {
		t.Fatalf("Why(d) = %+v", w)
	}

	ch, _ = e.Declass("b", 1, 100, 10)
	mustChange(t, ch, []ChangedItem{{"b", 3, 1}, {"c", 1, 0}}, "declass b")
	ch, _ = e.Raise("c", 2, 20)
	mustChange(t, ch, []ChangedItem{{"c", 0, 2}}, "raise c")
	if w, _ := e.Why("c"); w.Kind != "Floor" {
		t.Fatalf("Why(c) = %+v, want Floor", w)
	}
	ch, _ = e.Tick(99)
	mustChange(t, ch, nil, "tick 99")
	before := e.Evals()
	ch, _ = e.Tick(100)
	mustChange(t, ch, []ChangedItem{{"b", 1, 3}}, "tick 100")
	if got := e.Evals() - before; got != 2 {
		t.Fatalf("evals at tick 100 = %d, want 2", got)
	}
	ch, _ = e.RemoveEdge("a", "b", 101)
	mustChange(t, ch, []ChangedItem{{"b", 3, 0}}, "remove a->b")
	if v, _ := e.Eff("c"); v != 2 {
		t.Fatalf("eff c after remove = %d, want 2", v)
	}
}

// 四种 kind 的降级与封顶。
func TestKinds(t *testing.T) {
	cases := []struct {
		kind Kind
		want int
	}{
		{Copy, 4}, {Mask, 3}, {Hash, 2}, {Agg, 2},
	}
	for _, tc := range cases {
		e := NewEngine(4)
		e.AddColumn("src", 4, 1)
		e.AddColumn("dst", 0, 2)
		ch, err := e.AddEdge("src", "dst", tc.kind, 3)
		if err != nil {
			t.Fatal(err)
		}
		mustChange(t, ch, []ChangedItem{{"dst", 0, tc.want}}, fmt.Sprintf("kind %v", tc.kind))
	}
	for x := 0; x <= 4; x++ {
		if got := Hash.Apply(x); got != maxInt(x-2, 0) {
			t.Fatalf("Hash %d = %d", x, got)
		}
		if got := Agg.Apply(x); got != minInt(x, 2) {
			t.Fatalf("Agg %d = %d", x, got)
		}
	}
}

// 下限压过上限。
func TestFloorOverCap(t *testing.T) {
	e := NewEngine(2)
	e.AddColumn("x", 4, 1)
	ch, _ := e.Declass("x", 1, 100, 2)
	mustChange(t, ch, []ChangedItem{{"x", 4, 1}}, "cap")
	if w, _ := e.Why("x"); w.Kind != "Cap" {
		t.Fatalf("Why = %+v, want Cap", w)
	}
	ch, _ = e.Raise("x", 3, 3)
	mustChange(t, ch, []ChangedItem{{"x", 1, 3}}, "floor wins")
	if w, _ := e.Why("x"); w.Kind != "Floor" {
		t.Fatalf("Why = %+v, want Floor", w)
	}
}

// until 恰等失效，小 1 仍生效。
func TestUntilBoundary(t *testing.T) {
	e := NewEngine(2)
	e.AddColumn("x", 4, 1)
	e.Declass("x", 1, 100, 10)
	ch, _ := e.Tick(99)
	mustChange(t, ch, nil, "t < until")
	if v, _ := e.Eff("x"); v != 1 {
		t.Fatalf("eff at 99 = %d", v)
	}
	ch, _ = e.Tick(100)
	mustChange(t, ch, []ChangedItem{{"x", 1, 4}}, "t == until expires")
}

// 删边导致级别下降。
func TestRemoveEdgeDecreases(t *testing.T) {
	e := NewEngine(2)
	e.AddColumn("a", 4, 1)
	e.AddColumn("b", 0, 2)
	e.AddEdge("a", "b", Copy, 3)
	ch, _ := e.RemoveEdge("a", "b", 4)
	mustChange(t, ch, []ChangedItem{{"b", 4, 0}}, "remove drops eff")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// eff 未变即止步：a 0->1，b 经 Mask 仍为 0，c 不求值。
func TestStopAtUnchanged(t *testing.T) {
	e := NewEngine(4)
	e.AddColumn("a", 0, 1)
	e.AddColumn("b", 0, 2)
	e.AddColumn("c", 0, 3)
	e.AddEdge("a", "b", Mask, 4)
	e.AddEdge("b", "c", Copy, 5)
	before := e.Evals()
	ch, _ := e.SetBase("a", 1, 6)
	mustChange(t, ch, []ChangedItem{{"a", 0, 1}}, "only a changes")
	if got := e.Evals() - before; got != 2 {
		t.Fatalf("evals = %d, want 2 (a,b; c pruned)", got)
	}
}

// 菱形汇合每列只求值一次。
func TestDiamondOnce(t *testing.T) {
	e := NewEngine(5)
	e.AddColumn("a", 0, 1)
	e.AddColumn("b", 0, 2)
	e.AddColumn("c", 0, 3)
	e.AddColumn("d", 0, 4)
	e.AddEdge("a", "b", Copy, 5)
	e.AddEdge("a", "c", Copy, 6)
	e.AddEdge("b", "d", Copy, 7)
	e.AddEdge("c", "d", Copy, 8)
	before := e.Evals()
	ch, _ := e.SetBase("a", 4, 9)
	mustChange(t, ch,
		[]ChangedItem{{"a", 0, 4}, {"b", 0, 4}, {"c", 0, 4}, {"d", 0, 4}},
		"diamond")
	if got := e.Evals() - before; got != 4 {
		t.Fatalf("evals = %d, want 4 (each column once)", got)
	}
}

// WhatIf 与随后真正 SetBase（不触发失效）逐项相同，且只读。
func TestWhatIfEquivalence(t *testing.T) {
	e := NewEngine(4)
	e.AddColumn("a", 4, 1)
	e.AddColumn("b", 0, 2)
	e.AddColumn("c", 0, 3)
	e.AddEdge("a", "b", Mask, 4)
	e.AddEdge("b", "c", Hash, 5)
	before := e.Evals()
	hyp, err := e.WhatIf("a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if e.Evals() != before {
		t.Fatal("WhatIf must not move evals")
	}
	if v, _ := e.Eff("a"); v != 4 {
		t.Fatalf("WhatIf mutated state: eff a = %d", v)
	}
	real, err := e.SetBase("a", 1, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hyp, real) {
		t.Fatalf("WhatIf %v != SetBase %v", hyp, real)
	}
}

// Why 的并列取字节序最小，Base 次序，无入边。
func TestWhyTieAndOrder(t *testing.T) {
	e := NewEngine(8)
	e.AddColumn("m", 3, 1)
	e.AddColumn("n", 3, 2)
	e.AddColumn("z", 0, 3)
	e.AddEdge("m", "z", Copy, 4)
	e.AddEdge("n", "z", Copy, 5)
	if w, _ := e.Why("z"); w.Kind != "Edge" || w.Src != "m" {
		t.Fatalf("tie Why = %+v, want Edge m", w)
	}
	e.SetBase("z", 3, 6)
	if w, _ := e.Why("z"); w.Kind != "Base" {
		t.Fatalf("base tie Why = %+v, want Base", w)
	}
	e.AddColumn("solo", 2, 7)
	if w, _ := e.Why("solo"); w.Kind != "Base" {
		t.Fatalf("no-in-edge Why = %+v, want Base", w)
	}
}

// 无关列 100 与 10000 两档下同一操作 evals 相同。
func TestUnrelatedColumnsDoNotAffectEvals(t *testing.T) {
	build := func(nmax, extra int) (*Engine, int64) {
		e := NewEngine(nmax)
		e.AddColumn("a", 0, 1)
		e.AddColumn("b", 0, 2)
		e.AddEdge("a", "b", Mask, 3)
		for i := 0; i < extra; i++ {
			name := fmt.Sprintf("u%05d", i)
			e.AddColumn(name, 0, 1)
		}
		return e, e.Evals()
	}
	e100, base100 := build(200, 100)
	e10000, base10000 := build(10200, 10000)
	ch1, _ := e100.SetBase("a", 4, 2000)
	ch2, _ := e10000.SetBase("a", 4, 2000)
	ev1, ev2 := e100.Evals()-base100, e10000.Evals()-base10000
	if ev1 != ev2 || ev1 != 2 {
		t.Fatalf("evals 100=%d 10000=%d, want equal 2", ev1, ev2)
	}
	if !reflect.DeepEqual(ch1, ch2) {
		t.Fatalf("Changed differs: %v vs %v", ch1, ch2)
	}
}

// 拒绝次序与被拒操作零副作用（时钟、cap 失效均不发生）。
func TestRejectionOrderAndNoSideEffect(t *testing.T) {
	e := NewEngine(40)
	e.Tick(10)
	_, err := e.AddColumn("", 0, 5)
	mustErr(t, err, ErrInvalid, "bad name beats clock")
	_, err = e.AddColumn("ok", 9, 11)
	mustErr(t, err, ErrInvalid, "bad level")
	_, err = e.Declass("ok", 1, 5, 11)
	mustErr(t, err, ErrInvalid, "until<=now beats missing column")
	_, err = e.SetBase("ghost", 0, 5)
	mustErr(t, err, ErrClock, "clock beats missing column")
	e.AddColumn("a", 4, 11)
	_, err = e.SetBase("ghost", 0, 12)
	mustErr(t, err, ErrNoColumn, "missing column")
	_, err = e.AddColumn("a", 0, 12)
	mustErr(t, err, ErrDuplicate, "duplicate column")
	e.AddColumn("b", 0, 13)
	_, err = e.RemoveEdge("a", "b", 14)
	mustErr(t, err, ErrNoEdge, "missing edge")
	_, err = e.AddEdge("a", "a", Copy, 14)
	mustErr(t, err, ErrInvalid, "self loop")
	_, err = e.AddEdge("a", "b", Kind(99), 14)
	mustErr(t, err, ErrInvalid, "bad kind")

	// 成环拒绝。
	e.AddColumn("c", 0, 15)
	e.AddEdge("a", "b", Copy, 16)
	e.AddEdge("b", "c", Copy, 17)
	_, err = e.AddEdge("c", "a", Copy, 18)
	mustErr(t, err, ErrCycle, "cycle")

	// 入边 16 超限：同时也会成环，但先报超限。
	e.AddColumn("z", 0, 19)
	for i := 0; i < 16; i++ {
		src := fmt.Sprintf("i%02d", i)
		if _, gerr := e.AddColumn(src, 0, int64(20+i)); gerr != nil {
			t.Fatalf("add src %s: %v", src, gerr)
		}
	}
	for i := 0; i < 16; i++ {
		src := fmt.Sprintf("i%02d", i)
		ch, gerr := e.AddEdge(src, "z", Copy, int64(40+i))
		if gerr != nil {
			t.Fatalf("fill in-edge %d: %v", i, gerr)
		}
		if len(ch) != 0 {
			t.Fatalf("fill edge %d changed %v", i, ch)
		}
	}
	e.AddColumn("s", 0, 80)
	if _, err := e.AddEdge("z", "s", Copy, 81); err != nil {
		t.Fatal(err)
	}
	_, err = e.AddEdge("s", "z", Copy, 82)
	mustErr(t, err, ErrLimit, "in-edge limit beats cycle")

	// 被拒操作不推进时钟、不触发 cap 失效。
	e2 := NewEngine(5)
	e2.AddColumn("x", 4, 1)
	e2.Declass("x", 1, 10, 2)
	if _, err := e2.SetBase("x", 2, 0); !errors.Is(err, ErrClock) {
		t.Fatalf("want clock reject, got %v", err)
	}
	if v, _ := e2.Eff("x"); v != 1 {
		t.Fatalf("rejected op must not expire cap: eff=%d", v)
	}
	// 时钟仍是 2：now=2 重复设置不回退，cap 在 until=10 前仍生效。
	if v, err := e2.Eff("x"); err != nil || v != 1 {
		t.Fatalf("state after rejects: %d %v", v, err)
	}
	ch, _ := e2.Tick(9)
	mustChange(t, ch, nil, "still capped at 9")
	ch, _ = e2.Tick(10)
	mustChange(t, ch, []ChangedItem{{"x", 1, 4}}, "expires at 10")
}

// Nmax 拒绝新增第 Nmax+1 列。
func TestColumnLimit(t *testing.T) {
	e := NewEngine(2)
	if _, err := e.AddColumn("a", 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddColumn("b", 0, 2); err != nil {
		t.Fatal(err)
	}
	_, err := e.AddColumn("c", 0, 3)
	mustErr(t, err, ErrLimit, "nmax")
}

// naiveModel 是完全按定义从零全量重算的参考实现。
type naiveModel struct {
	nmax    int
	lastNow int64
	cols    map[string]*naiveCol
	edges   map[string]map[string]Kind // src -> dst -> kind
}

type naiveCol struct {
	base   int
	floor  int
	capL   int
	capU   int64
	hasCap bool
}

type naiveOp struct {
	name              string
	col, src, dst     string
	base, level, kind int
	until, now        int64
}

func newNaive(nmax int) *naiveModel {
	return &naiveModel{nmax: nmax, cols: map[string]*naiveCol{}, edges: map[string]map[string]Kind{}}
}

func (m *naiveModel) inEdges(dst string) []naiveOp {
	var ops []naiveOp
	for src, outs := range m.edges {
		if k, ok := outs[dst]; ok {
			ops = append(ops, naiveOp{name: "edge", src: src, dst: dst, kind: int(k)})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].src < ops[j].src })
	return ops
}

func (m *naiveModel) outDegree(col string) int { return len(m.edges[col]) }

func (m *naiveModel) raw(c *naiveCol, name string, eff map[string]int) int {
	raw := c.base
	for _, ed := range m.inEdges(name) {
		if v := Kind(ed.kind).Apply(eff[ed.src]); v > raw {
			raw = v
		}
	}
	return raw
}

func (m *naiveModel) effOne(name string, eff map[string]int) int {
	c := m.cols[name]
	raw := m.raw(c, name, eff)
	effV := raw
	if c.hasCap && c.capL < effV {
		effV = c.capL
	}
	if c.floor > effV {
		effV = c.floor
	}
	return effV
}

// fullRecompute 按拓扑序从零重算全部 eff。
func (m *naiveModel) fullRecompute() map[string]int {
	indeg := map[string]int{}
	for src, outs := range m.edges {
		if _, ok := m.cols[src]; !ok {
			continue
		}
		for dst := range outs {
			if _, ok := m.cols[dst]; ok {
				indeg[dst]++
			}
		}
	}
	var queue []string
	for name := range m.cols {
		if indeg[name] == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)
	eff := map[string]int{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		eff[cur] = m.effOne(cur, eff)
		for dst := range m.edges[cur] {
			if _, ok := m.cols[dst]; !ok {
				continue
			}
			indeg[dst]--
			if indeg[dst] == 0 {
				queue = append(queue, dst)
			}
		}
	}
	return eff
}

func (m *naiveModel) reaches(from, to string) bool {
	seen := map[string]bool{from: true}
	frontier := []string{from}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for dst := range m.edges[cur] {
			if dst == to {
				return true
			}
			if !seen[dst] {
				seen[dst] = true
				frontier = append(frontier, dst)
			}
		}
	}
	return false
}

func validNaiveName(s string) bool { return len(s) >= 1 && len(s) <= 128 }

// validate 返回错误类别字符串；"" 表示通过。
func (m *naiveModel) validate(op naiveOp) string {
	switch op.name {
	case "addcol":
		if !validNaiveName(op.col) || op.base < 0 || op.base > 4 || op.now < 0 || op.now > 1e12 {
			return "invalid"
		}
		if op.now < m.lastNow {
			return "clock"
		}
		if _, ok := m.cols[op.col]; ok {
			return "duplicate"
		}
		if len(m.cols) >= m.nmax {
			return "limit"
		}
	case "setbase", "raise":
		if !validNaiveName(op.col) || op.base < 0 || op.base > 4 || op.now < 0 || op.now > 1e12 {
			return "invalid"
		}
		if op.now < m.lastNow {
			return "clock"
		}
		if _, ok := m.cols[op.col]; !ok {
			return "nocolumn"
		}
	case "declass":
		if !validNaiveName(op.col) || op.level < 0 || op.level > 4 ||
			op.now < 0 || op.now > 1e12 || op.until < 0 || op.until > 1e12 || op.until <= op.now {
			return "invalid"
		}
		if op.now < m.lastNow {
			return "clock"
		}
		if _, ok := m.cols[op.col]; !ok {
			return "nocolumn"
		}
	case "addedge":
		if !validNaiveName(op.src) || !validNaiveName(op.dst) || op.kind < 0 || op.kind > 3 ||
			op.now < 0 || op.now > 1e12 || op.src == op.dst {
			return "invalid"
		}
		if op.now < m.lastNow {
			return "clock"
		}
		if _, ok := m.cols[op.src]; !ok {
			return "nocolumn"
		}
		if _, ok := m.cols[op.dst]; !ok {
			return "nocolumn"
		}
		if _, ok := m.edges[op.src][op.dst]; ok {
			return "duplicate"
		}
		indeg := 0
		for _, outs := range m.edges {
			if _, ok := outs[op.dst]; ok {
				indeg++
			}
		}
		if indeg >= 16 {
			return "limit"
		}
		if m.reaches(op.dst, op.src) {
			return "cycle"
		}
	case "removeedge":
		if !validNaiveName(op.src) || !validNaiveName(op.dst) ||
			op.now < 0 || op.now > 1e12 || op.src == op.dst {
			return "invalid"
		}
		if op.now < m.lastNow {
			return "clock"
		}
		if _, ok := m.cols[op.src]; !ok {
			return "nocolumn"
		}
		if _, ok := m.cols[op.dst]; !ok {
			return "nocolumn"
		}
		if _, ok := m.edges[op.src][op.dst]; !ok {
			return "noedge"
		}
	case "tick":
		if op.now < 0 || op.now > 1e12 {
			return "invalid"
		}
		if op.now < m.lastNow {
			return "clock"
		}
	}
	return ""
}

// apply 在通过校验后执行，返回过期列数与 Changed。
func (m *naiveModel) apply(op naiveOp) (int, []ChangedItem) {
	old := m.fullRecompute()
	var expired []string
	for name, c := range m.cols {
		if c.hasCap && c.capU <= op.now {
			c.hasCap = false
			expired = append(expired, name)
		}
	}
	switch op.name {
	case "addcol":
		m.cols[op.col] = &naiveCol{base: op.base}
	case "setbase":
		m.cols[op.col].base = op.base
	case "raise":
		m.cols[op.col].floor = op.base
	case "declass":
		m.cols[op.col].capL = op.level
		m.cols[op.col].capU = op.until
		m.cols[op.col].hasCap = true
	case "addedge":
		if m.edges[op.src] == nil {
			m.edges[op.src] = map[string]Kind{}
		}
		m.edges[op.src][op.dst] = Kind(op.kind)
	case "removeedge":
		delete(m.edges[op.src], op.dst)
	case "tick":
	}
	m.lastNow = op.now
	next := m.fullRecompute()
	var changed []ChangedItem
	for name := range next {
		if op.name == "addcol" && name == op.col {
			continue
		}
		if old[name] != next[name] {
			changed = append(changed, ChangedItem{Col: name, Old: old[name], New: next[name]})
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Col < changed[j].Col })
	return len(expired), changed
}

func (m *naiveModel) why(name string) WhySource {
	c := m.cols[name]
	rawV := c.base
	bestSrc, best := "", -1
	for _, ed := range m.inEdges(name) {
		v := Kind(ed.kind).Apply(m.fullRecompute()[ed.src])
		if v > best {
			best, bestSrc = v, ed.src
		}
		if v > rawV {
			rawV = v
		}
	}
	capped := rawV
	if c.hasCap && c.capL < capped {
		capped = c.capL
	}
	switch {
	case c.floor > capped:
		return WhySource{Kind: "Floor"}
	case c.hasCap && c.capL < rawV:
		return WhySource{Kind: "Cap"}
	case c.base >= best:
		return WhySource{Kind: "Base"}
	default:
		return WhySource{Kind: "Edge", Src: bestSrc}
	}
}

func errClass(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrClock):
		return "clock"
	case errors.Is(err, ErrNoColumn):
		return "nocolumn"
	case errors.Is(err, ErrDuplicate):
		return "duplicate"
	case errors.Is(err, ErrNoEdge):
		return "noedge"
	case errors.Is(err, ErrLimit):
		return "limit"
	case errors.Is(err, ErrCycle):
		return "cycle"
	default:
		return ""
	}
}

var _ = strings.Compare

func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	var logLines []string
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(1000 + g)))
		nmax := 1 + rng.Intn(12)
		eng := NewEngine(nmax)
		nav := newNaive(nmax)
		ops := 40 + rng.Intn(80)
		var now int64 = 1
		var existing []string

		logf := func(format string, args ...any) {
			logLines = append(logLines, fmt.Sprintf("g%04d "+format, append([]any{g}, args...)...))
		}

		for step := 0; step < ops; step++ {
			op := genOp(rng, existing, now)
			preEvals := eng.Evals()
			engCh, engErr := dispatch(eng, op)
			navClass := nav.validate(op)

			logf("step=%d op=%s", step, opString(op))
			logf("  -> engine_err=%v changed=%v", errClass(engErr), engCh)

			if (engErr == nil) != (navClass == "") {
				t.Fatalf("group %d step %d: reject mismatch engine=%v naive=%s\n%s\n%v",
					g, step, engErr, navClass, opString(op), dumpTail(logLines))
			}
			if engErr != nil {
				if got := errClass(engErr); got != navClass {
					t.Fatalf("group %d step %d: error class engine=%s naive=%s\n%s",
						g, step, got, navClass, opString(op))
				}
				// 被拒后双方状态仍需一致：随机挑列比对 eff/Why。
				assertStatesEqual(t, g, step, eng, nav, existing, logLines)
				continue
			}

			expired, navCh := nav.apply(op)
			logf("  <-  naive_changed=%v expired=%d", navCh, expired)
			if len(engCh) != len(navCh) {
				t.Fatalf("group %d step %d: Changed len engine=%d naive=%d\nengine=%v\nnaive=%v\n%s",
					g, step, len(engCh), len(navCh), engCh, navCh, opString(op))
			}
			for i := range navCh {
				if engCh[i] != navCh[i] {
					t.Fatalf("group %d step %d item %d: engine=%v naive=%v\n%s",
						g, step, i, engCh[i], navCh[i], opString(op))
				}
			}

			// evals 上界：S + Σ(变化列出边数)。
			spent := eng.Evals() - preEvals
			sumOut := expired
			if op.name != "tick" {
				sumOut++
			}
			for _, item := range navCh {
				sumOut += nav.outDegree(item.Col)
			}
			if spent > int64(sumOut) {
				t.Fatalf("group %d step %d: evals=%d > bound=%d\n%s",
					g, step, spent, sumOut, opString(op))
			}

			assertStatesEqual(t, g, step, eng, nav, existing, logLines)

			// WhatIf 与紧随其后、不触发失效的 SetBase 必须逐项一致。
			if op.name == "addcol" {
				existing = append(existing, op.col)
			}
			if rng.Intn(4) == 0 && len(existing) > 0 {
				col := existing[rng.Intn(len(existing))]
				base := rng.Intn(5)
				hyp, hypErr := eng.WhatIf(col, base)
				if hypErr != nil {
					t.Fatalf("WhatIf unexpected err %v", hypErr)
				}
				// 保证不触发上限失效：now 不变即可（重放同 now）。
				real, realErr := eng.SetBase(col, base, nav.lastNow)
				if realErr != nil {
					t.Fatalf("SetBase after WhatIf err %v", realErr)
				}
				logf("whatif col=%s base=%d -> %v ; setbase -> %v", col, base, hyp, real)
				if len(hyp) != len(real) {
					t.Fatalf("WhatIf/SetBase len %d vs %d", len(hyp), len(real))
				}
				for i := range hyp {
					if hyp[i] != real[i] {
						t.Fatalf("WhatIf/SetBase item %d: %v vs %v", i, hyp[i], real[i])
					}
				}
				// 让 naive 与这次真实 SetBase 对齐。
				syncOp := naiveOp{name: "setbase", col: col, base: base, now: nav.lastNow}
				nav.validate(syncOp)
				_, navCh2 := nav.apply(syncOp)
				if len(real) != len(navCh2) {
					t.Fatalf("post-WhatIf SetBase vs naive: %v vs %v", real, navCh2)
				}
				for i := range real {
					if real[i] != navCh2[i] {
						t.Fatalf("post-WhatIf item %d: %v vs %v", i, real[i], navCh2[i])
					}
				}
				assertStatesEqual(t, g, step, eng, nav, existing, logLines)
			}
			now = nav.lastNow
		}
	}
	// 打印前 60 行判定日志，其余随 -v 可扩展。
	limit := 60
	if len(logLines) < limit {
		limit = len(logLines)
	}
	for _, line := range logLines[:limit] {
		t.Log(line)
	}
	t.Logf("differential run complete: %d groups", groups)
}

func dispatch(e *Engine, op naiveOp) ([]ChangedItem, error) {
	switch op.name {
	case "addcol":
		return e.AddColumn(op.col, op.base, op.now)
	case "setbase":
		return e.SetBase(op.col, op.base, op.now)
	case "raise":
		return e.Raise(op.col, op.base, op.now)
	case "declass":
		return e.Declass(op.col, op.level, op.until, op.now)
	case "addedge":
		return e.AddEdge(op.src, op.dst, Kind(op.kind), op.now)
	case "removeedge":
		return e.RemoveEdge(op.src, op.dst, op.now)
	case "tick":
		return e.Tick(op.now)
	}
	return nil, fmt.Errorf("unknown op %s", op.name)
}

func genOp(rng *rand.Rand, existing []string, now int64) naiveOp {
	// 列很少时优先建列。
	if len(existing) == 0 || rng.Intn(4) == 0 {
		name := fmt.Sprintf("c%03d", rng.Intn(14))
		return naiveOp{name: "addcol", col: name, base: rng.Intn(5), now: now + int64(rng.Intn(3))}
	}
	col := existing[rng.Intn(len(existing))]
	switch rng.Intn(10) {
	case 0:
		return naiveOp{name: "addcol", col: fmt.Sprintf("c%03d", rng.Intn(14)),
			base: rng.Intn(5), now: now + int64(rng.Intn(3))}
	case 1:
		return naiveOp{name: "setbase", col: col, base: rng.Intn(5), now: now + int64(rng.Intn(3))}
	case 2:
		return naiveOp{name: "raise", col: col, base: rng.Intn(5), now: now + int64(rng.Intn(3))}
	case 3:
		return naiveOp{name: "declass", col: col, level: rng.Intn(5),
			until: now + int64(1+rng.Intn(6)), now: now + int64(rng.Intn(3))}
	case 4, 5:
		src := existing[rng.Intn(len(existing))]
		dst := existing[rng.Intn(len(existing))]
		return naiveOp{name: "addedge", src: src, dst: dst, kind: rng.Intn(4),
			now: now + int64(rng.Intn(3))}
	case 6:
		src := existing[rng.Intn(len(existing))]
		dst := existing[rng.Intn(len(existing))]
		return naiveOp{name: "removeedge", src: src, dst: dst, now: now + int64(rng.Intn(3))}
	case 7:
		// 偶发非法参数，覆盖拒绝路径。
		bad := []string{"", strings.Repeat("z", 129), "x"}
		return naiveOp{name: "setbase", col: bad[rng.Intn(len(bad))],
			base: rng.Intn(7) - 1, now: now + int64(rng.Intn(3))}
	case 8:
		// 偶发时钟回退。
		back := int64(1)
		if now > 2 {
			back = now - int64(rng.Intn(2))
		}
		return naiveOp{name: "tick", now: back}
	default:
		return naiveOp{name: "tick", now: now + int64(rng.Intn(4))}
	}
}

func opString(op naiveOp) string {
	return fmt.Sprintf("{name:%s col:%q src:%q dst:%q base:%d level:%d kind:%d until:%d now:%d}",
		op.name, op.col, op.src, op.dst, op.base, op.level, op.kind, op.until, op.now)
}

func assertStatesEqual(t *testing.T, g, step int, e *Engine, m *naiveModel, cols []string, log []string) {
	t.Helper()
	want := m.fullRecompute()
	checked := 0
	for name, w := range want {
		got, err := e.Eff(name)
		if err != nil || got != w {
			t.Fatalf("group %d step %d eff %s: engine=%d(%v) naive=%d\n%s",
				g, step, name, got, err, w, dumpTail(log))
		}
		if checked < 3 {
			gw, gerr := e.Why(name)
			nw := m.why(name)
			if gerr != nil || gw != nw {
				t.Fatalf("group %d step %d Why %s: engine=%+v(%v) naive=%+v",
					g, step, name, gw, gerr, nw)
			}
		}
		checked++
	}
}

func dumpTail(lines []string) string {
	start := len(lines) - 20
	if start < 0 {
		start = 0
	}
	out := ""
	for _, line := range lines[start:] {
		out += line + "\n"
	}
	return out
}

func TestConcurrentOperations(t *testing.T) {
	e := NewEngine(64)
	for i := 0; i < 16; i++ {
		name := string(rune('a' + i))
		if _, err := e.AddColumn(name, i%5, 0); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				now := int64(k + 1)
				src := string(rune('a' + (k+id)%16))
				dst := string(rune('a' + (k*3+id+1)%16))
				_, _ = e.AddEdge(src, dst, Kind(k%4), now)
				_, _ = e.SetBase(src, k%5, now)
				if k%5 == 0 {
					_, _ = e.Raise(src, k%4, now)
				}
				if k%7 == 0 {
					_, _ = e.Declass(dst, k%3, now+10, now)
				}
				_, _ = e.RemoveEdge(src, dst, now)
				_, _ = e.Tick(now)
				_, _ = e.Eff(src)
				_, _ = e.Why(dst)
				_, _ = e.WhatIf(src, k%5)
			}
		}(w)
	}
	wg.Wait()
	// 最终每列 eff 必须在合法范围且能稳定重复读取。
	for i := 0; i < 16; i++ {
		name := string(rune('a' + i))
		v, err := e.Eff(name)
		if err != nil || v < 0 || v > 4 {
			t.Fatalf("final eff %s = %d (%v)", name, v, err)
		}
		if _, err := e.Why(name); err != nil {
			t.Fatalf("Why %s: %v", name, err)
		}
	}
}
