package inline

import (
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

// nNode 是朴素参照模型中的函数体节点。
type nNode struct {
	id       string
	callee   string
	hotness  float64
	pos      int
	parent   *nNode
	examined bool
	inlined  bool
	children []*nNode
}

// naiveDecide 是独立的朴素参照实现：不做任何剪枝与增量维护，
// 每轮重新扫描整棵展开树找出下一个应考察的调用点，
// 沿父指针重建展开链，严格按定义逐层展开。
func naiveDecide(prog *Program, cfg Config) *Report {
	bodies := map[string]body{}
	rep := &Report{}
	for _, name := range prog.order {
		fr := naiveRoot(prog, cfg, name, bodies)
		bodies[name] = body{size: fr.FinalSize, sites: fr.surviving}
		rep.Functions = append(rep.Functions, fr)
	}
	return rep
}

func naiveBodyOf(prog *Program, bodies map[string]body, name string) body {
	if b, ok := bodies[name]; ok {
		return b
	}
	f, _ := prog.Lookup(name)
	sites := make([]bodySite, len(f.CallSites))
	for i, cs := range f.CallSites {
		sites[i] = bodySite{callee: cs.Callee, hotness: cs.Hotness}
	}
	return body{size: f.Size, sites: sites}
}

func naiveLess(a, b *nNode) bool {
	if a.hotness != b.hotness {
		return a.hotness > b.hotness
	}
	if a.pos != b.pos {
		return a.pos < b.pos
	}
	return a.id < b.id
}

func naiveRoot(prog *Program, cfg Config, root string, bodies map[string]body) FunctionReport {
	f, _ := prog.Lookup(root)
	fr := FunctionReport{Name: root, InitialSize: f.Size, DeepestPath: []string{root}}
	size, growth := f.Size, int64(0)
	var roots []*nNode
	for i, cs := range f.CallSites {
		roots = append(roots, &nNode{
			id: root + "#" + strconv.Itoa(i), callee: cs.Callee, hotness: cs.Hotness, pos: i,
		})
	}
	for {
		var cands []*nNode
		var collect func(n *nNode)
		collect = func(n *nNode) {
			if !n.examined {
				cands = append(cands, n)
				return
			}
			for _, c := range n.children {
				collect(c)
			}
		}
		for _, r := range roots {
			collect(r)
		}
		if len(cands) == 0 {
			break
		}
		best := cands[0]
		for _, c := range cands[1:] {
			if naiveLess(c, best) {
				best = c
			}
		}
		best.examined = true
		var rev []string
		for p := best.parent; p != nil; p = p.parent {
			rev = append(rev, p.callee)
		}
		path := []string{root}
		for i := len(rev) - 1; i >= 0; i-- {
			path = append(path, rev[i])
		}
		reason := RejectNone
		var cb body
		cf, ok := prog.funcs[best.callee]
		if !ok {
			reason = RejectUndefined
		} else if cf.Marks.NoInline {
			reason = RejectNoInline
		} else if best.callee == path[len(path)-1] {
			reason = RejectDirectRecursion
		} else if cf.Marks.NonInlinableStructure {
			reason = RejectNonInlinableStructure
		} else {
			cnt := 0
			for _, g := range path {
				if g == best.callee {
					cnt++
				}
			}
			if cnt >= cfg.MaxChainRepeat {
				reason = RejectChainLimit
			} else {
				cb = naiveBodyOf(prog, bodies, best.callee)
				delta := cb.size - cfg.CallOverhead
				if !cf.Marks.AlwaysInline &&
					((growth+delta)*cfg.GrowthDen > cfg.GrowthNum*f.Size || size+delta > cfg.MaxSize) {
					reason = RejectBudget
				}
			}
		}
		if reason != RejectNone {
			fr.Decisions = append(fr.Decisions, CallDecision{
				ID: best.id, Callee: best.callee, Hotness: best.hotness, Path: path, Reason: reason,
			})
			continue
		}
		delta := cb.size - cfg.CallOverhead
		size += delta
		growth += delta
		best.inlined = true
		fr.Decisions = append(fr.Decisions, CallDecision{
			ID: best.id, Callee: best.callee, Hotness: best.hotness, Path: path, Inlined: true,
		})
		p2 := append(append([]string{}, path...), best.callee)
		if len(p2) > len(fr.DeepestPath) {
			fr.DeepestPath = p2
		}
		for k, s := range cb.sites {
			best.children = append(best.children, &nNode{
				id:      best.id + "/" + best.callee + "#" + strconv.Itoa(k),
				callee:  s.callee,
				hotness: best.hotness * s.hotness,
				pos:     k,
				parent:  best,
			})
		}
	}
	fr.FinalSize = size
	var surv []bodySite
	var walk func(n *nNode)
	walk = func(n *nNode) {
		if !n.inlined {
			surv = append(surv, bodySite{callee: n.callee, hotness: n.hotness})
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	fr.surviving = surv
	return fr
}

func randomProgram(rng *rand.Rand) ([]Function, Config) {
	n := 3 + rng.Intn(2)
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("F%d", i)
	}
	funcs := make([]Function, n)
	for i := range funcs {
		f := Function{Name: names[i], Size: int64(1 + rng.Intn(120))}
		switch rng.Intn(10) {
		case 0:
			f.Marks.NoInline = true
		case 1:
			f.Marks.AlwaysInline = true
		case 2:
			f.Marks.NonInlinableStructure = true
		}
		for j := 0; j < rng.Intn(3); j++ {
			callee := names[rng.Intn(n)]
			if rng.Intn(10) == 0 {
				callee = "UNDEF"
			}
			f.CallSites = append(f.CallSites, CallSite{
				Callee:  callee,
				Hotness: float64(rng.Intn(21)) / 20,
			})
		}
		funcs[i] = f
	}
	growths := [][2]int64{{0, 1}, {1, 2}, {1, 1}, {2, 1}, {5, 1}}
	g := growths[rng.Intn(len(growths))]
	maxes := []int64{50, 200, 100000}
	cfg := Config{
		CallOverhead:   int64(rng.Intn(15)),
		GrowthNum:      g[0],
		GrowthDen:      g[1],
		MaxSize:        maxes[rng.Intn(len(maxes))],
		MaxChainRepeat: 1 + rng.Intn(2),
	}
	return funcs, cfg
}

func reportsEqual(a, b *Report) bool {
	if len(a.Functions) != len(b.Functions) {
		return false
	}
	for i := range a.Functions {
		x, y := a.Functions[i], b.Functions[i]
		if x.Name != y.Name || x.InitialSize != y.InitialSize || x.FinalSize != y.FinalSize {
			return false
		}
		if !reflect.DeepEqual(x.DeepestPath, y.DeepestPath) {
			return false
		}
		if !reflect.DeepEqual(x.Decisions, y.Decisions) {
			return false
		}
		if !reflect.DeepEqual(x.surviving, y.surviving) {
			return false
		}
	}
	return true
}

// 随机调用图上，优化实现与朴素参照模型的决策与最终尺寸必须完全一致。
func TestNaiveModelAgreement(t *testing.T) {
	rng := rand.New(rand.NewSource(20241006))
	for iter := 0; iter < 200; iter++ {
		funcs, cfg := randomProgram(rng)
		reg := NewRegistry()
		for _, f := range funcs {
			if err := reg.Add(f); err != nil {
				t.Fatal(err)
			}
		}
		sess := reg.Begin()
		got, err := Decide(sess, cfg)
		if err != nil {
			sess.Close()
			t.Fatalf("iter %d: Decide: %v", iter, err)
		}
		want := naiveDecide(sess.Program(), cfg)
		sess.Close()
		t.Logf("iter %d 输入: %+v", iter, funcs)
		t.Logf("iter %d 配置: %+v", iter, cfg)
		t.Logf("iter %d 输出:\n%s", iter, got)
		if !reportsEqual(got, want) {
			t.Fatalf("iter %d 与朴素模型不一致\n输入: %+v\n配置: %+v\n引擎:\n%s\n朴素:\n%s",
				iter, funcs, cfg, got, want)
		}
	}
}

// 判定工作量（预算判定次数、展开链扫描步数）不随无关函数数量变化。
func TestStatsIndependentOfUnrelatedFunctions(t *testing.T) {
	base := []Function{
		{Name: "F", Size: 10, CallSites: []CallSite{{Callee: "A", Hotness: 1}}},
		{Name: "A", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
	}
	cfg := Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 1 << 40, MaxChainRepeat: 3}
	run := func(extra int) *Report {
		reg := NewRegistry()
		for _, f := range base {
			if err := reg.Add(f); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < extra; i++ {
			if err := reg.Add(Function{Name: fmt.Sprintf("u%d", i), Size: 1}); err != nil {
				t.Fatal(err)
			}
		}
		sess := reg.Begin()
		defer sess.Close()
		rep, err := Decide(sess, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	r1 := run(0)
	r2 := run(5000)
	if r1.Stats != r2.Stats {
		t.Fatalf("无关函数数量改变了判定工作量: %+v vs %+v", r1.Stats, r2.Stats)
	}
	f1, _ := r1.Func("F")
	f2, _ := r2.Func("F")
	if !reflect.DeepEqual(f1, f2) {
		t.Fatalf("F 的报告受无关函数影响")
	}
}

// 预算判定开销与全程序函数总数无关：增加大量无关函数后耗时不变。
// 验证: go test ./inline -bench BudgetCheckScaling -benchtime 100x
func BenchmarkBudgetCheckScaling(b *testing.B) {
	for _, k := range []int{10, 1000, 100000} {
		b.Run(fmt.Sprintf("unrelated=%d", k), func(b *testing.B) {
			reg := NewRegistry()
			reg.Add(Function{Name: "root", Size: 100, CallSites: []CallSite{{Callee: "leaf", Hotness: 1}}})
			reg.Add(Function{Name: "leaf", Size: 50})
			for i := 0; i < k; i++ {
				reg.Add(Function{Name: fmt.Sprintf("u%d", i), Size: 1})
			}
			sess := reg.Begin()
			defer sess.Close()
			cfg := Config{CallOverhead: 5, GrowthNum: 1, GrowthDen: 1, MaxSize: 1 << 40, MaxChainRepeat: 2}
			prog := sess.Program()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e := &engine{prog: prog, cfg: cfg, bodies: map[string]body{}}
				e.decideRoot("root")
			}
		})
	}
}

// 展开链判定开销与链外函数数量无关。
// 验证: go test ./inline -bench ChainScanScaling -benchtime 100x
func BenchmarkChainScanScaling(b *testing.B) {
	for _, k := range []int{10, 1000, 100000} {
		b.Run(fmt.Sprintf("unrelated=%d", k), func(b *testing.B) {
			reg := NewRegistry()
			reg.Add(Function{Name: "F", Size: 10, CallSites: []CallSite{{Callee: "A", Hotness: 1}}})
			reg.Add(Function{Name: "A", Size: 10, CallSites: []CallSite{{Callee: "F", Hotness: 1}}})
			for i := 0; i < k; i++ {
				reg.Add(Function{Name: fmt.Sprintf("u%d", i), Size: 1})
			}
			sess := reg.Begin()
			defer sess.Close()
			cfg := Config{CallOverhead: 0, GrowthNum: 1000, GrowthDen: 1, MaxSize: 1 << 40, MaxChainRepeat: 3}
			prog := sess.Program()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e := &engine{prog: prog, cfg: cfg, bodies: map[string]body{}}
				e.decideRoot("F")
			}
		})
	}
}
