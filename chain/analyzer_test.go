package chain

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, a *Analyzer, id string, period, phase, w int) {
	t.Helper()
	if err := a.AddTask(Task{ID: id, Period: period, Phase: phase, WriteDelay: w}); err != nil {
		t.Fatalf("AddTask(%s): %v", id, err)
	}
}

func mustChain(t *testing.T, a *Analyzer, name string, ids ...string) {
	t.Helper()
	if err := a.AddChain(name, ids); err != nil {
		t.Fatalf("AddChain(%s): %v", name, err)
	}
}

// 规范示例：A(4,0,4) B(6,1,6) => 18/11/18；B 相位 0 => 17/10/17。
func TestSpecExample(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 4, 0, 4)
	mustAdd(t, a, "B", 6, 1, 6)
	mustChain(t, a, "c", "A", "B")
	got, err := a.Analyze("c")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Analysis{18, 11, 18}) {
		t.Fatalf("phi=1 got %+v", got)
	}
	if err := a.SetPhase("B", 0); err != nil {
		t.Fatal(err)
	}
	got, _ = a.Analyze("c")
	if got != (Analysis{17, 10, 17}) {
		t.Fatalf("phi=0 got %+v", got)
	}
	if r := reactionAt(5, []int{4, 6}, []int{0, 0}, []int{4, 6}); r != 13 {
		t.Fatalf("x=5 reaction = %d want 13", r)
	}
}

// 同刻可见 vs 差 1 不可见。
func TestSameTimeVisibility(t *testing.T) {
	p, w := []int{4, 4}, []int{4, 4}
	if r := reactionAt(0, p, []int{0, 0}, w); r != 8 {
		t.Fatalf("same-time r=%d want 8", r)
	}
	// B 相位 1：A 于 4 写出时，B 的作业 1 已在 1 时刻读过，下一释放 5（差 1 不可见），9 写出
	if r := reactionAt(0, p, []int{0, 1}, w); r != 9 {
		t.Fatalf("offset r=%d want 9", r)
	}
	if firstReleaseAtOrAfter(4, 4, 0) != 4 {
		t.Fatal("firstRelease >= must include equal")
	}
	if firstReleaseAtOrAfter(5, 4, 1) != 5 {
		t.Fatal("firstRelease 5 for phi1")
	}
}

// τ1 采样恰在 x 上算本次采样。
func TestSampleExactlyAtX(t *testing.T) {
	p, w := []int{5, 7}, []int{5, 7}
	// x=0 即 τ1 首个释放时刻：取本作业(0 释放、5 写出)，B 于 7 释放、14 写出，R=14
	if firstReleaseAtOrAfter(0, 5, 0) != 0 {
		t.Fatal("x equal to a release must select that release")
	}
	if r := reactionAt(0, p, []int{0, 0}, w); r != 14 {
		t.Fatalf("exact x=0 r=%d want 14", r)
	}
	// x=1 错过 0 点作业，τ1 下一释放为 5（10 写出），B 于 14 释放、21 写出，R=20
	if r := reactionAt(1, p, []int{0, 0}, w); r != 20 {
		t.Fatalf("x=1 r=%d want 20", r)
	}
}

// w<T 的隐式（固定响应时间）通信。
func TestImplicitCommunication(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 10, 0, 2)
	mustAdd(t, a, "B", 10, 2, 3) // B 释放时刻对齐 A 写出时刻 2+10j
	mustChain(t, a, "c", "A", "B")
	got, err := a.Analyze("c")
	if err != nil {
		t.Fatal(err)
	}
	// x=0：A 于 2 写出，B 同刻释放可见、5 写出，R=5=Σw
	if r := reactionAt(0, []int{10, 10}, []int{0, 2}, []int{2, 3}); r != 5 {
		t.Fatalf("aligned x=0 r=%d want 5", r)
	}
	if got.MinReaction != 5 {
		t.Fatalf("min reaction %d want sumW=5", got.MinReaction)
	}
	if got.MaxAge != got.MaxReaction {
		t.Fatalf("age/reaction mismatch %+v", got)
	}
}

// 链上周期不同余时的超周期 H。
func TestNonCongruentHyperperiod(t *testing.T) {
	if hyperperiod([]int{4, 6}) != 12 {
		t.Fatal("lcm(4,6)=12")
	}
	if hyperperiod([]int{3, 5, 7}) != 105 {
		t.Fatal("lcm(3,5,7)=105")
	}
	a := NewAnalyzer()
	mustAdd(t, a, "A", 3, 0, 3)
	mustAdd(t, a, "B", 5, 0, 5)
	mustAdd(t, a, "C", 7, 0, 7)
	mustChain(t, a, "c", "A", "B", "C")
	got, _ := a.Analyze("c")
	want := analyzeChain([]int{3, 5, 7}, []int{0, 0, 0}, []int{3, 5, 7})
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// x 取值区间端点：Φ 与 Φ+H-1 都计入。
func TestIntervalEndpoints(t *testing.T) {
	p, w, ph := []int{4, 6}, []int{4, 6}, []int{0, 1}
	got := analyzeChain(p, ph, w)
	if got != (Analysis{18, 11, 18}) {
		t.Fatalf("endpoint analysis %+v", got)
	}
	r0 := reactionAt(1, p, ph, w)
	rLast := reactionAt(12, p, ph, w)
	if r0 != 18 || rLast != 13 {
		t.Fatalf("endpoint reactions x=Phi:%d x=Phi+H-1:%d", r0, rLast)
	}
}

// Tune：φ=1 的 B 被调到 0；φ=2（同样 17）当前已最优，不改动。
func TestTuneCommitAndNoop(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 4, 0, 4)
	mustAdd(t, a, "B", 6, 1, 6)
	mustChain(t, a, "c", "A", "B")
	res, err := a.Tune("c")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.BeforeMaxReaction != 18 || res.AfterMaxReaction != 17 {
		t.Fatalf("tune res %+v", res)
	}
	if fmt.Sprint(res.BeforePhases) != "[0 1]" || fmt.Sprint(res.AfterPhases) != "[0 0]" {
		t.Fatalf("phases before=%v after=%v", res.BeforePhases, res.AfterPhases)
	}
	tasks, _ := a.Snapshot()
	if tasks[1].Phase != 0 {
		t.Fatalf("committed phase %d", tasks[1].Phase)
	}
	res2, _ := a.Tune("c")
	if res2.Changed || res2.AfterMaxReaction != 17 {
		t.Fatalf("noop tune %+v", res2)
	}

	b := NewAnalyzer()
	mustAdd(t, b, "A", 4, 0, 4)
	mustAdd(t, b, "B", 6, 2, 6)
	mustChain(t, b, "c", "A", "B")
	an, _ := b.Analyze("c")
	if an.MaxReaction != 17 {
		t.Fatalf("phi=2 max %d want 17", an.MaxReaction)
	}
	res3, _ := b.Tune("c")
	if res3.Changed {
		t.Fatalf("phi=2 must not change: %+v", res3)
	}
	tasks, _ = b.Snapshot()
	if tasks[1].Phase != 2 {
		t.Fatalf("phase mutated to %d", tasks[1].Phase)
	}
}

// Tune 并列取字典序最小：用独立穷举交叉验证每个起始相位的落点。
func TestTuneLexicographicTie(t *testing.T) {
	p, w := []int{6, 4}, []int{6, 4}
	best := 1 << 30
	bestPhi := -1
	for phiB := 0; phiB < 4; phiB++ {
		m := analyzeChain(p, []int{0, phiB}, w).MaxReaction
		if m < best {
			best, bestPhi = m, phiB
		}
	}
	for start := 0; start < 4; start++ {
		startMax := analyzeChain(p, []int{0, start}, w).MaxReaction
		a := NewAnalyzer()
		mustAdd(t, a, "A", 6, 0, 6)
		mustAdd(t, a, "B", 4, start, 4)
		mustChain(t, a, "c", "A", "B")
		res, err := a.Tune("c")
		if err != nil {
			t.Fatal(err)
		}
		if startMax == best {
			// 当前已最优：不得提交，即使存在并列相位
			if res.Changed {
				t.Fatalf("start=%d already optimal but changed to %v", start, res.AfterPhases)
			}
			continue
		}
		if !res.Changed || res.AfterPhases[1] != bestPhi {
			t.Fatalf("start=%d chose %v want phi=%d best=%d", start, res.AfterPhases, bestPhi, best)
		}
	}
}

// Tune 提交与逐个 SetPhase 到同一组相位等价。
func TestTuneEqualsSetPhase(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 4, 0, 4)
	mustAdd(t, a, "B", 6, 3, 6)
	mustChain(t, a, "c", "A", "B")
	res, _ := a.Tune("c")

	b := NewAnalyzer()
	mustAdd(t, b, "A", 4, 0, 4)
	mustAdd(t, b, "B", 6, 3, 6)
	mustChain(t, b, "c", "A", "B")
	if err := b.SetPhase("B", res.AfterPhases[1]); err != nil {
		t.Fatal(err)
	}
	ta, ca := a.Snapshot()
	tb, cb := b.Snapshot()
	if fmt.Sprint(ta) != fmt.Sprint(tb) || fmt.Sprint(ca) != fmt.Sprint(cb) {
		t.Fatalf("states differ:\n%v %v\n%v %v", ta, ca, tb, cb)
	}
}

// Tune 共享链拒绝，且被拒操作不得改动任何状态。
func TestTuneSharedRejected(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 4, 0, 4)
	mustAdd(t, a, "B", 6, 1, 6)
	mustAdd(t, a, "C", 8, 0, 8)
	mustChain(t, a, "c1", "A", "B")
	mustChain(t, a, "c2", "B", "C")
	if _, err := a.Tune("c1"); !errors.Is(err, ErrShared) {
		t.Fatalf("tune shared err=%v", err)
	}
	tasks, _ := a.Snapshot()
	if tasks[0].Phase != 0 || tasks[1].Phase != 1 || tasks[2].Phase != 0 {
		t.Fatalf("rejected tune mutated state: %+v", tasks)
	}
}

// 规模过大：H>5000；Tune 周期积>1000；边界 1000 允许。
func TestTooLarge(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 997, 0, 10)
	mustAdd(t, a, "B", 991, 0, 10)
	if err := a.AddChain("big", []string{"A", "B"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("addchain H err=%v", err)
	}

	b := NewAnalyzer()
	mustAdd(t, b, "P", 32, 0, 32)
	mustAdd(t, b, "Q", 32, 0, 32)
	mustChain(t, b, "c", "P", "Q")
	if _, err := b.Analyze("c"); err != nil {
		t.Fatalf("H=32 analyze should pass: %v", err)
	}

	c := NewAnalyzer()
	mustAdd(t, c, "P", 25, 0, 25)
	mustAdd(t, c, "Q", 40, 0, 40)
	mustChain(t, c, "c", "P", "Q")
	if _, err := c.Tune("c"); err != nil {
		t.Fatalf("product=1000 should pass: %v", err)
	}

	// τ2..τn 周期之积：T2*T3=32*32=1024 > 1000，而 H=32
	d := NewAnalyzer()
	mustAdd(t, d, "T1", 32, 0, 32)
	mustAdd(t, d, "T2", 32, 0, 32)
	mustAdd(t, d, "T3", 32, 0, 32)
	mustChain(t, d, "c", "T1", "T2", "T3")
	if _, err := d.Analyze("c"); err != nil {
		t.Fatalf("H=32 analyze should pass: %v", err)
	}
	if _, err := d.Tune("c"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("tune product T2*T3=1024 err=%v", err)
	}

	// 边界：T2*T3=25*40=1000 允许，H=lcm(25,40)=200
	e := NewAnalyzer()
	mustAdd(t, e, "U1", 25, 0, 25)
	mustAdd(t, e, "U2", 25, 0, 25)
	mustAdd(t, e, "U3", 40, 0, 40)
	mustChain(t, e, "c", "U1", "U2", "U3")
	if _, err := e.Tune("c"); err != nil {
		t.Fatalf("product=1000 boundary should pass: %v", err)
	}
}

// 拒绝原因顺序：非法参数最先，其后不存在、重复、容量、规模、引用、共享。
func TestRejectionOrder(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 4, 0, 4)
	if err := a.AddTask(Task{ID: "", Period: 4, Phase: 0, WriteDelay: 4}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id err=%v", err)
	}
	long := "123456789012345678901234567890123"
	if err := a.AddTask(Task{ID: long, Period: 4, Phase: 0, WriteDelay: 4}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("33-byte id err=%v", err)
	}
	if err := a.AddTask(Task{ID: "X", Period: 0, Phase: 0, WriteDelay: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("T=0 err=%v", err)
	}
	if err := a.AddTask(Task{ID: "X", Period: 1001, Phase: 0, WriteDelay: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("T=1001 err=%v", err)
	}
	if err := a.AddTask(Task{ID: "X", Period: 4, Phase: 4, WriteDelay: 4}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("phi=T err=%v", err)
	}
	if err := a.AddTask(Task{ID: "X", Period: 4, Phase: -1, WriteDelay: 4}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("phi=-1 err=%v", err)
	}
	if err := a.AddTask(Task{ID: "X", Period: 4, Phase: 0, WriteDelay: 5}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("w>T err=%v", err)
	}
	if err := a.AddTask(Task{ID: "A", Period: 4, Phase: 0, WriteDelay: 4}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate id err=%v", err)
	}
	if err := a.SetPhase("ghost", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("setphase missing err=%v", err)
	}
	if err := a.SetDelay("ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("setdelay missing err=%v", err)
	}
	if err := a.SetPhase("A", 9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("setphase range err=%v", err)
	}
	if err := a.SetDelay("A", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("setdelay 0 err=%v", err)
	}
	if err := a.RemoveTask("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing err=%v", err)
	}
	if err := a.AddChain("", []string{"A"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty chain name err=%v", err)
	}
	if err := a.AddChain("one", []string{"A"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("chain len 1 err=%v", err)
	}
	mustAdd(t, a, "B", 6, 1, 6)
	mustChain(t, a, "c", "A", "B")
	if err := a.AddChain("c", []string{"A", "B"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup chain err=%v", err)
	}
	if err := a.AddChain("d", []string{"A", "A"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup task in chain err=%v", err)
	}
	if err := a.AddChain("e", []string{"A", "ZZ"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task in chain err=%v", err)
	}
	if err := a.RemoveTask("A"); !errors.Is(err, ErrInUse) {
		t.Fatalf("remove referenced err=%v", err)
	}
	if _, err := a.Analyze("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("analyze missing err=%v", err)
	}
	if _, err := a.Tune("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tune missing err=%v", err)
	}
	if err := a.RemoveChain("c"); err != nil {
		t.Fatalf("remove chain: %v", err)
	}
	if err := a.RemoveTask("A"); err != nil {
		t.Fatalf("remove after chain gone: %v", err)
	}
}

// 容量：最多 16 个任务，第 17 个报容量已满；删除后可再加入。
func TestCapacityFull(t *testing.T) {
	a := NewAnalyzer()
	for i := 0; i < MaxTasks; i++ {
		mustAdd(t, a, fmt.Sprintf("t%02d", i), 4, 0, 4)
	}
	if err := a.AddTask(Task{ID: "extra", Period: 4, Phase: 0, WriteDelay: 4}); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("capacity err=%v", err)
	}
	if err := a.RemoveTask("t00"); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, a, "extra", 4, 0, 4)
}

// 不变量与确定性：MaxAge==MaxReaction；Σw<=Min<=Max；逐 x 下界；重复调用一致。
func TestInvariantsAndDeterminism(t *testing.T) {
	cases := [][][]int{
		{{4, 6}, {0, 1}, {4, 6}},
		{{3, 5, 7}, {1, 2, 0}, {2, 5, 3}},
		{{10, 10}, {3, 7}, {1, 2}},
		{{2, 3, 4, 5}, {0, 1, 2, 3}, {2, 1, 4, 2}},
	}
	for ci, c := range cases {
		p, ph, wd := c[0], c[1], c[2]
		g1 := analyzeChain(p, ph, wd)
		g2 := analyzeChain(p, ph, wd)
		if g1 != g2 {
			t.Fatalf("case %d nondeterministic %+v %+v", ci, g1, g2)
		}
		if g1.MaxAge != g1.MaxReaction {
			t.Fatalf("case %d MaxAge %d != MaxReaction %d", ci, g1.MaxAge, g1.MaxReaction)
		}
		if g1.MinReaction > g1.MaxReaction || g1.MinReaction < sum(wd) {
			t.Fatalf("case %d bounds broken %+v sumW=%d", ci, g1, sum(wd))
		}
		h := hyperperiod(p)
		phi, sumP := 0, 0
		for k, pp := range p {
			if ph[k] > phi {
				phi = ph[k]
			}
			sumP += pp
		}
		for x := phi; x < phi+h; x++ {
			if reactionAt(x, p, ph, wd) < sum(wd) {
				t.Fatalf("case %d reaction at %d < sumW", ci, x)
			}
		}
		w0 := phi + 2*sumP
		for x := w0; x < w0+h; x++ {
			if ageAt(x, p, ph, wd) < sum(wd) {
				t.Fatalf("case %d age at %d < sumW", ci, x)
			}
		}
	}
}

func sum(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

// 并发调用：配合 -race 检测竞争；操作仅在锁内改状态，串行化成立。
func TestConcurrentLinearizability(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, "A", 4, 0, 4)
	mustAdd(t, a, "B", 6, 1, 6)
	mustChain(t, a, "c", "A", "B")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := a.Analyze("c"); err != nil {
					t.Errorf("analyze: %v", err)
					return
				}
				if g%2 == 0 {
					if err := a.SetPhase("B", (g+i)%6); err != nil {
						t.Errorf("setphase: %v", err)
						return
					}
				} else {
					if err := a.SetDelay("B", 6); err != nil {
						t.Errorf("setdelay: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	got, err := a.Analyze("c")
	if err != nil {
		t.Fatal(err)
	}
	if got.MinReaction < 10 || got.MaxReaction > 18 || got.MaxAge != got.MaxReaction {
		t.Fatalf("post-concurrent state invalid %+v", got)
	}
}

// 操作序列重放确定性：同一序列两次构造得到完全一致的分析结果与快照。
func TestReplayDeterminism(t *testing.T) {
	build := func() (*Analysis, []Task) {
		a := NewAnalyzer()
		_ = a.AddTask(Task{ID: "A", Period: 4, Phase: 0, WriteDelay: 4})
		_ = a.AddTask(Task{ID: "B", Period: 6, Phase: 1, WriteDelay: 6})
		_ = a.AddChain("c", []string{"A", "B"})
		_, _ = a.Tune("c")
		_ = a.SetDelay("B", 5)
		g, _ := a.Analyze("c")
		tasks, _ := a.Snapshot()
		return &g, tasks
	}
	g1, t1 := build()
	g2, t2 := build()
	if *g1 != *g2 || fmt.Sprint(t1) != fmt.Sprint(t2) {
		t.Fatalf("replay differs %+v %+v", *g1, *g2)
	}
}
