package sequential

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// naiveSim 是按规范文字逐步写成的朴素模拟器（大整数），用于对照被测实现。
type naiveSim struct {
	rA, rB, nmin, minStep, nmax, tf, tau int64
	table                                []int64

	stopped      bool
	looks        int
	lastCountedN int64
	lastConcl    Conclusion
	lastNA       int64
	lastCA       int64
	lastNB       int64
	lastCB       int64
}

func newNaiveSim(rA, rB, nmin, minStep, nmax int64, table []int64, tf, tau int64) *naiveSim {
	tbl := make([]int64, len(table))
	copy(tbl, table)
	return &naiveSim{rA: rA, rB: rB, nmin: nmin, minStep: minStep, nmax: nmax, table: tbl, tf: tf, tau: tau}
}

// look 返回结论、判定依据与拒绝原因。
func (s *naiveSim) look(nA, cA, nB, cB int64) (Conclusion, string, error) {
	if nA < 0 || nA > 1_000_000 || cA < 0 || cA > nA || nB < 0 || nB > 1_000_000 || cB < 0 || cB > nB {
		return s.lastConcl, "数值越界", ErrInvalidParam
	}
	if s.stopped {
		return s.lastConcl, "已停止", ErrStopped
	}
	if nA < s.lastNA || cA < s.lastCA || nB < s.lastNB || cB < s.lastCB {
		return s.lastConcl, "数据回退", ErrRegression
	}
	s.lastNA, s.lastCA, s.lastNB, s.lastCB = nA, cA, nB, cB

	n := nA + nB
	c := cA + cB
	d := cB*nA - cA*nB

	if n >= 2*s.nmin {
		dev := nA*s.rB - nB*s.rA
		if dev < 0 {
			dev = -dev
		}
		if dev*100 > s.tau*(nA*s.rB+nB*s.rA) {
			s.stopped = true
			s.lastConcl = Imbalance
			return Imbalance, fmt.Sprintf("比例偏差 %d*100 > tau*(%d)", dev, nA*s.rB+nB*s.rA), nil
		}
	}
	if nA < s.nmin || nB < s.nmin {
		s.lastConcl = Continue
		return Continue, "样本不足", nil
	}
	if n-s.lastCountedN < s.minStep {
		s.lastConcl = Continue
		return Continue, fmt.Sprintf("增量 %d < minStep，仅观察", n-s.lastCountedN), nil
	}

	s.looks++
	s.lastCountedN = n
	idx := s.looks
	if idx > len(s.table) {
		idx = len(s.table)
	}
	t := s.table[idx-1]
	reason := fmt.Sprintf("第 %d 次有效检视, T_%d=%d", s.looks, idx, t)

	var significant, futileStat bool
	if c == 0 || c == n {
		significant = false
		futileStat = s.tf > 0
		reason += ", z^2 未定义按 0 处理"
	} else {
		lhs := new(big.Int).Mul(big.NewInt(d), big.NewInt(d))
		lhs.Mul(lhs, big.NewInt(n))
		lhs.Mul(lhs, big.NewInt(100))
		rhs := new(big.Int).Mul(big.NewInt(nA), big.NewInt(nB))
		rhs.Mul(rhs, big.NewInt(c))
		rhs.Mul(rhs, big.NewInt(n-c))
		significant = lhs.Cmp(new(big.Int).Mul(big.NewInt(t), rhs)) >= 0
		futileStat = lhs.Cmp(new(big.Int).Mul(big.NewInt(s.tf), rhs)) < 0
		reason += fmt.Sprintf(", lhs=%s, rhs=%s", lhs.String(), rhs.String())
	}

	finish := func(concl Conclusion, why string) (Conclusion, string, error) {
		s.stopped = true
		s.lastConcl = concl
		return concl, reason + ", " + why, nil
	}
	if significant {
		if d > 0 {
			return finish(Win, "显著且 D>0")
		}
		return finish(Lose, "显著且 D<0")
	}
	if n >= s.nmax {
		return finish(Futile, "n>=nmax")
	}
	if 2*n >= s.nmax && futileStat {
		return finish(Futile, "穿越无效边界")
	}
	s.lastConcl = Continue
	return Continue, reason + ", 未达任何边界", nil
}

func (s *naiveSim) status() Status {
	return Status{Stopped: s.stopped, Looks: s.looks, LastCountedN: s.lastCountedN, LastConclusion: s.lastConcl}
}

// 2000 组随机检视序列与朴素模拟对照，日志打印输入、输出与判定依据。
func TestRandomSequencesAgainstNaiveSim(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	tally := map[Conclusion]int{}
	for trial := 0; trial < 2000; trial++ {
		rA := int64(rng.Intn(3) + 1)
		rB := int64(rng.Intn(3) + 1)
		nmin := int64(rng.Intn(500) + 1)
		minStep := int64(rng.Intn(2000) + 1)
		nmax := int64(rng.Intn(2_000_000-1) + 2)
		k := rng.Intn(8) + 1
		table := make([]int64, k)
		for i := range table {
			table[i] = int64(rng.Intn(1_000_000) + 1)
		}
		tf := int64(rng.Intn(1_000_001))
		tau := int64(rng.Intn(101))

		judge, err := NewJudge(rA, rB, nmin, minStep, nmax, table, tf, tau)
		if err != nil {
			t.Fatalf("trial %d: NewJudge 报错: %v", trial, err)
		}
		sim := newNaiveSim(rA, rB, nmin, minStep, nmax, table, tf, tau)
		t.Logf("trial %d: rA=%d rB=%d nmin=%d minStep=%d nmax=%d T=%v Tf=%d tau=%d",
			trial, rA, rB, nmin, minStep, nmax, table, tf, tau)

		var nA, cA, nB, cB int64
		steps := rng.Intn(25) + 5
		for step := 0; step < steps; step++ {
			switch rng.Intn(20) {
			case 0: // 非法数值
				nA2, cA2, nB2, cB2 := nA, cA, nB, cB
				switch rng.Intn(4) {
				case 0:
					nA2 = -1
				case 1:
					cA2 = nA2 + 1
				case 2:
					nB2 = 1_000_001
				case 3:
					cB2 = -1
				}
				checkLook(t, trial, step, judge, sim, nA2, cA2, nB2, cB2)
				continue
			case 1: // 数据回退
				if nA > 0 {
					checkLook(t, trial, step, judge, sim, nA-1, cA, nB, cB)
					continue
				}
			}
			// 单调递增的正常检视。
			nA += int64(rng.Intn(3000))
			if nA > 1_000_000 {
				nA = 1_000_000
			}
			nB += int64(rng.Intn(3000))
			if nB > 1_000_000 {
				nB = 1_000_000
			}
			cA += int64(rng.Intn(1500))
			if cA > nA {
				cA = nA
			}
			cB += int64(rng.Intn(1500))
			if cB > nB {
				cB = nB
			}
			concl := checkLook(t, trial, step, judge, sim, nA, cA, nB, cB)
			tally[concl]++
		}
		if got, want := judge.Status(), sim.status(); got != want {
			t.Fatalf("trial %d: 最终状态不一致: 实现 %+v, 模拟 %+v", trial, got, want)
		}
	}
	t.Logf("结论分布: %v", tally)
}

func checkLook(t *testing.T, trial, step int, judge *Judge, sim *naiveSim, nA, cA, nB, cB int64) Conclusion {
	t.Helper()
	gotC, gotErr := judge.Look(nA, cA, nB, cB)
	wantC, reason, wantErr := sim.look(nA, cA, nB, cB)
	t.Logf("trial %d step %d: Look(%d,%d,%d,%d) => %s, err=%v, 依据: %s",
		trial, step, nA, cA, nB, cB, gotC, gotErr, reason)
	if gotC != wantC || gotErr != wantErr {
		t.Fatalf("trial %d step %d: Look(%d,%d,%d,%d) 实现=(%s,%v) 模拟=(%s,%v)",
			trial, step, nA, cA, nB, cB, gotC, gotErr, wantC, wantErr)
	}
	if got, want := judge.Status(), sim.status(); got != want {
		t.Fatalf("trial %d step %d: 状态不一致: 实现 %+v, 模拟 %+v", trial, step, got, want)
	}
	return gotC
}
