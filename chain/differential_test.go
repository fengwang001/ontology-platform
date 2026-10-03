package chain

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// pickPeriods 生成链长 2..6 的周期，使 H<=5000，并尽量覆盖各种同余关系。
func pickPeriods(rng *rand.Rand) []int {
	n := 2 + rng.Intn(5)
	small := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 15, 16, 20, 24, 25, 30}
	for attempt := 0; attempt < 200; attempt++ {
		ps := make([]int, n)
		for i := range ps {
			if rng.Intn(3) == 0 {
				ps[i] = small[rng.Intn(len(small))]
			} else {
				ps[i] = 1 + rng.Intn(40)
			}
		}
		if hyperperiod(ps) <= MaxHyperperiod {
			return ps
		}
	}
	// 回退：全部相同周期
	p := 1 + rng.Intn(20)
	ps := make([]int, n)
	for i := range ps {
		ps[i] = p
	}
	return ps
}

// TestDifferentialRandom 与朴素事件传播模拟器对拍 2000 组随机链与相位，
// 同时校验全部不变量。日志逐条打印输入、双方输出与判定依据，种子固定可复现。
func TestDifferentialRandom(t *testing.T) {
	const casesN = 2000
	const seed = 20261003

	logFile, err := os.Create("differential.log")
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()
	fmt.Fprintf(w, "# differential test seed=%d cases=%d\n", seed, casesN)

	rng := rand.New(rand.NewSource(seed))
	passed := 0
	for c := 0; c < casesN; c++ {
		ps := pickPeriods(rng)
		n := len(ps)
		ph := make([]int, n)
		wd := make([]int, n)
		for i := range ps {
			ph[i] = rng.Intn(ps[i])
			// 1/4 概率给 w<T（隐式通信），否则 LET(w=T)
			if rng.Intn(4) == 0 && ps[i] > 1 {
				wd[i] = 1 + rng.Intn(ps[i]-1)
			} else {
				wd[i] = ps[i]
			}
		}

		fast := analyzeChain(ps, ph, wd)
		naive := naiveAnalysis(ps, ph, wd)

		sumW := 0
		for _, x := range wd {
			sumW += x
		}
		reasons := ""
		ok := true
		addReason := func(s string) {
			ok = false
			if reasons != "" {
				reasons += "; "
			}
			reasons += s
		}
		if fast != naive {
			addReason(fmt.Sprintf("fast %+v != naive %+v", fast, naive))
		}
		if fast.MaxAge != fast.MaxReaction {
			addReason("MaxAge != MaxReaction")
		}
		if fast.MinReaction < sumW {
			addReason(fmt.Sprintf("MinReaction %d < sumW %d", fast.MinReaction, sumW))
		}
		if fast.MinReaction > fast.MaxReaction {
			addReason("MinReaction > MaxReaction")
		}
		h := hyperperiod(ps)
		phi, sumP := 0, 0
		for i, p := range ps {
			if ph[i] > phi {
				phi = ph[i]
			}
			sumP += p
		}
		for x := phi; x < phi+h; x++ {
			if r := reactionAt(x, ps, ph, wd); r < sumW {
				addReason(fmt.Sprintf("Reaction(%d)=%d < sumW", x, r))
				break
			}
		}
		w0 := phi + 2*sumP
		for x := w0; x < w0+h; x++ {
			if g := ageAt(x, ps, ph, wd); g < sumW {
				addReason(fmt.Sprintf("Age(%d)=%d < sumW", x, g))
				break
			}
		}

		verdict := "PASS"
		if !ok {
			verdict = "FAIL: " + reasons
		}
		fmt.Fprintf(w, "case=%d input={T=%v phi=%v w=%v Phi=%d H=%d W=%d sumW=%d} fast={maxR=%d minR=%d maxAge=%d} naive={maxR=%d minR=%d maxAge=%d} verdict=%s\n",
			c, ps, ph, wd, phi, h, w0, sumW,
			fast.MaxReaction, fast.MinReaction, fast.MaxAge,
			naive.MaxReaction, naive.MinReaction, naive.MaxAge, verdict)

		if !ok {
			w.Flush()
			t.Fatalf("case %d: %s", c, reasons)
		}
		passed++
	}
	fmt.Fprintf(w, "# summary: %d/%d passed\n", passed, casesN)
	t.Logf("differential: %d/%d passed; log=chain/differential.log", passed, casesN)
}

// independentTune 不依赖 Analyzer.Tune：独立按字典序穷举 τ2..τn 相位，
// 返回最优 MaxReaction 与字典序最小的最优相位向量（含当前相位参与比较）。
func independentTune(ps, ph, wd []int) (int, []int) {
	n := len(ps)
	bestMax := analyzeChain(ps, ph, wd).MaxReaction
	bestPhases := append([]int(nil), ph...)
	cand := append([]int(nil), ph...)
	var enum func(idx int)
	enum = func(idx int) {
		if idx == n {
			m := analyzeChain(ps, cand, wd).MaxReaction
			if m < bestMax {
				bestMax = m
				bestPhases = append([]int(nil), cand...)
			}
			return
		}
		for v := 0; v < ps[idx]; v++ {
			cand[idx] = v
			enum(idx + 1)
		}
		cand[idx] = ph[idx]
	}
	enum(1)
	return bestMax, bestPhases
}

// TestTuneDifferential 对乘积<=1000 的随机链，校验 Tune 的搜索结果与独立穷举一致，
// 提交/不提交规则正确，且未提交时“调整后”值等于调整前。
func TestTuneDifferential(t *testing.T) {
	const casesN = 300
	const seed = 424242

	logFile, err := os.Create("tune_differential.log")
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()
	fmt.Fprintf(w, "# tune differential seed=%d cases=%d\n", seed, casesN)

	rng := rand.New(rand.NewSource(seed))
	passed := 0
	for c := 0; c < casesN; c++ {
		ps := pickPeriods(rng)
		n := len(ps)
		ph := make([]int, n)
		wd := make([]int, n)
		for i := range ps {
			ph[i] = rng.Intn(ps[i])
			wd[i] = ps[i]
			if rng.Intn(3) == 0 && ps[i] > 1 {
				wd[i] = 1 + rng.Intn(ps[i]-1)
			}
		}
		prod := 1
		for k := 1; k < n; k++ {
			prod *= ps[k]
		}
		if prod > MaxTuneProduct {
			c--
			continue
		}

		a := NewAnalyzer()
		ids := make([]string, n)
		for i := range ps {
			ids[i] = fmt.Sprintf("t%d", i)
			if err := a.AddTask(Task{ID: ids[i], Period: ps[i], Phase: ph[i], WriteDelay: wd[i]}); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.AddChain("c", ids); err != nil {
			t.Fatal(err)
		}

		before, _ := a.Analyze("c")
		res, err := a.Tune("c")
		if err != nil {
			t.Fatalf("case %d tune: %v", c, err)
		}
		expMax, expPh := independentTune(ps, ph, wd)
		after, _ := a.Analyze("c")

		ok := true
		reasons := ""
		check := func(cond bool, s string) {
			if !cond {
				ok = false
				if reasons != "" {
					reasons += "; "
				}
				reasons += s
			}
		}
		check(res.BeforeMaxReaction == before.MaxReaction, "before mismatch")
		check(res.AfterMaxReaction == expMax, fmt.Sprintf("after %d != exp %d", res.AfterMaxReaction, expMax))
		check(fmt.Sprint(res.AfterPhases) == fmt.Sprint(expPh),
			fmt.Sprintf("phases %v != exp %v", res.AfterPhases, expPh))
		check(after.MaxReaction == res.AfterMaxReaction, "post-analyze mismatch")
		check(res.Changed == (expMax < before.MaxReaction), "changed flag mismatch")
		if !res.Changed {
			check(fmt.Sprint(res.AfterPhases) == fmt.Sprint(res.BeforePhases), "unchanged but phases differ")
			check(res.AfterMaxReaction == res.BeforeMaxReaction, "unchanged but max differs")
		}

		verdict := "PASS"
		if !ok {
			verdict = "FAIL: " + reasons
		}
		fmt.Fprintf(w, "case=%d input={T=%v phi=%v w=%v prod=%d} before=%d after=%d changed=%v expectPhases=%v verdict=%s\n",
			c, ps, ph, wd, prod, before.MaxReaction, res.AfterMaxReaction, res.Changed, expPh, verdict)
		if !ok {
			w.Flush()
			t.Fatalf("case %d: %s", c, reasons)
		}
		passed++
	}
	fmt.Fprintf(w, "# summary: %d/%d passed\n", passed, casesN)
	t.Logf("tune differential: %d/%d passed; log=chain/tune_differential.log", passed, casesN)
}
