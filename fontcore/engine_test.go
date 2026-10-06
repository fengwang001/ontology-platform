package fontcore

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// Example 演示登记、触发、时钟推进、加载完成与整形的完整协作。
func Example() {
	cfg := DefaultConfig()
	engine, err := New(cfg)
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	_ = engine.Register(FamilySpec{
		Name: "main", Fallbacks: []string{"sys"},
		Faces: []FaceSpec{
			{Name: "latin", WeightLo: 400, WeightHi: 400,
				Style: StyleNormal, WidthLo: 100, WidthHi: 100,
				Display:  DisplaySwap,
				Ranges:   []RuneRange{{Lo: 'a', Hi: 'z'}},
				Resource: "https://example.test/latin.woff2"},
		},
	})
	_ = engine.Register(FamilySpec{
		Name: "sys",
		Faces: []FaceSpec{
			{Name: "sys-latin", WeightLo: 100, WeightHi: 900,
				Style: StyleNormal, WidthLo: 50, WidthHi: 200,
				Display: DisplayOptional,
				Ranges:  []RuneRange{{Lo: 'a', Hi: 'z'}}},
		},
	})
	_ = engine.Advance(0)
	runs, _ := engine.Shape("main", "abc", 400, 100, StyleNormal)
	fmt.Println("runs-at-0:", runs[0].Phase)
	_, _ = engine.ShapeChars("sys", "abc", 400, 100, StyleNormal)
	_ = engine.Loaded("sys", "sys-latin")
	runs, _ = engine.Shape("main", "abc", 400, 100, StyleNormal)
	fmt.Println("fallback:", runs[0].Family, runs[0].Face)
	_ = engine.Loaded("main", "latin")
	runs, _ = engine.Shape("main", "abc", 400, 100, StyleNormal)
	fmt.Println("primary:", runs[0].Family, runs[0].Face, runs[0].Runes)
	// Output:
	// runs-at-0: last-resort
	// fallback: sys sys-latin
	// primary: main latin 3
}

func Example_phaseString() {
	fmt.Println(PhaseBlock, PhaseSwap)
	// Output: block swap
}

func testCfg() Config {
	return Config{WeightLow: 400, WeightHigh: 500,
		BlockBlock: 10, SwapBlock: 2, FallbackBlock: 5,
		FallbackSwap: 10, OptionalBlock: 5}
}

// 四种策略在期末恰等时刻（左闭右开）的转换。
func TestPolicyBoundaries(t *testing.T) {
	cfg := testCfg()
	check := func(disp Display, at int64, want Phase) {
		t.Helper()
		e := newTestEngine(t, cfg)
		mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
			face0("p", 400, 400, StyleNormal, 100, 100, disp, rr0('a', 'z')),
		}})
		mustAdvance(t, e, 0)
		shape0(t, e, "f", "a", 400, 100, StyleNormal)
		mustAdvance(t, e, at)
		got := shape0(t, e, "f", "a", 400, 100, StyleNormal)
		if got[0].Phase != want {
			t.Fatalf("disp=%d at=%d want %s got %s", disp, at, want, got[0].Phase)
		}
	}
	// 期末前一刻仍在阻塞期；期末刻恰好进入下一期（左闭右开）。
	check(DisplayBlock, 9, PhaseBlock)
	check(DisplayBlock, 10, PhaseLastResort) // 进入交换期但无回退 -> 最后手段
	check(DisplaySwap, 1, PhaseBlock)
	check(DisplaySwap, 2, PhaseLastResort)
	check(DisplayFallback, 4, PhaseBlock)
	check(DisplayFallback, 5, PhaseLastResort)
	check(DisplayOptional, 4, PhaseBlock)
	check(DisplayOptional, 5, PhaseLastResort) // 可选无交换期：直接永久回退
}

func setupFallback(t *testing.T, disp Display) *Engine {
	t.Helper()
	e := newTestEngine(t, testCfg())
	mustReg(t, e, FamilySpec{Name: "f", Fallbacks: []string{"fb"}, Faces: []FaceSpec{
		face0("p", 400, 400, StyleNormal, 100, 100, disp, rr0('a', 'z')),
	}})
	mustReg(t, e, FamilySpec{Name: "fb", Faces: []FaceSpec{
		face0("q", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
	}})
	return e
}

// 加载完成替换占位与回退；交换期无限策略完成即可换回。
func TestLoadReplaces(t *testing.T) {
	e := setupFallback(t, DisplayBlock)
	mustAdvance(t, e, 0)
	if got := shape0(t, e, "f", "a", 400, 100, StyleNormal); got[0].Phase != PhaseBlock {
		t.Fatalf("initial block, got %s", got[0].Phase)
	}
	shape0(t, e, "fb", "a", 400, 100, StyleNormal)
	if err := e.Loaded("fb", "q"); err != nil {
		t.Fatal(err)
	}
	mustAdvance(t, e, 11)
	got := shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Phase != PhaseSwap || got[0].Family != "fb" {
		t.Fatalf("swap via fallback: %+v", got[0])
	}
	if err := e.Loaded("f", "p"); err != nil {
		t.Fatal(err)
	}
	if got = shape0(t, e, "f", "a", 400, 100, StyleNormal); got[0].Phase != PhasePrimary {
		t.Fatalf("primary after load: %s", got[0].Phase)
	}
}

// 可选策略：期满后加载完成也不得替换。
func TestOptionalLateLoad(t *testing.T) {
	e := setupFallback(t, DisplayOptional)
	shape0(t, e, "fb", "a", 400, 100, StyleNormal)
	if err := e.Loaded("fb", "q"); err != nil {
		t.Fatal(err)
	}
	shape0(t, e, "f", "a", 400, 100, StyleNormal) // t=0 触发
	mustAdvance(t, e, 6)
	got := shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Phase != PhaseFailed || got[0].Family != "fb" {
		t.Fatalf("optional expiry -> permanent fallback: %+v", got[0])
	}
	if err := e.Loaded("f", "p"); err != nil {
		t.Fatal(err)
	}
	if got = shape0(t, e, "f", "a", 400, 100, StyleNormal); got[0].Phase != PhaseFailed {
		t.Fatalf("late optional load must not replace: %s", got[0].Phase)
	}
}

// 回退策略：交换期边界决定完成是否可替换。
func TestFallbackPolicyWindow(t *testing.T) {
	setup := func() *Engine {
		e := setupFallback(t, DisplayFallback)
		shape0(t, e, "fb", "a", 400, 100, StyleNormal)
		if err := e.Loaded("fb", "q"); err != nil {
			t.Fatal(err)
		}
		shape0(t, e, "f", "a", 400, 100, StyleNormal) // t=0 触发，swapEnd=15
		return e
	}
	e := setup()
	mustAdvance(t, e, 14)
	if err := e.Loaded("f", "p"); err != nil {
		t.Fatal(err)
	}
	if got := shape0(t, e, "f", "a", 400, 100, StyleNormal); got[0].Phase != PhasePrimary {
		t.Fatalf("load at 14 should replace, got %s", got[0].Phase)
	}
	e2 := setup()
	mustAdvance(t, e2, 15) // 恰等期末 -> 已永久回退
	if err := e2.Loaded("f", "p"); err != nil {
		t.Fatal(err)
	}
	if got := shape0(t, e2, "f", "a", 400, 100, StyleNormal); got[0].Phase != PhaseFailed {
		t.Fatalf("load at 15 must not replace, got %s", got[0].Phase)
	}
}

// 回退族不触发自身加载；显式失败永久回退。
func TestFallbackNoTriggerAndFailure(t *testing.T) {
	e := setupFallback(t, DisplaySwap)
	shape0(t, e, "f", "a", 400, 100, StyleNormal) // t=0 先触发
	mustAdvance(t, e, 10)
	got := shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Phase != PhaseLastResort {
		t.Fatalf("unloaded fallback unusable: %+v", got[0])
	}
	if e.loads[[2]string{"fb", "q"}].triggered {
		t.Fatal("fallback family must not self-trigger")
	}
	if err := e.Failed("f", "p"); err != nil {
		t.Fatal(err)
	}
	if got = shape0(t, e, "f", "a", 400, 100, StyleNormal); got[0].Phase != PhaseLastResort {
		t.Fatalf("failed face permanent: %s", got[0].Phase)
	}
}

// 最后手段标记。
func TestLastResort(t *testing.T) {
	e := newTestEngine(t, testCfg())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("p", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
	}})
	got := shape0(t, e, "f", "中", 400, 100, StyleNormal)
	if got[0].Phase != PhaseLastResort {
		t.Fatalf("uncovered -> last resort, got %s", got[0].Phase)
	}
}

// 段落合并边界（按字节偏移）。
func TestRunMerging(t *testing.T) {
	e := newTestEngine(t, testCfg())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("la", 400, 400, StyleNormal, 100, 100, DisplayBlock, rr0('a', 'z')),
		face0("cj", 400, 400, StyleNormal, 100, 100, DisplayBlock, rr0(0x4E00, 0x9FFF)),
	}})
	runs, err := e.Shape("f", "aa中中b", 400, 100, StyleNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("want 3 runs, got %d: %+v", len(runs), runs)
	}
	if runs[0].Face != "la" || runs[0].Runes != 2 || runs[0].Start != 0 || runs[0].End != 2 {
		t.Fatalf("run0: %+v", runs[0])
	}
	if runs[1].Face != "cj" || runs[1].Runes != 2 {
		t.Fatalf("run1: %+v", runs[1])
	}
	if runs[2].Face != "la" || runs[2].Runes != 1 {
		t.Fatalf("run2: %+v", runs[2])
	}
	if "aa中中b"[runs[1].Start:runs[2].End] != "中中b" {
		t.Fatal("byte offsets wrong")
	}
}

// 并发：同一字符同一时刻的两次整形一致；每张人脸至多触发一次。
func TestConcurrentShapesConsistent(t *testing.T) {
	e := setupFallback(t, DisplaySwap)
	shape0(t, e, "f", "ab", 400, 100, StyleNormal) // t=0 触发，起算点固定
	mustAdvance(t, e, 3)
	const n = 64
	var wg sync.WaitGroup
	res := make([][]CharResult, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			res[i], errs[i] = e.ShapeChars("f", "ab", 400, 100, StyleNormal)
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if len(res[i]) != 2 || res[i][0] != res[0][0] || res[i][1] != res[0][1] {
			t.Fatalf("inconsistent concurrent shape: %+v vs %+v", res[i], res[0])
		}
	}
	ld := e.loads[[2]string{"f", "p"}]
	if !ld.triggered || ld.triggerAt != 0 {
		t.Fatalf("trigger once at first use: %+v", ld)
	}
	// 再推进 + 并发混合加载报告，不应出现重复 settled 之外的崩溃；
	// 只有一个 Loaded 能成功，其余必须拿到可区分错误。
	var ok, bad int
	var mu sync.Mutex
	wg.Add(8)
	for i := 0; i < 8; i++ {
		go func() {
			defer wg.Done()
			err := e.Loaded("f", "p")
			mu.Lock()
			if err == nil {
				ok++
			} else if errors.Is(err, ErrInvalidLoadState) {
				bad++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ok != 1 || ok+bad != 8 {
		t.Fatalf("loaded reports: ok=%d bad=%d", ok, bad)
	}
}
