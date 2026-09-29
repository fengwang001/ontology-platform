package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

func kindOf(err error) ErrorKind {
	var ge *GraphError
	if errors.As(err, &ge) {
		return ge.Kind
	}
	return ""
}

func ignore1(v any, err error) error { return err }

func fnPlus(n int) func([]any) (any, error) {
	return func(d []any) (any, error) { return d[0].(int) + n, nil }
}

func fnTimes(n int) func([]any) (any, error) {
	return func(d []any) (any, error) { return d[0].(int) * n, nil }
}

func mustRegister(t *testing.T, g *Graph, v View) {
	t.Helper()
	if err := g.Register(v); err != nil {
		t.Fatalf("register %s: %v", v.Name, err)
	}
	t.Logf("注册 %s 依赖=%v", v.Name, v.Dependencies)
}

func assertValues(t *testing.T, g *Graph, want map[string]int) {
	t.Helper()
	for name, w := range want {
		v, err := g.Get(name)
		if err != nil || v.(int) != w {
			t.Fatalf("%s 应为 %d，得到 %v (err=%v)", name, w, v, err)
		}
	}
}

func logSnapshot(t *testing.T, g *Graph, stage string) {
	t.Helper()
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("%s: snapshot: %v", stage, err)
	}
	names := make([]string, 0, len(snap))
	for name := range snap {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := ""
	for _, name := range names {
		parts += fmt.Sprintf(" %s=%v", name, snap[name])
	}
	t.Logf("[%s] generation=%d 物化值:%s", stage, g.Generation(), parts)
}

// 前向引用：先注册依赖尚未存在的视图，后补齐被依赖者。
func TestForwardReference(t *testing.T) {
	g := NewGraph()

	registered := []string{}
	reg := func(v View) {
		t.Helper()
		if err := g.Register(v); err != nil {
			t.Fatalf("register %s: %v", v.Name, err)
		}
		registered = append(registered, v.Name)
		t.Logf("注册顺序 %d: %s 依赖=%v", len(registered), v.Name, v.Dependencies)
	}

	reg(View{Name: "a"})
	reg(View{Name: "b", Dependencies: []string{"a", "c"}, Fn: func(d []any) (any, error) {
		return d[0].(int) + d[1].(int), nil
	}})
	reg(View{Name: "c", Dependencies: []string{"a"}, Fn: func(d []any) (any, error) {
		return d[0].(int) * 10, nil
	}})

	if err := g.SetBaseAndRecompute("a", 2); err != nil {
		t.Fatalf("set+recompute: %v", err)
	}
	logSnapshot(t, g, "前向引用: 设值 a=2 并重算")
	v, err := g.Get("b")
	if err != nil || v.(int) != 22 {
		t.Fatalf("b 应为 22（依据 a=2,c=20），得到 %v, err=%v", v, err)
	}
	if err := g.Verify(); err != nil {
		t.Fatalf("全量重算对照失败: %v", err)
	}
	t.Logf("判定依据: c=f(a)=20, b=f(a,c)=22；Verify 全量重算结果一致")
}

// 级联失效：改一个基视图，整条下游链全部重算并保持函数关系。
func TestCascadeInvalidation(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "a"})
	mustRegister(t, g, View{Name: "b", Dependencies: []string{"a"}, Fn: fnPlus(1)})
	mustRegister(t, g, View{Name: "c", Dependencies: []string{"b"}, Fn: fnTimes(2)})
	mustRegister(t, g, View{Name: "d", Dependencies: []string{"b", "c"}, Fn: func(d []any) (any, error) {
		return d[0].(int) + d[1].(int), nil
	}})
	t.Logf("注册顺序: a(基) -> b=a+1 -> c=2b -> d=b+c")

	for _, a := range []int{1, 4, 10} {
		if err := g.SetBaseAndRecompute("a", a); err != nil {
			t.Fatalf("a=%d: %v", a, err)
		}
		logSnapshot(t, g, fmt.Sprintf("级联失效: 设值 a=%d 并重算", a))
		wantB := a + 1
		wantC := wantB * 2
		wantD := wantB + wantC
		assertValues(t, g, map[string]int{"a": a, "b": wantB, "c": wantC, "d": wantD})
		t.Logf("判定依据: a=%d => b=%d, c=%d, d=%d（下游传递闭包全部重算）", a, wantB, wantC, wantD)
		if err := g.Verify(); err != nil {
			t.Fatalf("a=%d 全量重算对照失败: %v", a, err)
		}
	}
}

// 去重：同一轮里汇合的下游只求值一次；无脏视图时不求值。
func TestDeduplication(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "a"})
	mustRegister(t, g, View{Name: "x"})
	bCalls := 0
	yCalls := 0
	mustRegister(t, g, View{Name: "b", Dependencies: []string{"a"}, Fn: func(d []any) (any, error) {
		bCalls++
		return d[0].(int) + 1, nil
	}})
	mustRegister(t, g, View{Name: "y", Dependencies: []string{"a", "x", "b"}, Fn: func(d []any) (any, error) {
		yCalls++
		return d[0].(int) + d[1].(int) + d[2].(int), nil
	}})
	t.Logf("注册顺序: a,x 基视图 -> b=f(a) -> y=f(a,x,b)")

	if err := g.SetBase("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := g.SetBase("x", 10); err != nil {
		t.Fatal(err)
	}
	if err := g.SetBase("a", 2); err != nil {
		t.Fatal(err)
	}
	t.Logf("设值（未重算）: a=1, x=10, a=2 —— a 与 y 被多次标记但保持单一脏位")
	if err := g.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	logSnapshot(t, g, "去重: 合并设值后重算")
	if bCalls != 1 || yCalls != 1 {
		t.Fatalf("同一轮内 b/y 应各求值一次，得到 b=%d y=%d", bCalls, yCalls)
	}
	assertValues(t, g, map[string]int{"a": 2, "x": 10, "b": 3, "y": 15})
	t.Logf("判定依据: b 求值 1 次、y 求值 1 次；b=2+1=3, y=2+10+3=15")

	if err := g.Recompute(); err != nil {
		t.Fatalf("idle recompute: %v", err)
	}
	if bCalls != 1 || yCalls != 1 {
		t.Fatalf("无脏视图时不得求值，得到 b=%d y=%d", bCalls, yCalls)
	}
	t.Logf("判定依据: 第二轮无脏视图，b/y 求值次数保持 1")
	if err := g.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 环检测：互相依赖与自环必须整体拒绝且不改变注册状态。
func TestCycleDetection(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "x", Dependencies: []string{"y"}, Fn: fnPlus(0)})

	err := g.Register(View{Name: "y", Dependencies: []string{"x"}, Fn: fnPlus(0)})
	if kindOf(err) != ErrCycleDetected {
		t.Fatalf("x<->y 应报 cycle_detected，得到 %v", err)
	}
	t.Logf("环检测: 注册 y 依赖 x（x 已依赖 y）被拒绝: %v", err)

	err = g.Register(View{Name: "z", Dependencies: []string{"z"}, Fn: fnPlus(0)})
	if kindOf(err) != ErrCycleDetected {
		t.Fatalf("自环应报 cycle_detected，得到 %v", err)
	}
	t.Logf("环检测: z 依赖自身被拒绝: %v", err)

	if _, err := g.Get("y"); kindOf(err) != ErrNameNotRegistered {
		t.Fatalf("被拒绝的 y 应保持未注册，得到 %v", err)
	}
	if err := g.Register(View{Name: "y"}); err != nil {
		t.Fatalf("拒绝成环后应仍能注册合法基视图 y: %v", err)
	}
	t.Logf("判定依据: 成环注册被整体拒绝，图未被污染，合法基视图 y 随后可正常注册")
}

// 重算时依赖尚未注册：报错且整轮状态不变。
func TestUnknownDependency(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "a"})
	mustRegister(t, g, View{Name: "d", Dependencies: []string{"ghost"}, Fn: fnPlus(0)})

	if err := g.SetBase("a", 7); err != nil {
		t.Fatal(err)
	}
	err := g.Recompute()
	if kindOf(err) != ErrUnknownDep {
		t.Fatalf("应报 unknown_dependency，得到 %v", err)
	}
	t.Logf("未注册依赖: 重算被拒绝: %v", err)
	if g.Generation() != 0 {
		t.Fatalf("失败轮次不得推进 generation，得到 %d", g.Generation())
	}
	v, err := g.Get("a")
	if err != nil || v.(int) != 7 {
		t.Fatalf("失败不得改变已设基视图值，得到 %v/%v", v, err)
	}
	if _, err := g.Get("d"); kindOf(err) != ErrNotMaterialized {
		t.Fatalf("失败后 d 不应物化，得到 %v", err)
	}
	t.Logf("判定依据: generation=0，a=7 保留，d 未物化——失败无副作用")
}

// 未注册名读写、基/派生视图误用，必须给出可区分原因。
func TestNameAndKindErrors(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "a"})
	mustRegister(t, g, View{Name: "b", Dependencies: []string{"a"}, Fn: fnPlus(1)})

	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"Get 未注册名", ignore1(g.Get("nope")), ErrNameNotRegistered},
		{"SetBase 未注册名", g.SetBase("nope", 1), ErrNameNotRegistered},
		{"SetBaseAndRecompute 未注册名", g.SetBaseAndRecompute("nope", 1), ErrNameNotRegistered},
		{"对派生视图设值", g.SetBase("b", 1), ErrNotBaseView},
		{"读未物化视图", ignore1(g.Get("b")), ErrNotMaterialized},
	}
	for _, tc := range cases {
		if kindOf(tc.err) != tc.want {
			t.Fatalf("%s: 期望 %s，得到 %v", tc.name, tc.want, tc.err)
		}
		t.Logf("%s => %v", tc.name, tc.err)
	}

	if err := g.Register(View{}); kindOf(err) != ErrEmptyName {
		t.Fatalf("空名称应报 empty_name，得到 %v", err)
	}
	if err := g.Register(View{Name: "a"}); kindOf(err) != ErrDuplicateName {
		t.Fatalf("重名应报 duplicate_name，得到 %v", err)
	}
	if err := g.Register(View{Name: "base2", Fn: fnPlus(0)}); kindOf(err) != ErrBaseViewWithFn {
		t.Fatalf("基视图携带函数应报 base_view_with_fn，得到 %v", err)
	}
	if err := g.Register(View{Name: "d2", Dependencies: []string{"a"}}); kindOf(err) != ErrDerivedWithoutFn {
		t.Fatalf("派生视图缺函数应报 derived_without_fn，得到 %v", err)
	}
	t.Logf("判定依据: 空名/重名/类型误用各自返回稳定 ErrorKind，调用方可区分")
}

// 计算函数失败：整轮回滚，旧状态保持不变；合法值重试成功。
func TestComputeFailureAtomicity(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "a"})
	mustRegister(t, g, View{Name: "b", Dependencies: []string{"a"}, Fn: func(d []any) (any, error) {
		if d[0].(int) < 0 {
			return nil, fmt.Errorf("negative input")
		}
		return d[0].(int) + 100, nil
	}})

	if err := g.SetBaseAndRecompute("a", 1); err != nil {
		t.Fatal(err)
	}
	logSnapshot(t, g, "原子性: 设值 a=1 重算成功")

	err := g.SetBaseAndRecompute("a", -1)
	if kindOf(err) != ErrComputeFailed {
		t.Fatalf("应报 compute_failed，得到 %v", err)
	}
	t.Logf("原子性: a=-1 触发计算失败: %v", err)
	v, _ := g.Get("a")
	b, _ := g.Get("b")
	if v.(int) != 1 || b.(int) != 101 {
		t.Fatalf("失败后状态应回滚到 a=1,b=101，得到 a=%v b=%v", v, b)
	}
	if g.Generation() != 1 {
		t.Fatalf("失败不得推进 generation，得到 %d", g.Generation())
	}
	t.Logf("判定依据: 失败后仍为上一轮完整状态 a=1,b=101, generation=1")

	if err := g.SetBaseAndRecompute("a", 5); err != nil {
		t.Fatalf("合法值重试失败: %v", err)
	}
	assertValues(t, g, map[string]int{"a": 5, "b": 105})
	logSnapshot(t, g, "原子性: 设值 a=5 重试成功")
	t.Logf("判定依据: 回滚后可重试，a=5 => b=105")
}

// 并发：重算期间读者只能观察到整轮前或整轮后的一致状态。
func TestConcurrentReadersNeverSeeHalfRound(t *testing.T) {
	g := NewGraph()
	mustRegister(t, g, View{Name: "a"})
	mustRegister(t, g, View{Name: "b", Dependencies: []string{"a"}, Fn: fnTimes(2)})
	mustRegister(t, g, View{Name: "c", Dependencies: []string{"b"}, Fn: fnPlus(1)})

	if err := g.SetBaseAndRecompute("a", 0); err != nil {
		t.Fatal(err)
	}

	const readers = 8
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap, err := g.Snapshot()
				if err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
				a, ok1 := snap["a"].(int)
				b, ok2 := snap["b"].(int)
				c, ok3 := snap["c"].(int)
				if ok1 && ok2 && ok3 {
					if b != 2*a || c != 2*a+1 {
						t.Errorf("读到半轮混合状态: a=%d b=%d c=%d（应为 b=2a, c=2a+1）", a, b, c)
						return
					}
				}
			}
		}()
	}

	for a := 1; a <= 200; a++ {
		if err := g.SetBaseAndRecompute("a", a); err != nil {
			t.Fatalf("set %d: %v", a, err)
		}
	}
	close(stop)
	wg.Wait()

	logSnapshot(t, g, "并发: 200 轮原子设值重算完成")
	if err := g.Verify(); err != nil {
		t.Fatalf("最终全量重算对照失败: %v", err)
	}
	t.Logf("判定依据: 8 个并发读者全程只观察到 b=2a 且 c=2a+1 的完整轮次，未出现半轮混合")
}

// 可复现：拓扑并列顺序由注册顺序决定，两次全量推导结果一致。
func TestDeterministicReproducible(t *testing.T) {
	build := func() *Graph {
		g := NewGraph()
		mustRegister(t, g, View{Name: "a"})
		mustRegister(t, g, View{Name: "m", Dependencies: []string{"a"}, Fn: fnTimes(3)})
		mustRegister(t, g, View{Name: "n", Dependencies: []string{"a"}, Fn: fnPlus(7)})
		mustRegister(t, g, View{Name: "s", Dependencies: []string{"m", "n"}, Fn: func(d []any) (any, error) {
			return d[0].(int)*1000 + d[1].(int), nil
		}})
		return g
	}

	g1, g2 := build(), build()
	for i := 0; i < 5; i++ {
		if err := g1.SetBaseAndRecompute("a", i); err != nil {
			t.Fatal(err)
		}
		if err := g2.SetBaseAndRecompute("a", i); err != nil {
			t.Fatal(err)
		}
		s1, _ := g1.Snapshot()
		s2, _ := g2.Snapshot()
		if fmt.Sprint(s1) != fmt.Sprint(s2) {
			t.Fatalf("同输入两轮结果不一致: %v vs %v", s1, s2)
		}
	}
	logSnapshot(t, g1, "可复现: 相同注册顺序与输入")
	t.Logf("判定依据: 两个独立图在相同注册顺序与输入下快照逐字节一致")
}
