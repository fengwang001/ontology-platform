package budget

import (
	"math/big"
	"math/rand"
	"testing"
)

// naiveSBF 按题面“逐步朴素模拟”的约定求最坏供给：
//
//	时间线半无限（周期 k >= 0）；每个周期内枚举 Theta 个供给时隙的放置
//	（时隙时刻 a + k*Pi + x，a = Pi-Theta）；窗口起点 s 枚举 0..Pi-1；
//	窗口 [s, s+t) 左闭右开；sbf(t) 为全部放置与起点下供给时隙数的最小值。
//
// 该穷举与闭式公式 q*Theta + max(0, t-2a-q*Pi) 逐点一致。
func naiveSBF(pi, theta, t int64) int64 {
	if t <= 0 {
		return 0
	}
	if theta == pi {
		return t
	}
	a := pi - theta
	var subsets [][]int64
	var comb func(start, k int64, cur []int64)
	comb = func(start, k int64, cur []int64) {
		if k == 0 {
			cp := make([]int64, len(cur))
			copy(cp, cur)
			subsets = append(subsets, cp)
			return
		}
		for x := start; x <= pi-k; x++ {
			comb(x+1, k-1, append(cur, x))
		}
	}
	comb(0, theta, nil)

	periods := t/pi + 2
	worst := t
	for _, set := range subsets {
		slots := make(map[int64]bool)
		for p := int64(0); p <= periods; p++ {
			for _, x := range set {
				slots[a+p*pi+x] = true
			}
		}
		for s := int64(0); s < pi; s++ {
			var cnt int64
			for slot := range slots {
				if s <= slot && slot < s+t {
					cnt++
				}
			}
			if cnt < worst {
				worst = cnt
			}
		}
	}
	return worst
}

func TestSBFNaiveExhaustive(t *testing.T) {
	for pi := int64(1); pi <= 4; pi++ {
		for theta := int64(1); theta <= pi; theta++ {
			for tt := int64(1); tt <= 40; tt++ {
				got, want := sbf(pi, theta, tt), naiveSBF(pi, theta, tt)
				if got != want {
					t.Fatalf("pi=%d theta=%d t=%d formula=%d naive=%d", pi, theta, tt, got, want)
				}
			}
		}
	}
}

// naiveMinBudget 逐个整数 t 扫描 [1, Dmax+H]，逐个 Theta 从 1..Pi 试探，
// 使用穷举 sbf。返回 (theta, feasible, violateTAtPi)。
func naiveMinBudget(t *testing.T, pi int64, tasks []Task) (int64, bool, int64) {
	if len(tasks) == 0 {
		return 0, true, 0
	}
	_, horizon, ok := checkHorizon(pi, tasks)
	if !ok {
		t.Fatalf("naive: horizon too large unexpectedly")
	}
	utilOK := func(theta int64) bool {
		sum := new(big.Rat)
		for _, tk := range tasks {
			sum.Add(sum, bigRat(tk.C, tk.T))
		}
		return sum.Cmp(bigRat(theta, pi)) <= 0
	}
	if !utilOK(pi) {
		return pi, false, 0
	}
	for tt := int64(1); tt <= horizon; tt++ {
		if dbf(tasks, tt) > naiveSBF(pi, pi, tt) {
			return pi, false, tt
		}
	}
	for theta := int64(1); theta <= pi; theta++ {
		if !utilOK(theta) {
			continue
		}
		good := true
		for tt := int64(1); tt <= horizon; tt++ {
			if dbf(tasks, tt) > naiveSBF(pi, theta, tt) {
				good = false
				break
			}
		}
		if good {
			return theta, true, 0
		}
	}
	return pi, false, 0
}

func TestMinBudgetNaiveSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 300; iter++ {
		pi := int64(1 + rng.Intn(4))
		n := 1 + rng.Intn(3)
		tasks := randTasks(rng, n, 6)
		got := minBudget(pi, tasks)
		ntheta, nfeas, nbad := naiveMinBudget(t, pi, tasks)
		if got.feasible != nfeas {
			t.Fatalf("iter=%d feasible mismatch pi=%d tasks=%+v got=%+v", iter, pi, tasks, got)
		}
		if got.feasible && got.theta != ntheta {
			t.Fatalf("iter=%d theta mismatch pi=%d tasks=%+v got=%d naive=%d", iter, pi, tasks, got.theta, ntheta)
		}
		if !got.feasible && got.violateT != nbad {
			t.Fatalf("iter=%d violate t mismatch got=%d naive=%d", iter, got.violateT, nbad)
		}
	}
}
