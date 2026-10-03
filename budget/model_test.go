package budget

import "testing"

func TestSBF(t *testing.T) {
	// Theta = Pi：恒等供给。
	for _, x := range []int64{0, 1, 2, 3, 5, 17, 100} {
		if got := sbf(4, 4, x); got != x {
			t.Fatalf("sbf(4,4,%d)=%d, want %d", x, got, x)
		}
	}
	// Pi=4, Theta=1, a=3：q=floor(max(0,t-3)/4)，sbf=q+max(0,t-6-4q)
	// Pi=4, Theta=1, a=3：q=floor(max(0,t-3)/4)，sbf=q*1+max(0,t-6-4q)
	want := map[int64]int64{1: 0, 2: 0, 3: 0, 4: 0, 5: 0, 6: 0, 7: 1, 8: 1, 9: 1, 10: 1, 11: 2, 12: 2}
	for tt, w := range want {
		if got := sbf(4, 1, tt); got != w {
			t.Fatalf("sbf(4,1,%d)=%d, want %d", tt, got, w)
		}
	}
	// 非周期点：Pi=4,Theta=2,a=2，t=6（恰为 2a 之后的完整周期补给）
	// q=floor((6-2)/4)=1, sbf=2+max(0,6-4-4)=2。
	if got := sbf(4, 2, 6); got != 2 {
		t.Fatalf("sbf(4,2,6)=%d, want 2", got)
	}
	if got := sbf(4, 2, 5); got != 1 {
		t.Fatalf("sbf(4,2,5)=%d, want 1 (5-4=1)", got)
	}
}

func TestDBF(t *testing.T) {
	tk := Task{ID: "x", C: 2, T: 10, D: 6}
	if got := dbfOne(tk, 5); got != 0 {
		t.Fatalf("dbf at t<D = %d, want 0", got)
	}
	if got := dbfOne(tk, 6); got != 2 {
		t.Fatalf("dbf at t=D = %d, want 2 (first job counted exactly)", got)
	}
	if got := dbfOne(tk, 15); got != 2 {
		t.Fatalf("dbf at 15 = %d, want 2", got)
	}
	if got := dbfOne(tk, 16); got != 4 {
		t.Fatalf("dbf at 16 = %d, want 4", got)
	}
}

func TestExampleFromSpec(t *testing.T) {
	// X, Pi=4, x1(C=2,T=10,D=6) => Theta=2。
	r1 := minBudget(4, []Task{{ID: "x1", C: 2, T: 10, D: 6}})
	if !r1.feasible || r1.theta != 2 {
		t.Fatalf("D=6: theta=%d feasible=%v", r1.theta, r1.feasible)
	}
	// D=5 => 线性估计给 2，但 sbf(5)=1 < 2，须 Theta=3。
	r2 := minBudget(4, []Task{{ID: "x1", C: 2, T: 10, D: 5}})
	if !r2.feasible || r2.theta != 3 {
		t.Fatalf("D=5: theta=%d feasible=%v", r2.theta, r2.feasible)
	}
}

func TestInfeasibleViolationPoint(t *testing.T) {
	// C>D 之类非法由 Manager 拦截；构造合法但利用率 >1 的集合：
	// T=2,C=2,D=2 单个任务利用率为 1，再加 T=2,C=1,D=2 => 1.5 > 1。
	tasks := []Task{
		{ID: "a", C: 2, T: 2, D: 2},
		{ID: "b", C: 1, T: 2, D: 2},
	}
	r := minBudget(2, tasks)
	if r.feasible || r.tooLarge {
		t.Fatalf("expected infeasible, got %+v", r)
	}
	if r.violateT != 0 {
		t.Fatalf("utilization-only failure must report t=0, got %d", r.violateT)
	}
	// 仅供给违反：D 很大导致即便 Theta=Pi 也违反不可能（sbf=t），
	// 因为 sbf(t)=t 且 dbf(t) <= t 由 sum C/T<=1 保证；因此 t!=0 的
	// 情形只可能同时伴随利用率问题——此处验证“仅利用率”优先记 0。
}

func TestBinarySearchCheckCount(t *testing.T) {
	for pi := int64(1); pi <= 1000; pi++ {
		resetFeasibilityChecks()
		tasks := []Task{{ID: "a", C: 1, T: 1000, D: 1000}}
		minBudget(pi, tasks)
		used := feasibilityChecks.Load()
		bound := int64(ceilLog2(pi) + 1)
		if used > bound {
			t.Fatalf("pi=%d used %d checks > bound %d", pi, used, bound)
		}
	}
}

func TestJumpPointBoundAndNoIntegerScan(t *testing.T) {
	tasks := []Task{
		{ID: "a", C: 1, T: 3, D: 2},
		{ID: "b", C: 1, T: 5, D: 4},
	}
	_, horizon, ok := checkHorizon(6, tasks)
	if !ok {
		t.Fatalf("horizon unexpectedly too large")
	}
	points := jumpPoints(tasks, horizon)
	bound := jumpPointBound(tasks, horizon)
	if len(points) > bound {
		t.Fatalf("points %d exceed bound %d", len(points), bound)
	}
	// 全部为严格递增跳变点。
	for i := 1; i < len(points); i++ {
		if points[i] <= points[i-1] {
			t.Fatalf("points not strictly increasing: %v", points)
		}
		for _, tk := range tasks {
			if (points[i]-tk.D)%tk.T != 0 && points[i] >= tk.D {
				// 点必须属于某个任务的 D+mT 序列。
			}
		}
	}
	// 朴素核对：收集集合相等。
	naive := map[int64]bool{}
	for _, tk := range tasks {
		for x := tk.D; x <= horizon; x += tk.T {
			naive[x] = true
		}
	}
	if len(naive) != len(points) {
		t.Fatalf("point set mismatch: %v vs %v", points, naive)
	}
}

func TestDeadlineAtSBFStep(t *testing.T) {
	// Pi=4,Theta=2,a=2：sbf 的阶跃点之一 t=6（恰 2a+Pi）。
	// 任务 D=6, C=2, T=10：dbf(6)=2，sbf(4,2,6)=2，恰好可行 => Theta=2。
	r := minBudget(4, []Task{{ID: "z", C: 2, T: 10, D: 6}})
	if !r.feasible || r.theta != 2 {
		t.Fatalf("deadline at step point: theta=%d feasible=%v", r.theta, r.feasible)
	}
	// 同样需求 D=7：sbf(4,2,7)=3 也可行，仍 theta=2；
	// D=5：sbf=1 < 2，需要 3。
	r5 := minBudget(4, []Task{{ID: "z", C: 2, T: 10, D: 5}})
	if r5.theta != 3 {
		t.Fatalf("D=5 theta=%d want 3", r5.theta)
	}
}

func TestSizeTierPointCounts(t *testing.T) {
	// 10^3 档：Dmax+H <= 1000，断言跳变点数不超过 sum(ceil(H'/T_i)) 且不逐点扫描。
	small := []Task{
		{ID: "a", C: 1, T: 12, D: 12},
		{ID: "b", C: 1, T: 30, D: 20},
	}
	_, h1, ok := checkHorizon(10, small)
	if !ok || h1 > 1000 {
		t.Fatalf("small tier horizon=%d ok=%v", h1, ok)
	}
	p1 := jumpPoints(small, h1)
	if len(p1) > jumpPointBound(small, h1) {
		t.Fatalf("small tier point count %d > bound", len(p1))
	}
	// 10^6 档：Dmax+H 恰不超过 1e6（构造互质但乘积可控）。
	// Pi=997, T=1000 => H=997000, Dmax=1000 => 998000 <= 1e6。
	big := []Task{{ID: "a", C: 1, T: 1000, D: 1000}}
	_, h2, ok := checkHorizon(997, big)
	if !ok || h2 != 998000 {
		t.Fatalf("big tier horizon=%d ok=%v", h2, ok)
	}
	p2 := jumpPoints(big, h2)
	if len(p2) > jumpPointBound(big, h2) {
		t.Fatalf("big tier point count %d > bound %d", len(p2), jumpPointBound(big, h2))
	}
	if int64(len(p2)) != h2/1000 {
		t.Fatalf("big tier points=%d want %d", len(p2), h2/1000)
	}
}

func TestTooLargeBoundaryAndManager(t *testing.T) {
	// 恰在 1e6 内可行：Pi=1000, T=997 => H=997000，Dmax=3000 => 1,000,000 恰好。
	r := minBudget(1000, []Task{{ID: "a", C: 1, T: 997, D: 3000}})
	if r.tooLarge || !r.feasible {
		t.Fatalf("horizon exactly 1e6 must be allowed: %+v", r)
	}
	// 超出 1：同任务 D=3001 => Dmax+H=1000001 => tooLarge。
	r2 := minBudget(1000, []Task{{ID: "a", C: 1, T: 997, D: 3001}})
	if !r2.tooLarge {
		t.Fatalf("horizon 1000001 must be too large")
	}
	// LCM 中途溢出式超限：Pi=1000,T=997,T=991 => LCM 远超 1e6，乘法中即停。
	r3 := minBudget(1000, []Task{
		{ID: "a", C: 1, T: 997, D: 10},
		{ID: "b", C: 1, T: 991, D: 10},
	})
	if !r3.tooLarge {
		t.Fatalf("lcm overflow path must report too large")
	}
}

func TestMinBudgetInputOrderIndependent(t *testing.T) {
	tasks := []Task{
		{ID: "a", C: 1, T: 3, D: 3},
		{ID: "b", C: 2, T: 7, D: 5},
		{ID: "c", C: 1, T: 5, D: 4},
	}
	r1 := minBudget(4, tasks)
	r2 := minBudget(4, []Task{tasks[2], tasks[0], tasks[1]})
	if r1.theta != r2.theta || r1.feasible != r2.feasible {
		t.Fatalf("order dependence: %+v vs %+v", r1, r2)
	}
}
