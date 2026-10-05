package classify

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/lineage"
)

// mustEngine 返回引擎与一个断言无错并展开 Changed 的辅助闭包。
func mustEngine(t *testing.T, nmax int) (*Engine, func([]Change, error) []Change) {
	t.Helper()
	e, err := New(nmax)
	if err != nil {
		t.Fatalf("New(%d): %v", nmax, err)
	}
	op := func(changes []Change, err error) []Change {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return changes
	}
	return e, op
}

func checkChanged(t *testing.T, what string, got, want []Change) {
	t.Helper()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: Changed = %v, want %v", what, got, want)
	}
}

func checkEff(t *testing.T, e *Engine, want map[string]int) {
	t.Helper()
	for name, w := range want {
		got, err := e.Eff(name)
		if err != nil {
			t.Errorf("Eff(%q): %v", name, err)
			continue
		}
		if got != w {
			t.Errorf("Eff(%q) = %d, want %d", name, got, w)
		}
	}
}

func checkWhy(t *testing.T, e *Engine, col string, want Reason) {
	t.Helper()
	got, err := e.Why(col)
	if err != nil {
		t.Errorf("Why(%q): %v", col, err)
		return
	}
	if got != want {
		t.Errorf("Why(%q) = %+v, want %+v", col, got, want)
	}
}

// TestSpecExample 逐步复现题目中的例子。
func TestSpecExample(t *testing.T) {
	e, op := mustEngine(t, 100)
	op(e.AddColumn("a", 4, 0))
	op(e.AddColumn("b", 0, 0))
	op(e.AddColumn("c", 0, 0))
	op(e.AddColumn("d", 1, 0))
	op(e.AddEdge("a", "b", lineage.Mask, 1))
	op(e.AddEdge("b", "c", lineage.Hash, 2))
	op(e.AddEdge("a", "d", lineage.Agg, 3))

	checkEff(t, e, map[string]int{"a": 4, "b": 3, "c": 1, "d": 2})
	checkWhy(t, e, "d", Reason{Kind: ReasonEdge, Src: "a"})

	got := op(e.Declass("b", 1, 100, 10))
	checkChanged(t, "Declass(b,1,100)@10", got, []Change{{"b", 3, 1}, {"c", 1, 0}})
	checkWhy(t, e, "b", Reason{Kind: ReasonCap})

	got = op(e.Raise("c", 2, 20))
	checkChanged(t, "Raise(c,2)@20", got, []Change{{"c", 0, 2}})
	checkWhy(t, e, "c", Reason{Kind: ReasonFloor})

	got = op(e.Tick(99))
	checkChanged(t, "Tick(99)", got, nil)
	checkEff(t, e, map[string]int{"a": 4, "b": 1, "c": 2, "d": 2})

	got = op(e.Tick(100))
	checkChanged(t, "Tick(100)", got, []Change{{"b", 1, 3}})
	if e.evals != 2 {
		t.Errorf("Tick(100) evals = %d, want 2", e.evals)
	}
	checkEff(t, e, map[string]int{"a": 4, "b": 3, "c": 2, "d": 2})

	got = op(e.RemoveEdge("a", "b", 101))
	checkChanged(t, "RemoveEdge(a,b)@101", got, []Change{{"b", 3, 0}})
	checkEff(t, e, map[string]int{"a": 4, "b": 0, "c": 2, "d": 2})
}

// TestEdgeKinds 覆盖四种 kind 的降级与封顶。
func TestEdgeKinds(t *testing.T) {
	cases := []struct {
		kind     lineage.Kind
		up, want int
	}{
		{lineage.Copy, 4, 4},
		{lineage.Mask, 4, 3},
		{lineage.Hash, 4, 2},
		{lineage.Agg, 4, 2}, // 封顶在 2
		{lineage.Copy, 1, 1},
		{lineage.Mask, 1, 0},
		{lineage.Hash, 1, 0},
		{lineage.Agg, 1, 1},
		{lineage.Hash, 2, 0},
	}
	for _, tc := range cases {
		e, op := mustEngine(t, 10)
		op(e.AddColumn("up", tc.up, 0))
		op(e.AddColumn("down", 0, 0))
		op(e.AddEdge("up", "down", tc.kind, 1))
		checkEff(t, e, map[string]int{"up": tc.up, "down": tc.want})
	}
}

// TestFloorOverridesCap 下限压过上限。
func TestFloorOverridesCap(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("x", 4, 0))
	got := op(e.Declass("x", 1, 100, 1))
	checkChanged(t, "Declass", got, []Change{{"x", 4, 1}})
	checkWhy(t, e, "x", Reason{Kind: ReasonCap})

	got = op(e.Raise("x", 3, 2))
	checkChanged(t, "Raise", got, []Change{{"x", 1, 3}})
	checkEff(t, e, map[string]int{"x": 3})
	checkWhy(t, e, "x", Reason{Kind: ReasonFloor})

	// 取消下限后回到 cap。
	got = op(e.Raise("x", 0, 3))
	checkChanged(t, "Raise(0)", got, []Change{{"x", 3, 1}})
	checkWhy(t, e, "x", Reason{Kind: ReasonCap})
}

// TestCapExpiryBoundary until 恰等与差 1 的边界。
func TestCapExpiryBoundary(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("x", 4, 0))

	// until <= now 属于参数非法。
	if _, err := e.Declass("x", 1, 5, 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("Declass until==now: err = %v, want ErrInvalid", err)
	}
	if _, err := e.Declass("x", 1, 4, 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("Declass until<now: err = %v, want ErrInvalid", err)
	}
	// until = now+1 是最小合法值。
	got := op(e.Declass("x", 1, 6, 5))
	checkChanged(t, "Declass(x,1,6)@5", got, []Change{{"x", 4, 1}})

	got = op(e.Tick(5))
	checkChanged(t, "Tick(5)", got, nil)
	checkEff(t, e, map[string]int{"x": 1}) // t=5 < until=6 仍生效

	got = op(e.Tick(6))
	checkChanged(t, "Tick(6)", got, []Change{{"x", 1, 4}}) // t==until 即失效

	// 覆盖式 Declass，验证 until 差 1 的两侧。
	got = op(e.Declass("x", 2, 10, 7))
	checkChanged(t, "Declass(x,2,10)@7", got, []Change{{"x", 4, 2}})
	got = op(e.Tick(9))
	checkChanged(t, "Tick(9)", got, nil)
	checkEff(t, e, map[string]int{"x": 2})
	got = op(e.Tick(10))
	checkChanged(t, "Tick(10)", got, []Change{{"x", 2, 4}})
}

// TestRemoveEdgeLowers 删边导致级别下降。
func TestRemoveEdgeLowers(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("a", 4, 0))
	op(e.AddColumn("b", 1, 0))
	op(e.AddEdge("a", "b", lineage.Copy, 1))
	checkEff(t, e, map[string]int{"b": 4})
	got := op(e.RemoveEdge("a", "b", 2))
	checkChanged(t, "RemoveEdge", got, []Change{{"b", 4, 1}})
	checkWhy(t, e, "b", Reason{Kind: ReasonBase})
}

// TestStopWhenUnchanged eff 未变即止步，下游不再求值。
func TestStopWhenUnchanged(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("a", 4, 0))
	op(e.AddColumn("b", 2, 0))
	op(e.AddColumn("c", 0, 0))
	op(e.AddEdge("a", "b", lineage.Agg, 1)) // agg 封顶 2，b 恒为 2
	op(e.AddEdge("b", "c", lineage.Copy, 2))
	checkEff(t, e, map[string]int{"a": 4, "b": 2, "c": 2})

	got := op(e.SetBase("a", 3, 3))
	checkChanged(t, "SetBase(a,3)", got, []Change{{"a", 4, 3}})
	if e.evals != 2 { // 只求值 a 与 b；c 不求值
		t.Errorf("evals = %d, want 2", e.evals)
	}
}

// TestDiamondEvaluatedOnce 菱形汇合处每列只求值一次。
func TestDiamondEvaluatedOnce(t *testing.T) {
	e, op := mustEngine(t, 10)
	for _, c := range []struct {
		name string
		base int
	}{{"a", 4}, {"b", 0}, {"c", 0}, {"d", 0}} {
		op(e.AddColumn(c.name, c.base, 0))
	}
	op(e.AddEdge("a", "b", lineage.Copy, 1))
	op(e.AddEdge("a", "c", lineage.Copy, 1))
	op(e.AddEdge("b", "d", lineage.Copy, 1))
	op(e.AddEdge("c", "d", lineage.Copy, 1))

	got := op(e.SetBase("a", 3, 2))
	checkChanged(t, "SetBase(a,3)", got, []Change{
		{"a", 4, 3}, {"b", 4, 3}, {"c", 4, 3}, {"d", 4, 3},
	})
	if e.evals != 4 { // a,b,c,d 各一次；d 虽有两条入边也只求值一次
		t.Errorf("evals = %d, want 4", e.evals)
	}
}

// TestWhatIfMatchesSetBase WhatIf 与真实 SetBase 逐项一致，且不改状态。
func TestWhatIfMatchesSetBase(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("a", 1, 0))
	op(e.AddColumn("b", 0, 0))
	op(e.AddColumn("c", 0, 0))
	op(e.AddEdge("a", "b", lineage.Mask, 1))
	op(e.AddEdge("b", "c", lineage.Copy, 1))

	before := map[string]int{"a": 1, "b": 0, "c": 0}
	whatIf := op(e.WhatIf("a", 4))
	checkEff(t, e, before) // WhatIf 不改任何状态

	got := op(e.SetBase("a", 4, 2)) // 本次无上限失效
	if !reflect.DeepEqual(whatIf, got) {
		t.Errorf("WhatIf = %v, SetBase = %v, 应逐项相同", whatIf, got)
	}
	checkChanged(t, "SetBase(a,4)", got, []Change{{"a", 1, 4}, {"b", 0, 3}, {"c", 0, 3}})

	// WhatIf 的参数校验。
	if _, err := e.WhatIf("a", 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("WhatIf bad level: err = %v, want ErrInvalid", err)
	}
	if _, err := e.WhatIf("zz", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("WhatIf missing: err = %v, want ErrNotFound", err)
	}
}

// TestWhyTieAndOrder Why 的并列处理与 Floor>Cap>Base>Edge 次序。
func TestWhyTieAndOrder(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("a", 2, 0))
	op(e.AddColumn("b", 2, 0))
	op(e.AddColumn("d", 0, 0))
	op(e.AddEdge("b", "d", lineage.Copy, 1))
	op(e.AddEdge("a", "d", lineage.Copy, 1))
	// 两条入边贡献并列，取 src 字节序最小者。
	checkWhy(t, e, "d", Reason{Kind: ReasonEdge, Src: "a"})

	// b 抬升后贡献更大，来源切换。
	op(e.Raise("b", 3, 2))
	checkWhy(t, e, "d", Reason{Kind: ReasonEdge, Src: "b"})
	// 取消下限并降级后回到 a。
	op(e.Raise("b", 0, 3))
	op(e.Declass("b", 1, 100, 4))
	checkWhy(t, e, "d", Reason{Kind: ReasonEdge, Src: "a"})

	// Base：base 不小于所有入边贡献；无入边也是 Base。
	op(e.AddColumn("z", 3, 5))
	op(e.AddColumn("y", 3, 5))
	op(e.AddEdge("z", "y", lineage.Mask, 6)) // 贡献 mask(3)=2 < base 3
	checkWhy(t, e, "y", Reason{Kind: ReasonBase})
	checkWhy(t, e, "z", Reason{Kind: ReasonBase})

	// Cap 与 Floor 的优先次序。
	op(e.AddColumn("x", 4, 7))
	op(e.Declass("x", 1, 100, 8))
	checkWhy(t, e, "x", Reason{Kind: ReasonCap})
	op(e.Raise("x", 2, 9))
	checkWhy(t, e, "x", Reason{Kind: ReasonFloor})
}

// TestRejectionOrder 拒绝次序：参数非法 > 时钟回退 > 列不存在 >
// 已存在/边不存在 > 超限 > 成环，只报第一个。
func TestRejectionOrder(t *testing.T) {
	longName := strings.Repeat("x", 129)
	cases := []struct {
		name string
		op   func(e *Engine) error
		want error
	}{
		{"空名字", func(e *Engine) error { _, err := e.AddColumn("", 1, 2); return err }, ErrInvalid},
		{"超长名字", func(e *Engine) error { _, err := e.AddColumn(longName, 1, 2); return err }, ErrInvalid},
		{"级别越界", func(e *Engine) error { _, err := e.AddColumn("z", 5, 2); return err }, ErrInvalid},
		{"级别越界优先于时钟", func(e *Engine) error { _, err := e.AddColumn("z", 5, 0); return err }, ErrInvalid},
		{"时钟回退", func(e *Engine) error { _, err := e.AddColumn("z", 1, 0); return err }, ErrClock},
		{"时钟优先于不存在", func(e *Engine) error { _, err := e.SetBase("zz", 1, 0); return err }, ErrClock},
		{"列不存在", func(e *Engine) error { _, err := e.SetBase("zz", 1, 2); return err }, ErrNotFound},
		{"非法级别优先于不存在", func(e *Engine) error { _, err := e.SetBase("zz", 9, 2); return err }, ErrInvalid},
		{"列重复", func(e *Engine) error { _, err := e.AddColumn("a", 1, 2); return err }, ErrExists},
		{"自环属参数非法", func(e *Engine) error { _, err := e.AddEdge("a", "a", lineage.Copy, 2); return err }, ErrInvalid},
		{"kind越界", func(e *Engine) error { _, err := e.AddEdge("a", "b", lineage.Kind(9), 2); return err }, ErrInvalid},
		{"边重复", func(e *Engine) error { _, err := e.AddEdge("a", "b", lineage.Mask, 2); return err }, ErrExists},
		{"边不存在", func(e *Engine) error { _, err := e.RemoveEdge("b", "a", 2); return err }, ErrEdgeNotFound},
		{"删边列不存在优先", func(e *Engine) error { _, err := e.RemoveEdge("zz", "a", 2); return err }, ErrNotFound},
		{"成环", func(e *Engine) error { _, err := e.AddEdge("b", "a", lineage.Copy, 2); return err }, ErrCycle},
		{"Raise级别越界", func(e *Engine) error { _, err := e.Raise("a", 6, 2); return err }, ErrInvalid},
		{"until不严格大于now", func(e *Engine) error { _, err := e.Declass("a", 1, 2, 2); return err }, ErrInvalid},
		{"Tick时钟回退", func(e *Engine) error { _, err := e.Tick(0); return err }, ErrClock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, op := mustEngine(t, 10)
			op(e.AddColumn("a", 1, 0))
			op(e.AddColumn("b", 2, 0))
			op(e.AddEdge("a", "b", lineage.Copy, 1)) // 时钟推进到 1
			if err := tc.op(e); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

// TestLimits Nmax 与入边 16 上限，且超限检查在成环之前。
func TestLimits(t *testing.T) {
	e, op := mustEngine(t, 2)
	op(e.AddColumn("a", 1, 0))
	op(e.AddColumn("b", 1, 0))
	if _, err := e.AddColumn("c", 1, 1); !errors.Is(err, ErrLimit) {
		t.Errorf("Nmax: err = %v, want ErrLimit", err)
	}

	e2, _ := mustEngine(t, 32)
	op(e2.AddColumn("dst", 0, 0))
	for i := 0; i < 16; i++ {
		src := string(rune('A' + i))
		op(e2.AddColumn(src, 1, 0))
	}
	for i := 0; i < 16; i++ {
		src := string(rune('A' + i))
		op(e2.AddEdge(src, "dst", lineage.Copy, int64(i+1)))
	}
	op(e2.AddColumn("extra", 1, 20))
	if _, err := e2.AddEdge("extra", "dst", lineage.Copy, 21); !errors.Is(err, ErrLimit) {
		t.Errorf("in-degree 16: err = %v, want ErrLimit", err)
	}
}

// TestRejectedKeepsState 被拒绝的操作不改任何状态：数据、时钟、上限失效。
func TestRejectedKeepsState(t *testing.T) {
	e, op := mustEngine(t, 10)
	op(e.AddColumn("x", 4, 0))
	op(e.Declass("x", 1, 10, 5)) // cap 至 t=10
	checkEff(t, e, map[string]int{"x": 1})

	// 非法级别 + now=10：被拒后上限不得因 now=10 失效，时钟不得推进。
	if _, err := e.SetBase("x", 9, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	checkEff(t, e, map[string]int{"x": 1})

	// 时钟回退的被拒操作同样不改状态。
	if _, err := e.Tick(3); !errors.Is(err, ErrClock) {
		t.Fatalf("err = %v, want ErrClock", err)
	}
	checkEff(t, e, map[string]int{"x": 1})

	// 时钟仍停在 5：now=6 可被接受；cap 在 t=6 仍生效。
	got := op(e.Tick(6))
	checkChanged(t, "Tick(6)", got, nil)
	checkEff(t, e, map[string]int{"x": 1})

	// 到 t=10 上限正常失效。
	got = op(e.Tick(10))
	checkChanged(t, "Tick(10)", got, []Change{{"x", 1, 4}})
}

// TestEvalsIndependentOfUnrelated 无关列 100 与 10000 两档下 evals 相同。
func TestEvalsIndependentOfUnrelated(t *testing.T) {
	run := func(unrelated int) (int, []Change) {
		e, op := mustEngine(t, unrelated+10)
		op(e.AddColumn("a", 1, 0))
		op(e.AddColumn("b", 0, 0))
		op(e.AddColumn("c", 0, 0))
		op(e.AddEdge("a", "b", lineage.Mask, 1))
		op(e.AddEdge("b", "c", lineage.Hash, 1))
		for i := 0; i < unrelated; i++ {
			op(e.AddColumn("filler-"+strings.Repeat("x", 1+i%100)+string(rune(i)), i%5, 1))
		}
		changes := op(e.SetBase("a", 4, 2))
		return e.evals, changes
	}
	evals100, changes100 := run(100)
	evals10000, changes10000 := run(10000)
	if evals100 != evals10000 {
		t.Errorf("evals: 无关列 100 档 = %d, 10000 档 = %d, 应相同", evals100, evals10000)
	}
	if !reflect.DeepEqual(changes100, changes10000) {
		t.Errorf("Changed 不一致: %v vs %v", changes100, changes10000)
	}
	if evals100 != 3 { // a, b, c 各一次
		t.Errorf("evals = %d, want 3", evals100)
	}
}

// TestConcurrent 并发调用等价于某个串行顺序（配合 -race）。
func TestConcurrent(t *testing.T) {
	e, op := mustEngine(t, 64)
	op(e.AddColumn("a", 1, 0))
	op(e.AddColumn("b", 0, 0))
	op(e.AddEdge("a", "b", lineage.Copy, 1))

	var wg sync.WaitGroup
	// 单写者推进时钟与级别。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			now := int64(2 + i)
			_, _ = e.SetBase("a", i%5, now)
			_, _ = e.Tick(now)
		}
	}()
	// 多读者。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = e.Eff("a")
				_, _ = e.Eff("b")
				_, _ = e.Why("b")
				_, _ = e.WhatIf("a", i%5)
			}
		}()
	}
	wg.Wait()
	effA, err := e.Eff("a")
	if err != nil || effA != 49%5 {
		t.Errorf("Eff(a) = %d, %v; want %d", effA, err, 49%5)
	}
}
