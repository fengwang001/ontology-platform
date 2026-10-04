package budget

import (
	"math/big"
	"math/rand"
	"testing"
)

func TestSBFThetaEqualsPi(t *testing.T) {
	for _, pi := range []int64{1, 2, 4, 7, 1000} {
		for _, tm := range []int64{0, 1, 3, pi, pi + 1, 5 * pi} {
			if got := sbf(pi, pi, tm); got != tm {
				t.Fatalf("sbf(%d,%d,%d)=%d, want %d", pi, pi, tm, got, tm)
			}
		}
	}
}

func TestSBFThetaOne(t *testing.T) {
	// Π=4, Θ=1：a=3，供给曲线为 0,0,0,1,1,1,2,...（t=3,6,7,10,11,...）。
	cases := []struct{ t, want int64 }{
		{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}, {5, 0}, {6, 0},
		{7, 1}, {8, 1}, {9, 1}, {10, 1}, {11, 2}, {14, 2}, {15, 3},
	}
	for _, c := range cases {
		if got := sbf(4, 1, c.t); got != c.want {
			t.Fatalf("sbf(4,1,%d)=%d, want %d", c.t, got, c.want)
		}
	}
}

func TestSBFAtTwoA(t *testing.T) {
	// t 恰在 2a 处：sbf(2a)=max(0, 2a-2a)=0（此时 q=0，因 2a<Π+a 当 a<Π）。
	for pi := int64(2); pi <= 10; pi++ {
		for theta := int64(1); theta < pi; theta++ {
			a := pi - theta
			if got := sbf(pi, theta, 2*a); got != 0 {
				t.Fatalf("sbf(%d,%d,2a=%d)=%d, want 0", pi, theta, 2*a, got)
			}
			// t=2a+1 处供给开始恢复（若 2a+1 > a，即 a>=1 恒成立）。
			if got := sbf(pi, theta, 2*a+1); got != 1 {
				t.Fatalf("sbf(%d,%d,2a+1=%d)=%d, want 1", pi, theta, 2*a+1, got)
			}
		}
	}
}

func TestDBFFirstJobAtDeadline(t *testing.T) {
	tasks := []Task{{ID: "a", C: 2, T: 10, D: 6}}
	if got := dbf(tasks, 5); got != 0 {
		t.Fatalf("dbf(5)=%d, want 0", got)
	}
	if got := dbf(tasks, 6); got != 2 {
		t.Fatalf("dbf(6)=%d, want 2 (t=D 恰计入第一份)", got)
	}
	if got := dbf(tasks, 15); got != 2 {
		t.Fatalf("dbf(15)=%d, want 2", got)
	}
	if got := dbf(tasks, 16); got != 4 {
		t.Fatalf("dbf(16)=%d, want 4", got)
	}
}

func TestDBFAtSBFStepPoint(t *testing.T) {
	// 规格示例：Π=4, Θ=2, 任务 (C=2,T=10,D=6)，t=6 恰为 sbf 阶跃点，sbf=2=dbf 恰可行。
	if got := sbf(4, 2, 6); got != 2 {
		t.Fatalf("sbf(4,2,6)=%d, want 2", got)
	}
	tasks := []Task{{ID: "x1", C: 2, T: 10, D: 6}}
	theta, ok, _ := MinBudget(4, tasks)
	if !ok || theta != 2 {
		t.Fatalf("MinBudget(4, x1)=(%d,%v), want (2,true)", theta, ok)
	}
	// D=5 时 Θ=2 在 t=5 处 sbf=1<2，须 Θ=3（线性估计 t·Θ/Π 会误给 2）。
	if got := sbf(4, 2, 5); got != 1 {
		t.Fatalf("sbf(4,2,5)=%d, want 1", got)
	}
	tasks[0].D = 5
	theta, ok, _ = MinBudget(4, tasks)
	if !ok || theta != 3 {
		t.Fatalf("MinBudget(4, x1 D=5)=(%d,%v), want (3,true)", theta, ok)
	}
}

// linearScanMinBudget 逐个 Θ 线性扫描并对每个整数 t 检查，作为二分实现的对照。
func linearScanMinBudget(pi int64, tasks []Task) (int64, bool) {
	if len(tasks) == 0 {
		return 0, true
	}
	dmax, h, within := horizon(pi, tasks)
	if !within {
		return pi, false
	}
	bound := dmax + h
	for theta := int64(1); theta <= pi; theta++ {
		if !utilizationLE(tasks, theta, pi) {
			continue
		}
		ok := true
		for tm := int64(1); tm <= bound; tm++ {
			if dbf(tasks, tm) > sbf(pi, theta, tm) {
				ok = false
				break
			}
		}
		if ok {
			return theta, true
		}
	}
	return pi, false
}

func TestMinBudgetBinaryMatchesLinearScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 3000; trial++ {
		pi := int64(1 + rng.Intn(8))
		n := 1 + rng.Intn(3)
		tasks := make([]Task, n)
		for i := range tasks {
			tm := int64(1 + rng.Intn(8))
			d := int64(1 + rng.Intn(int(tm)))
			c := int64(1 + rng.Intn(int(d)))
			tasks[i] = Task{ID: string(rune('a' + i)), C: c, T: tm, D: d}
		}
		got, gotOK, _ := MinBudget(pi, tasks)
		want, wantOK := linearScanMinBudget(pi, tasks)
		if gotOK != wantOK || got != want {
			t.Fatalf("pi=%d tasks=%+v: binary=(%d,%v) linear=(%d,%v)", pi, tasks, got, gotOK, want, wantOK)
		}
	}
}

func TestMinBudgetCheckCountBound(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 500; trial++ {
		pi := int64(1 + rng.Intn(1000))
		n := 1 + rng.Intn(4)
		tasks := make([]Task, n)
		for i := range tasks {
			tm := int64(1 + rng.Intn(20))
			d := int64(1 + rng.Intn(int(tm)))
			c := int64(1 + rng.Intn(int(d)))
			tasks[i] = Task{ID: string(rune('a' + i)), C: c, T: tm, D: d}
		}
		resetStats()
		MinBudget(pi, tasks)
		// 上界：⌈log2 Π⌉+1。
		bound := int64(1)
		for (int64(1) << bound) < pi {
			bound++
		}
		bound++
		if stats.feasibilityChecks > bound {
			t.Fatalf("pi=%d: feasibilityChecks=%d > bound=%d", pi, stats.feasibilityChecks, bound)
		}
	}
}

func TestJumpPointCountBound(t *testing.T) {
	// 两档对照：Dmax+H 取 10^6 与 10^3。
	for _, bound := range []int64{1_000_000, 1_000} {
		rng := rand.New(rand.NewSource(99))
		for trial := 0; trial < 50; trial++ {
			n := 1 + rng.Intn(8)
			tasks := make([]Task, n)
			var limit int64
			for i := range tasks {
				tm := int64(1 + rng.Intn(1000))
				d := int64(1 + rng.Intn(int(tm)))
				c := int64(1 + rng.Intn(int(d)))
				tasks[i] = Task{ID: string(rune('a' + i)), C: c, T: tm, D: d}
				limit += (bound + tm - 1) / tm
			}
			points := jumpPoints(tasks, bound)
			if int64(len(points)) > limit {
				t.Fatalf("bound=%d: %d jump points > limit %d", bound, len(points), limit)
			}
			for i := 1; i < len(points); i++ {
				if points[i] <= points[i-1] {
					t.Fatalf("jump points not strictly increasing")
				}
			}
		}
	}
}

func TestMinBudgetOrderIndependent(t *testing.T) {
	tasks := []Task{
		{ID: "a", C: 2, T: 10, D: 6},
		{ID: "b", C: 1, T: 12, D: 9},
		{ID: "c", C: 3, T: 8, D: 8},
	}
	want, _, _ := MinBudget(6, tasks)
	// 反转顺序结果必须一致。
	rev := []Task{tasks[2], tasks[1], tasks[0]}
	got, _, _ := MinBudget(6, rev)
	if got != want {
		t.Fatalf("order dependent: %d vs %d", want, got)
	}
}

func TestInfeasibleViolationPoint(t *testing.T) {
	// 仅因 ΣC/T>1 不可行：violationT=0。
	tasks := []Task{{ID: "a", C: 5, T: 5, D: 5}, {ID: "b", C: 3, T: 6, D: 6}}
	_, ok, vt := MinBudget(4, tasks)
	if ok || vt != 0 {
		t.Fatalf("utilization infeasible: ok=%v vt=%d, want ok=false vt=0", ok, vt)
	}
	// 利用率满足但 sbf 违反：报告 Θ=Π 时最小违反点。
	// 三个 (C=1,T=3,D=1)：ΣC/T=1 恰满足，但 dbf(1)=3 > sbf(1)=1。
	tasks = []Task{
		{ID: "a", C: 1, T: 3, D: 1},
		{ID: "b", C: 1, T: 3, D: 1},
		{ID: "c", C: 1, T: 3, D: 1},
	}
	_, ok, vt = MinBudget(4, tasks)
	if ok || vt != 1 {
		t.Fatalf("sbf infeasible: ok=%v vt=%d, want ok=false vt=1", ok, vt)
	}
}

func TestUtilizationExact(t *testing.T) {
	// 1/2+1/3+1/7 = 41/42 <= 1；再加 1/7 变 48/42 > 1。
	tasks := []Task{
		{ID: "a", C: 1, T: 2, D: 2},
		{ID: "b", C: 1, T: 3, D: 3},
		{ID: "c", C: 1, T: 7, D: 7},
	}
	if !utilizationLE(tasks, 1, 1) {
		t.Fatal("41/42 <= 1 should hold")
	}
	tasks = append(tasks, Task{ID: "d", C: 1, T: 7, D: 7})
	if utilizationLE(tasks, 1, 1) {
		t.Fatal("48/42 <= 1 should not hold")
	}
}

func TestHorizonOverflowGuard(t *testing.T) {
	// 多个互质大周期使 lcm 迅速超过 10^6，须提前停止而不溢出。
	tasks := []Task{
		{ID: "a", C: 1, T: 997, D: 1},
		{ID: "b", C: 1, T: 991, D: 1},
		{ID: "c", C: 1, T: 983, D: 1},
	}
	if _, _, ok := horizon(1000, tasks); ok {
		t.Fatal("expected horizon overflow to be detected")
	}
}

func TestTotalReducedFraction(t *testing.T) {
	m := NewManager()
	if err := m.Declare("X", 4); err != nil {
		t.Fatal(err)
	}
	if err := m.Declare("Y", 6); err != nil {
		t.Fatal(err)
	}
	if got := m.Total(); got.Cmp(big.NewRat(0, 1)) != 0 {
		t.Fatalf("empty total=%v, want 0/1", got)
	}
	if err := m.AddTask("X", Task{ID: "x1", C: 2, T: 10, D: 6}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("Y", Task{ID: "y1", C: 1, T: 12, D: 12}); err != nil {
		t.Fatal(err)
	}
	// θ_X=2/4 + θ_Y=1/6 = 2/3。
	if got := m.Total(); got.Cmp(big.NewRat(2, 3)) != 0 {
		t.Fatalf("total=%v, want 2/3", got)
	}
}
