package viewgraph

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustRegister(t *testing.T, g *Graph, base bool, name string, deps []string, fn ComputeFn) {
	t.Helper()
	var err error
	if base {
		err = g.RegisterBase(name)
	} else {
		err = g.RegisterView(name, deps, fn)
	}
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
}

func intSum() ComputeFn {
	return func(deps map[string]any) (any, error) {
		sum := 0
		keys := make([]string, 0, len(deps))
		for k := range deps {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, ok := deps[k].(int)
			if !ok {
				return nil, fmt.Errorf("dependency %q is not int: %#v", k, deps[k])
			}
			sum += v
		}
		return sum, nil
	}
}

func assertValue(t *testing.T, g *Graph, name string, want any) {
	t.Helper()
	got, err := g.Get(name)
	if err != nil {
		t.Fatalf("get %q: %v", name, err)
	}
	if got != want {
		t.Fatalf("view %q = %#v, want %#v", name, got, want)
	}
}

func logState(t *testing.T, g *Graph, phase string) {
	t.Helper()
	names := g.RegistrationOrder()
	topo, err := g.TopoOrder()
	if err != nil {
		topo = []string{"<invalid: " + err.Error() + ">"}
	}
	t.Logf("[%s] registration order=%v topo order=%v", phase, names, topo)
	for _, name := range names {
		val, err := g.Get(name)
		dirty, derr := g.IsDirty(name)
		switch {
		case derr != nil:
			t.Logf("[%s]   view=%q err=%v", phase, name, derr)
		case errors.Is(err, ErrViewNotSet):
			t.Logf("[%s]   view=%q dirty=%v value=<not set>", phase, name, dirty)
		case err != nil:
			t.Logf("[%s]   view=%q err=%v dirty=%v", phase, name, err, dirty)
		default:
			t.Logf("[%s]   view=%q dirty=%v value=%v", phase, name, dirty, val)
		}
	}
}

// 前向引用：先注册依赖尚不存在的视图，后补基视图与中间视图，仍能按拓扑序重算。
func TestForwardReference(t *testing.T) {
	g := New()

	mustRegister(t, g, false, "c", []string{"b"}, intSum())
	mustRegister(t, g, false, "b", []string{"a"}, intSum())
	mustRegister(t, g, true, "a", nil, nil)

	topo, err := g.TopoOrder()
	if err != nil {
		t.Fatalf("topo after satisfying forward refs: %v", err)
	}
	if fmt.Sprint(topo) != "[a b c]" {
		t.Fatalf("topo order = %v, want [a b c] (deps first, ties by registration)", topo)
	}
	t.Logf("判定依据: 前向引用全部补齐后 TopoOrder=%v", topo)

	if err := g.Set("a", 3); err != nil {
		t.Fatalf("set a: %v", err)
	}
	logState(t, g, "after Set(a=3)")

	rep, err := g.Recompute()
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if fmt.Sprint(rep.Evaluated) != "[b c]" {
		t.Fatalf("evaluated = %v, want [b c]", rep.Evaluated)
	}
	logState(t, g, "after Recompute")

	assertValue(t, g, "b", 3)
	assertValue(t, g, "c", 3)
	if err := g.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// 级联失效：修改一个基视图会脏化整个下游传递闭包，重算后逐级恢复一致。
func TestCascadingInvalidation(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)
	mustRegister(t, g, true, "x", nil, nil)
	mustRegister(t, g, false, "b", []string{"a"}, intSum())
	mustRegister(t, g, false, "c", []string{"a", "b", "x"}, intSum())
	mustRegister(t, g, false, "d", []string{"b", "c"}, intSum())

	if err := g.Set("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := g.Set("x", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Recompute(); err != nil {
		t.Fatal(err)
	}
	logState(t, g, "initial round a=1 x=10")
	assertValue(t, g, "d", 1+(1+1+10))

	if err := g.Set("a", 2); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		dirty, err := g.IsDirty(name)
		if err != nil || !dirty {
			t.Fatalf("%s dirty=%v err=%v, want dirty (transitive closure of a)", name, dirty, err)
		}
	}
	if dirty, _ := g.IsDirty("x"); dirty {
		t.Fatal("x must stay clean: it is not downstream of a")
	}
	t.Log("判定依据: Set(a=2) 后脏标记=[a b c d]（下游传递闭包），x 保持干净")
	logState(t, g, "after Set(a=2) before Recompute")

	rep, err := g.Recompute()
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if fmt.Sprint(rep.Evaluated) != "[b c d]" {
		t.Fatalf("evaluated = %v, want exactly [b c d] in topo order", rep.Evaluated)
	}
	if len(rep.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none", rep.Skipped)
	}
	logState(t, g, "after Recompute a=2")

	assertValue(t, g, "b", 2)
	assertValue(t, g, "c", 2+2+10)
	assertValue(t, g, "d", 2+(2+2+10))
	if err := g.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}

	rep2, err := g.Recompute()
	if err != nil || len(rep2.Evaluated) != 0 {
		t.Fatalf("idle recompute = %+v, %v; want no evaluations", rep2, err)
	}
	t.Log("判定依据: 无脏视图时第二轮 Recompute Evaluated=[]（干净视图不求值）")
}

// 去重：重复依赖名只保留一次；同一轮内多次失效的视图至多求值一次。
func TestDeduplication(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)

	var calls int32
	fan := func(deps map[string]any) (any, error) {
		atomic.AddInt32(&calls, 1)
		return deps["a"].(int) * 2, nil
	}
	mustRegister(t, g, false, "m", []string{"a", "a", "a"}, fan)

	if got := len(g.views["m"].deps); got != 1 {
		t.Fatalf("deps of m = %v, want deduped single dependency", g.views["m"].deps)
	}
	t.Logf("判定依据: m 注册依赖 [a a a] 去重后=%v", g.views["m"].deps)

	if err := g.Set("a", 5); err != nil {
		t.Fatal(err)
	}
	if err := g.Set("a", 5); err != nil {
		t.Fatal(err)
	}
	rep, err := g.Recompute()
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("m computed %d times, want exactly 1", calls)
	}
	if fmt.Sprint(rep.Evaluated) != "[m]" {
		t.Fatalf("evaluated = %v, want [m]", rep.Evaluated)
	}
	assertValue(t, g, "m", 10)
	logState(t, g, "after deduped Recompute")
}

// 环检测：注册阶段允许暂时成环（含前向引用），但任何拓扑使用都必须拒绝。
func TestCycleDetection(t *testing.T) {
	g := New()
	mustRegister(t, g, false, "a", []string{"c"}, intSum())
	mustRegister(t, g, false, "b", []string{"a"}, intSum())
	mustRegister(t, g, false, "c", []string{"b"}, intSum())

	if _, err := g.Recompute(); !errors.Is(err, ErrCycle) {
		t.Fatalf("recompute err = %v, want ErrCycle", err)
	} else {
		t.Logf("判定依据: a->c->b->a 成环，Recompute 返回 %v", err)
	}
	if _, err := g.TopoOrder(); !errors.Is(err, ErrCycle) {
		t.Fatalf("topo err = %v, want ErrCycle", err)
	}
	if err := g.Verify(); !errors.Is(err, ErrCycle) {
		t.Fatalf("verify err = %v, want ErrCycle", err)
	}
	if _, err := g.FullRecompute(); !errors.Is(err, ErrCycle) {
		t.Fatalf("full recompute err = %v, want ErrCycle", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := g.Get(name); !errors.Is(err, ErrViewNotSet) {
			t.Fatalf("get %s after failed recompute: %v, want ErrViewNotSet", name, err)
		}
	}
}

// 重算时依赖仍未注册：前向引用未补齐，必须以可区分原因拒绝且不改状态。
func TestMissingDependencyAtRecompute(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)
	mustRegister(t, g, false, "b", []string{"a", "ghost"}, intSum())

	if err := g.Set("a", 1); err != nil {
		t.Fatal(err)
	}
	_, err := g.Recompute()
	if !errors.Is(err, ErrDependencyNotFound) {
		t.Fatalf("recompute err = %v, want ErrDependencyNotFound", err)
	}
	t.Logf("判定依据: b 依赖未注册的 ghost，Recompute 返回 %v", err)
	if dirty, _ := g.IsDirty("b"); !dirty {
		t.Fatal("b must remain dirty after failed recompute (no state change)")
	}
}

// 注册期校验：空名称、重名、空依赖名、nil 计算函数各自给出可区分原因，且失败不改状态。
func TestRegistrationErrors(t *testing.T) {
	g := New()
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"empty base", func() error { return g.RegisterBase("") }, ErrEmptyName},
		{"empty derived", func() error { return g.RegisterView("", []string{"a"}, intSum()) }, ErrEmptyName},
		{"empty dep", func() error { return g.RegisterView("z", []string{""}, intSum()) }, ErrEmptyName},
		{"nil fn", func() error { return g.RegisterView("n", []string{"a"}, nil) }, ErrNilCompute},
	}
	for _, tc := range cases {
		if err := tc.fn(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	mustRegister(t, g, true, "a", nil, nil)
	if err := g.RegisterBase("a"); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("dup base: err = %v, want ErrDuplicateName", err)
	}
	if err := g.RegisterView("a", nil, intSum()); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("dup cross-kind: err = %v, want ErrDuplicateName", err)
	}
	t.Log("判定依据: 空名/重名/nil 函数分别返回 ErrEmptyName / ErrDuplicateName / ErrNilCompute")
	if got := g.RegistrationOrder(); fmt.Sprint(got) != "[a]" {
		t.Fatalf("failed registrations changed state: order=%v", got)
	}
}

// 读写未注册名、给派生视图设值，原因可区分。
func TestUnregisteredAndBaseOnlyWrites(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)
	mustRegister(t, g, false, "b", []string{"a"}, intSum())

	if _, err := g.Get("nope"); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("get unknown: %v", err)
	}
	if _, err := g.IsDirty("nope"); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("dirty unknown: %v", err)
	}
	if err := g.Set("nope", 1); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("set unknown: %v", err)
	}
	if err := g.Set("b", 1); !errors.Is(err, ErrNotBaseView) {
		t.Fatalf("set derived: %v, want ErrNotBaseView", err)
	}
	t.Log("判定依据: 未注册名读/写→ErrViewNotFound，派生视图设值→ErrNotBaseView")
}

// 基视图未设值时重算必须失败；失败整体回滚，不产生任何中间值或脏位清除。
func TestUnsetBaseFailureIsAtomic(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)
	mustRegister(t, g, false, "b", []string{"a"}, intSum())
	mustRegister(t, g, false, "c", []string{"b"}, intSum())

	_, err := g.Recompute()
	if !errors.Is(err, ErrDependencyNotSet) {
		t.Fatalf("recompute unset base: %v, want ErrDependencyNotSet", err)
	}
	t.Logf("判定依据: 基视图 a 无值，Recompute 返回 %v", err)
	for _, name := range []string{"b", "c"} {
		if dirty, _ := g.IsDirty(name); !dirty {
			t.Fatalf("%s must stay dirty after failed round", name)
		}
		if _, err := g.Get(name); !errors.Is(err, ErrViewNotSet) {
			t.Fatalf("%s must have no committed value after failed round: %v", name, err)
		}
	}

	// 中途求值失败也必须整体回滚：b 先算出，c 失败，b 的新值不得提交。
	mustRegister(t, g, true, "ok", nil, nil)
	if err := g.Set("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := g.Set("ok", 1); err != nil {
		t.Fatal(err)
	}
	flaky := func(deps map[string]any) (any, error) {
		return nil, fmt.Errorf("boom")
	}
	mustRegister(t, g, false, "failview", []string{"ok"}, flaky)
	if _, err := g.Recompute(); err == nil {
		t.Fatal("expected compute error, got nil")
	} else {
		t.Logf("判定依据: 下游求值返回错误(%v)，整轮回滚，已算出的 b/c 不提交", err)
	}
	if _, err := g.Get("b"); !errors.Is(err, ErrViewNotSet) {
		t.Fatalf("b leaked staged value after failed round: %v", err)
	}
}

// 并发：设值+重算进行期间，任意快照必须恰好对应某一完整重算轮次（所有派生
// 视图要么整轮缺席、要么整轮一致），且可由 FullRecompute 全量重算复现。
func TestConcurrentConsistency(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)
	mustRegister(t, g, true, "x", nil, nil)
	mustRegister(t, g, false, "b", []string{"a"}, intSum())
	mustRegister(t, g, false, "c", []string{"a", "b"}, intSum())
	mustRegister(t, g, false, "d", []string{"b", "c", "x"}, intSum())

	if err := g.Set("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := g.Set("x", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Recompute(); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 写线程：不断推进 a 的版本并立即重算。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := g.Set("a", i); err != nil {
				t.Errorf("set: %v", err)
				return
			}
			if _, err := g.Recompute(); err != nil {
				t.Errorf("recompute: %v", err)
				return
			}
		}
	}()

	// 读线程：任何一次快照都必须是“某轮完整重算后”的状态。
	validRounds := map[string]bool{}
	for i := 0; ; i++ {
		select {
		case <-stop:
			i = -1
		default:
		}
		if i < 0 || i >= 400 {
			break
		}
		snap := g.Snapshot()
		// 完整轮次判定：四个派生/基视图要么全部在场，要么视图在设值后、
		// 重算前的窗口期缺席；任何部分派生视图组合都是半轮混合，必须失败。
		_, hasB := snap["b"]
		_, hasC := snap["c"]
		_, hasD := snap["d"]
		if hasB != hasC || hasC != hasD {
			t.Fatalf("half-round snapshot observed: %#v", snap)
		}
		if hasB {
			a := snap["a"].(int)
			if snap["b"] != a || snap["c"] != a+a || snap["d"] != a+(a+a)+100 {
				t.Fatalf("snapshot not equal to any complete round: %#v", snap)
			}
			validRounds[fmt.Sprint(a)] = true
		}
		time.Sleep(time.Microsecond)
	}

	close(stop)
	wg.Wait()
	if len(validRounds) == 0 {
		t.Fatal("never observed a complete round under concurrency")
	}
	t.Logf("判定依据: 并发期间观察到 %d 个不同完整轮次，快照均满足 b=a, c=2a, d=3a+x", len(validRounds))

	// 最终状态必须与全量重算 oracle 完全一致。
	full, err := g.FullRecompute()
	if err != nil {
		t.Fatalf("full recompute: %v", err)
	}
	final := g.Snapshot()
	for name, want := range full {
		if final[name] != want {
			t.Fatalf("view %q = %#v, full recompute says %#v", name, final[name], want)
		}
	}
	if err := g.Verify(); err != nil {
		t.Fatalf("final verify: %v", err)
	}
	logState(t, g, "final concurrent state")
}

// 全量重算对照：多轮增量 Recompute 后，逐视图与 FullRecompute oracle 核对。
func TestIncrementalMatchesFullRecompute(t *testing.T) {
	g := New()
	mustRegister(t, g, true, "a", nil, nil)
	mustRegister(t, g, true, "b0", nil, nil)
	mustRegister(t, g, false, "l1", []string{"a", "b0"}, intSum())
	mustRegister(t, g, false, "l2", []string{"l1", "a"}, intSum())
	mustRegister(t, g, false, "l3", []string{"l1", "l2", "b0"}, intSum())

	g.Set("a", 1)
	g.Set("b0", 2)
	for round := 0; round < 5; round++ {
		g.Set("a", round+10)
		if round%2 == 0 {
			g.Set("b0", round*7)
		}
		rep, err := g.Recompute()
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		full, err := g.FullRecompute()
		if err != nil {
			t.Fatalf("full round %d: %v", round, err)
		}
		snap := g.Snapshot()
		for name, want := range full {
			if snap[name] != want {
				t.Fatalf("round %d view %q = %#v, full recompute = %#v", round, name, snap[name], want)
			}
		}
		if err := g.Verify(); err != nil {
			t.Fatalf("round %d verify: %v", round, err)
		}
		t.Logf("判定依据: 第%d轮增量求值=%v，Snapshot 与 FullRecompute 逐视图一致: %v",
			round, rep.Evaluated, full)
	}
	logState(t, g, "after 5 incremental rounds")
}
