package compactor

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Selector {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func baseCfg() Config {
	return Config{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2, P: 0}
}

func addN(t *testing.T, s *Selector, now int64, sizes ...int64) {
	t.Helper()
	for _, size := range sizes {
		now++
		if _, err := s.AddRun(now, size); err != nil {
			t.Fatalf("AddRun(%d): %v", size, err)
		}
	}
}

func TestNewConfigValidation(t *testing.T) {
	if _, err := New(baseCfg()); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	cases := []Config{
		{MinRuns: 1, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 4, MaxRuns: 3, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 0, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 1_000_001, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: -1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 10_001, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 1, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 3, MaxMerge: 2, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 0},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2, P: -1},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrParam) {
			t.Fatalf("case %d: want ErrParam, got %v", i, err)
		}
	}
}

// 题目主例子：[3,3,6,50] 走 SizeRatio；Done 后只剩两段且 n<MinRuns。
func TestMainExample(t *testing.T) {
	s := mustNew(t, baseCfg())
	addN(t, s, 0, 50, 6, 3, 3)

	p, err := s.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 1 || p.Reason != ReasonSizeRatio {
		t.Fatalf("unexpected plan: %+v", p)
	}
	if got := p.Runs; len(got) != 3 || got[0] != 4 || got[1] != 3 || got[2] != 2 {
		t.Fatalf("Runs = %v", got)
	}
	if p.Total != 12 {
		t.Fatalf("Total = %d", p.Total)
	}

	if err := s.Done(11, p.ID, 11); err != nil {
		t.Fatal(err)
	}
	rr := s.Runs()
	if len(rr) != 2 || rr[0].ID != 5 || rr[0].Size != 11 || rr[1].ID != 1 || rr[1].Size != 50 {
		t.Fatalf("after Done runs = %+v", rr)
	}
	if rr[0].Created != 11 || rr[0].Fails != 0 || rr[0].Busy {
		t.Fatalf("new run attrs wrong: %+v", rr[0])
	}
	if _, err := s.Pick(12); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("want ErrNotNeeded, got %v", err)
	}
	if err := s.Done(13, p.ID, 1); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Done finished: %v", err)
	}
	if err := s.Abort(p.ID); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Abort finished: %v", err)
	}
	id, err := s.AddRun(14, 1)
	if err != nil || id != 6 {
		t.Fatalf("next id = %d, err = %v", id, err)
	}
}

// E*100 恰等于 A*S 触发 SpaceAmp；差 1 不触发；SpaceAmp 优先于 SizeRatio。
func TestSpaceAmpEqualityAndOffByOne(t *testing.T) {
	eq := mustNew(t, Config{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 0, MinMerge: 3, MaxMerge: 4, Cmax: 2})
	addN(t, eq, 0, 50, 50, 50) // E=100,S=50：10000 == 10000
	p, err := eq.Pick(10)
	if err != nil || p.Reason != ReasonSpaceAmp || len(p.Runs) != 3 || p.Total != 150 {
		t.Fatalf("equality: p=%+v err=%v", p, err)
	}

	off := mustNew(t, Config{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 2})
	addN(t, off, 0, 50, 50, 40) // 新->旧 [40,50,50]：E=90,S=50，9000<10000
	p2, err := off.Pick(10)
	if err != nil || p2.Reason != ReasonSizeRatio {
		t.Fatalf("off-by-one: p=%+v err=%v", p2, err)
	}
	if len(p2.Runs) != 2 || p2.Runs[0] != 2 || p2.Runs[1] != 1 {
		t.Fatalf("SizeRatio picked %v", p2.Runs)
	}

	pri := mustNew(t, baseCfg())
	addN(t, pri, 0, 12, 10, 10, 10) // E=30, S=12：3000 >= 2400
	p3, err := pri.Pick(10)
	if err != nil || p3.Reason != ReasonSpaceAmp || len(p3.Runs) != 4 {
		t.Fatalf("priority: p=%+v err=%v", p3, err)
	}
}

// 有忙运行时 SpaceAmp 让位给 SizeRatio。
func TestSpaceAmpYieldsWhenBusy(t *testing.T) {
	s := mustNew(t, baseCfg())
	addN(t, s, 0, 10, 10, 4, 4)
	p1, err := s.Pick(5)
	if err != nil || p1.Reason != ReasonSizeRatio || len(p1.Runs) != 2 {
		t.Fatalf("setup plan: %+v %v", p1, err)
	}
	if _, err := s.AddRun(6, 10_000); err != nil {
		t.Fatal(err)
	}
	// 最新起点 10000 无法并入 2；两个忙起点跳过；50,50 互相恰等可并入。
	p2, err := s.Pick(7)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Reason != ReasonSizeRatio || len(p2.Runs) != 2 || p2.Runs[0] != 2 || p2.Runs[1] != 1 {
		t.Fatalf("p2=%+v", p2)
	}
}

// SizeRatio 用累计和而非前一个运行比较：Rho=0，[新->旧 2,1,2,...]。
func TestSizeRatioUsesAccumulator(t *testing.T) {
	s := mustNew(t, Config{MinRuns: 3, MaxRuns: 6, A: 1_000_000, Rho: 0, MinMerge: 3, MaxMerge: 4, Cmax: 2})
	addN(t, s, 0, 2, 2, 1, 2)
	p, err := s.Pick(10)
	if err != nil || p.Reason != ReasonSizeRatio {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	// acc 2->3->5，四段全部并入，MaxMerge=4 截断；若只比较前值 1，则 2>1 会停。
	if len(p.Runs) != 4 {
		t.Fatalf("expected all 4 via accumulator, got %v", p.Runs)
	}
}

// size*100 恰等于 acc*(100+Rho) 时延伸。
func TestSizeRatioEqualityExtends(t *testing.T) {
	s := mustNew(t, Config{MinRuns: 2, MaxRuns: 6, A: 1_000_000, Rho: 50, MinMerge: 2, MaxMerge: 4, Cmax: 2})
	addN(t, s, 0, 3, 2) // 3*100 == 2*150
	p, err := s.Pick(10)
	if err != nil || p.Reason != ReasonSizeRatio || len(p.Runs) != 2 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

// 起点为忙时换下一个起点。
func TestSizeRatioSkipsBusyStart(t *testing.T) {
	s := mustNew(t, Config{MinRuns: 2, MaxRuns: 6, A: 1_000_000, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 2})
	addN(t, s, 0, 500, 500, 100, 100, 20, 20) // 新->旧 [20,20,100,100,500,500]
	p1, err := s.Pick(7)
	if err != nil || len(p1.Runs) != 2 || p1.Runs[0] != 6 || p1.Runs[1] != 5 {
		t.Fatalf("setup: %+v %v", p1, err)
	}
	p2, err := s.Pick(8)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Reason != ReasonSizeRatio || len(p2.Runs) != 2 || p2.Runs[0] != 4 || p2.Runs[1] != 3 {
		t.Fatalf("p2=%+v", p2)
	}
}

// 个数恰为 MinMerge 选中；MaxMerge 截断延伸。
func TestSizeRatioMergeBounds(t *testing.T) {
	s := mustNew(t, Config{MinRuns: 3, MaxRuns: 6, A: 1_000_000, Rho: 0, MinMerge: 3, MaxMerge: 3, Cmax: 2})
	addN(t, s, 0, 1000, 100, 10, 10, 10) // 最新三段并入，MaxMerge=3 截断
	p, err := s.Pick(6)
	if err != nil || len(p.Runs) != 3 || p.Runs[0] != 5 {
		t.Fatalf("maxmerge cap: p=%+v err=%v", p, err)
	}
	if err := s.Abort(p.ID); err != nil {
		t.Fatal(err)
	}

	// 恰为 MinMerge=2：[1,1,100]，并入 1 后个数 2，100 断，仍选中。
	s2 := mustNew(t, Config{MinRuns: 2, MaxRuns: 6, A: 1_000_000, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 2})
	addN(t, s2, 0, 100, 10, 10)
	p2, err := s2.Pick(5)
	if err != nil || len(p2.Runs) != 2 || p2.Runs[0] != 3 {
		t.Fatalf("minmerge exact: p=%+v err=%v", p2, err)
	}
}

// CountReduce 的 c 公式与忙窗口隔断。
func TestCountReduceFormulaAndBusyGap(t *testing.T) {
	cfg := Config{MinRuns: 2, MaxRuns: 3, A: 1_000_000, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 2}
	// n=6：c=min(6-3+1,4)=4；全员空闲，选最新 4 个（编号 6,5,4,3）。
	s := mustNew(t, cfg)
	addN(t, s, 0, 1_000_000, 100_000, 10_000, 1_000, 100, 10)
	p, err := s.Pick(7)
	if err != nil || p.Reason != ReasonCountReduce {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if len(p.Runs) != 4 || p.Runs[0] != 6 || p.Runs[3] != 3 {
		t.Fatalf("c formula runs=%v", p.Runs)
	}

	// 忙隔断：5 个运行先让最新 3 段忙，再补 1 个空闲 -> n=6, c=4。
	// 布局（新->旧）：6(闲),5(忙),4(忙),3(忙),2(闲),1(闲)，
	// 三个长度 4 窗口 {6,5,4,3}、{5,4,3,2}、{4,3,2,1} 均含忙 -> ErrNotNeeded。
	s2 := mustNew(t, cfg)
	addN(t, s2, 0, 10_000_000, 1_000_000, 100_000, 10_000, 1_000)
	pb, err := s2.Pick(6)
	if err != nil || pb.Reason != ReasonCountReduce || len(pb.Runs) != 3 {
		t.Fatalf("setup n=5 c=3: p=%+v err=%v", pb, err)
	}
	// pb 选最新 3 个（5,4,3），忙；再补一个空闲。
	if _, err := s2.AddRun(7, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Pick(8); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("all windows blocked: %v", err)
	}
	if err := s2.Abort(pb.ID); err != nil {
		t.Fatal(err)
	}
	// 解除忙后，下一个 Pick 立刻命中 CountReduce 窗口。
	p2, err := s2.Pick(9)
	if err != nil || p2.Reason != ReasonCountReduce || len(p2.Runs) != 4 {
		t.Fatalf("after abort: p=%+v err=%v", p2, err)
	}
}

// Periodic 恰等周期、取最旧者；Done 后 created 被重置。
func TestPeriodic(t *testing.T) {
	cfg := Config{MinRuns: 3, MaxRuns: 6, A: 1_000_000, Rho: 0, MinMerge: 3, MaxMerge: 4, Cmax: 2, P: 10}
	s := mustNew(t, cfg)
	// 严格变大序列避免 SizeRatio 命中；created = 1,2,3。
	addN(t, s, 0, 1000, 100, 10)
	// now=12：三者都满足，取最旧（编号 1）。
	p, err := s.Pick(12)
	if err != nil || p.Reason != ReasonPeriodic || len(p.Runs) != 1 || p.Runs[0] != 1 || p.Total != 1000 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	// 单运行选择器隔离验证 created 重置：前三条规则因 n<MinRuns 全不适用。
	solo := mustNew(t, cfg)
	id, err := solo.AddRun(100, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := solo.Pick(109); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("age 9 must not fire: %v", err)
	}
	sp, err := solo.Pick(110) // now-created=10 恰等
	if err != nil || sp.Reason != ReasonPeriodic || sp.Runs[0] != id || sp.Total != 5 {
		t.Fatalf("age 10 equality: p=%+v err=%v", sp, err)
	}
	if err := solo.Done(110, sp.ID, 9); err != nil {
		t.Fatal(err)
	}
	// 替换运行 created=110：age 9 不成立，age 10 恰等成立，编号递增。
	if _, err := solo.Pick(119); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("created reset, age 9 should not fire: %v", err)
	}
	p4, err := solo.Pick(120)
	if err != nil || p4.Reason != ReasonPeriodic || p4.Total != 9 || p4.Runs[0] != id+1 {
		t.Fatalf("created reset equality: p=%+v err=%v", p4, err)
	}

	// P=0 时周期规则关闭。
	off := mustNew(t, baseCfg())
	addN(t, off, 0, 100, 10)
	if _, err := off.Pick(100); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("P=0 periodic off: %v", err)
	}
}

// Cmax 已满返回 ErrBusy，且先于 ErrNotNeeded。
func TestCmaxBusyPrecedence(t *testing.T) {
	cfg := Config{MinRuns: 2, MaxRuns: 6, A: 1_000_000, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 1, P: 10}
	s := mustNew(t, cfg)
	addN(t, s, 0, 1, 1)
	p, err := s.Pick(5) // 周期未满（created<=1，age<=4），但 SizeRatio 选中
	if err != nil {
		t.Fatal(err)
	}
	// 唯一计划槽位被占。即便四条规则都不成立（Abort 后只剩两个 fails>=2
	// 无法 SizeRatio，周期 age 也不足），仍先报 ErrBusy：构造一个周期不满足的时刻。
	if _, err := s.Pick(6); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v (plan=%+v)", err, p)
	}
}

// Abort 后运行可再次选中；第二次 Abort 后 fails=2，SizeRatio 不再选它们，
// 但 SpaceAmp 不看 fails 仍可选全部。
func TestAbortFailsLifecycle(t *testing.T) {
	cfg := Config{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 2}
	s := mustNew(t, cfg)
	// 新->旧 [1,1,1,50]：SpaceAmp E=3,S=50 不成立；SizeRatio 合并最新三段 1。
	addN(t, s, 0, 50, 1, 1, 1)
	p, err := s.Pick(5)
	if err != nil || p.Reason != ReasonSizeRatio || len(p.Runs) != 3 {
		t.Fatalf("first pick: p=%+v err=%v", p, err)
	}
	// 第一次 Abort：fails=1，同样的段仍可被 SizeRatio 选中。
	if err := s.Abort(p.ID); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Runs() {
		if r.ID >= 2 && r.ID <= 4 && (r.Busy || r.Fails != 1) {
			t.Fatalf("after abort: %+v", r)
		}
	}
	p2, err := s.Pick(6)
	if err != nil || p2.Reason != ReasonSizeRatio {
		t.Fatalf("re-pick after 1 abort: p=%+v err=%v", p2, err)
	}
	if err := s.Abort(p2.ID); err != nil {
		t.Fatal(err)
	}
	// fails=2：三段 fails>=2 起点全跳过，50 单点不足 MinMerge。
	if _, err := s.Pick(7); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("fails=2 should block SizeRatio: %v", err)
	}
	// 加入巨大新运行触发 SpaceAmp：最旧仍是 50，E=10003，10003*100 >= 200*50。
	// SpaceAmp 不看 fails，五段全部选中，含两次失败的旧三段。
	if _, err := s.AddRun(8, 10_000); err != nil {
		t.Fatal(err)
	}
	p3, err := s.Pick(9)
	if err != nil || p3.Reason != ReasonSpaceAmp || len(p3.Runs) != 5 {
		t.Fatalf("spaceamp ignores fails=2: p=%+v err=%v", p3, err)
	}
}

func TestClockAndParamOrdering(t *testing.T) {
	s := mustNew(t, baseCfg())
	addN(t, s, 0, 1, 1, 1)
	p0, err := s.Pick(5)
	if err != nil {
		t.Fatal(err)
	}
	// 时钟倒退报 ErrClock。
	if _, err := s.Pick(4); !errors.Is(err, ErrClock) {
		t.Fatalf("pick clock: %v", err)
	}
	if _, err := s.AddRun(4, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("add clock: %v", err)
	}
	if err := s.Done(4, 1, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("done clock: %v", err)
	}
	// now 相等允许：先结束计划，再以相同 now 重选。
	if err := s.Abort(p0.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pick(5); err != nil {
		t.Fatalf("equal now rejected: %v", err)
	}
	// 参数错误先于时钟错误。
	if _, err := s.AddRun(4, 0); !errors.Is(err, ErrParam) {
		t.Fatalf("param before clock add: %v", err)
	}
	if _, err := s.Pick(-1); !errors.Is(err, ErrParam) {
		t.Fatalf("pick negative now: %v", err)
	}
	if err := s.Done(4, 1, 0); !errors.Is(err, ErrParam) {
		t.Fatalf("done param before clock: %v", err)
	}
	if err := s.Done(5, 999, 1); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown plan: %v", err)
	}
	if err := s.Abort(0); !errors.Is(err, ErrParam) {
		t.Fatalf("abort param: %v", err)
	}
	if err := s.Abort(999); !errors.Is(err, ErrUnknown) {
		t.Fatalf("abort unknown: %v", err)
	}
	// 被拒绝操作不推进时钟：4 仍被视为倒退。
	if _, err := s.AddRun(4, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("clock advanced by rejected call: %v", err)
	}
}

func TestConcurrentSafety(t *testing.T) {
	s := mustNew(t, Config{MinRuns: 2, MaxRuns: 3, A: 1_000_000, Rho: 0, MinMerge: 2, MaxMerge: 4, Cmax: 4})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := int64(g*1000 + i + 1)
				if _, err := s.AddRun(now, int64((i%5)+1)); err != nil &&
					!errors.Is(err, ErrClock) {
					t.Errorf("add: %v", err)
					return
				}
				if p, err := s.Pick(now + 1); err == nil {
					if i%2 == 0 {
						_ = s.Done(now+2, p.ID, int64((i%3)+1))
					} else {
						_ = s.Abort(p.ID)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// 高并发混合压力：大量 goroutine 交错 AddRun/Pick/Done/Abort，
// 以 race 检测器保证线性化安全；最终不变量：忙运行数等于未结束计划覆盖数，
// 活动计划计数不超过 Cmax。
func TestConcurrentStress(t *testing.T) {
	cfg := Config{MinRuns: 2, MaxRuns: 4, A: 500, Rho: 50, MinMerge: 2, MaxMerge: 3, Cmax: 3, P: 5}
	s := mustNew(t, cfg)
	var wg sync.WaitGroup
	var clock sync.Mutex
	var last int64
	advance := func() int64 {
		clock.Lock()
		defer clock.Unlock()
		last += int64(1 + (last % 3))
		return last
	}
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				now := advance()
				switch i % 4 {
				case 0:
					_, _ = s.AddRun(now, int64(1+(g+i)%97))
				case 1:
					if p, err := s.Pick(now); err == nil {
						_ = s.Done(advance(), p.ID, int64(1+(g+i)%50))
					}
				case 2:
					if p, err := s.Pick(now); err == nil {
						_ = s.Abort(p.ID)
					}
				case 3:
					_ = s.Runs()
				}
			}
		}(g)
	}
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active < 0 || s.active > cfg.Cmax {
		t.Fatalf("active = %d", s.active)
	}
	busy := 0
	for _, r := range s.runs {
		if r.Busy {
			busy++
		}
	}
	covered := 0
	for _, p := range s.plans {
		covered += len(p.runs)
	}
	if busy != covered {
		t.Fatalf("busy=%d but plan-covered=%d", busy, covered)
	}
}
