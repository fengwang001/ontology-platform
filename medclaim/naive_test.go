package medclaim

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// 随机生成一张保单（参数覆盖 0 免赔、0/100 比例、0 封顶等边界）。
func randomPolicy(rng *rand.Rand, id string) *Policy {
	codes := []string{"A", "B", "C", "X", "Y", "Z"}
	excl := map[string]struct{}{}
	for _, c := range codes[4:] {
		if rng.Intn(2) == 0 {
			excl[c] = struct{}{}
		}
	}
	return &Policy{
		PolicyID:           id,
		InceptionDay:       rng.Intn(5),
		YearLength:         1 + rng.Intn(40),
		PerClaimDeductible: rng.Intn(300),
		AnnualDeductCap:    rng.Intn(800),
		InpatientRatio:     rng.Intn(101),
		OutpatientRatio:    rng.Intn(101),
		AnnualOOPCap:       rng.Intn(1500),
		ExcludedCodes:      excl,
	}
}

func randomClaim(rng *rand.Rand, id string, policy *Policy) *Claim {
	n := 1 + rng.Intn(6)
	items := make([]Item, n)
	codes := []string{"A", "B", "C", "X", "Y", "Z"}
	for i := range items {
		cat := CategoryInpatient
		if rng.Intn(2) == 1 {
			cat = CategoryOutpatient
		}
		items[i] = Item{Code: codes[rng.Intn(len(codes))], Category: cat, Amount: 1 + rng.Intn(900)}
	}
	day := policy.InceptionDay + rng.Intn(policy.YearLength*3)
	return &Claim{PolicyID: policy.PolicyID, ClaimID: id, EventDay: day, Items: items}
}

func sameTotals(a, b map[int][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sameResult(a, b *SettlementResult) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.InsurerPaid != b.InsurerPaid || a.OOP != b.OOP || a.DeductibleUsed != b.DeductibleUsed {
		return false
	}
	if len(a.Items) != len(b.Items) {
		return false
	}
	for i := range a.Items {
		x, y := a.Items[i], b.Items[i]
		if x.Deductible != y.Deductible || x.InsurerPaid != y.InsurerPaid ||
			x.OOP != y.OOP || x.Excluded != y.Excluded || x.CapTruncated != y.CapTruncated {
			return false
		}
	}
	return true
}

// 对照朴素模型跑随机"提交/撤销"序列，逐操作比较错误码、单笔结果与全部年度累计，
// 并打印输入、输出与判定依据日志。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 60; iter++ {
		p := randomPolicy(rng, "P")
		eng := NewEngine()
		if err := eng.RegisterPolicy(p); err != nil {
			t.Fatal(err)
		}
		nv := newNaiveEngine()
		nv.register(p)

		live := []string{} // 生效中的理赔号（受理次序）
		counter := 0
		for op := 0; op < 120; op++ {
			if len(live) > 0 && rng.Intn(3) == 0 {
				// 以 1/2 概率撤末笔、1/2 概率撤随机笔（验证非末笔拒绝）
				var target string
				if rng.Intn(2) == 0 {
					target = live[len(live)-1]
				} else {
					target = live[rng.Intn(len(live))]
				}
				e1 := eng.Cancel("P", target)
				e2 := nv.cancel("P", target)
				t.Logf("[iter%d op%d] Cancel %s -> err1=%v err2=%v", iter, op, target, codeOf(e1), codeOf(e2))
				if codeOf(e1) != codeOf(e2) {
					t.Fatalf("撤销错误码不一致 %v vs %v", e1, e2)
				}
				if e1 == nil {
					live = removeStr(live, target)
				}
				continue
			}

			counter++
			c := randomClaim(rng, fmt.Sprintf("c%d", counter), p)
			t.Logf("[iter%d op%d] %s", iter, op, describeClaim(c))
			r1, e1 := eng.Submit(c)
			r2, e2 := nv.submit(c)
			if codeOf(e1) != codeOf(e2) {
				t.Fatalf("提交错误码不一致: 引擎=%v 朴素=%v", e1, e2)
			}
			if e1 != nil {
				t.Logf("    拒绝: %v（码 %d）", e1, codeOf(e1))
				continue
			}
			if !sameResult(r1, r2) {
				t.Fatalf("单笔结果不一致\n引擎: %s\n朴素: %s",
					describeResult(r1), describeResult(r2))
			}
			got := map[int][2]int{}
			// 引擎侧逐年读取
			for yi := range eng.policies["P"].accs {
				day := p.InceptionDay + yi*p.YearLength
				d, o, _ := eng.YearTotals("P", day)
				got[yi] = [2]int{d, o}
			}
			want := nv.totals("P")
			if !sameTotals(got, want) {
				t.Fatalf("年度累计不一致\n引擎: %v\n朴素: %v", got, want)
			}
			t.Logf("    %s | 判定依据: %s", describeResult(r1), why(p, r1))
			live = append(live, c.ClaimID)
		}
	}
}

func codeOf(err error) ErrCode {
	if err == nil {
		return 0
	}
	return ErrorCode(err)
}

func removeStr(xs []string, s string) []string {
	for i, v := range xs {
		if v == s {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

// why 给出该笔结果的关键判定依据文本。
func why(p *Policy, r *SettlementResult) string {
	s := fmt.Sprintf("本次免赔%d", r.DeductibleUsed)
	for _, ir := range r.Items {
		if ir.CapTruncated {
			s += "；自付封顶在本条截断，其后全额"
			break
		}
	}
	if r.DeductibleUsed == 0 {
		s += "；免赔为0（上限已满或已达自付封顶）"
	}
	_ = p
	return s
}

// 规模对照：在 1k 与 100k 笔历史（跨多个年度）的预热数据之上，
// 度量再提交一笔"新的理赔"的耗时，证明开销不随历史笔数/年度数增长。
func BenchmarkSubmitIndependentOfHistory(b *testing.B) {
	sizes := []int{1_000, 100_000}
	for _, n := range sizes {
		e := NewEngine()
		p := &Policy{
			PolicyID: "P", InceptionDay: 0, YearLength: 7,
			PerClaimDeductible: 100, AnnualDeductCap: 1_000_000_000,
			InpatientRatio: 80, OutpatientRatio: 60,
			AnnualOOPCap:  1_000_000_000,
			ExcludedCodes: map[string]struct{}{},
		}
		if err := e.RegisterPolicy(p); err != nil {
			b.Fatal(err)
		}
		// 预热 n 笔历史，年度数 = n/7 量级，且每年度不断新增。
		for i := 0; i < n; i++ {
			if _, err := e.Submit(&Claim{
				PolicyID: "P", ClaimID: fmt.Sprintf("h%d", i),
				EventDay: i * 3, // 跨很多年度
				Items:    []Item{inp("A", 500), outp("B", 200)},
			}); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			for k := 0; k < b.N; k++ {
				_, err := e.Submit(&Claim{
					PolicyID: "P", ClaimID: fmt.Sprintf("probe%d", k),
					EventDay: n*3 + 10, // 新年度
					Items:    []Item{inp("A", 500), outp("B", 200)},
				})
				if err != nil {
					b.Fatal(err)
				}
				// 探针笔立即撤销，避免历史无限增长影响后一轮计时。
				if err := e.Cancel("P", fmt.Sprintf("probe%d", k)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestPerformanceScaleProof 以普通测试形式给出可验证的规模对照结论：
// 100k 历史下单笔均耗时不得超过 1k 历史下的 10 倍（宽松阈值防 CI 抖动）。
func TestPerformanceScaleProof(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过规模对照")
	}
	measure := func(n int) float64 {
		e := NewEngine()
		p := &Policy{
			PolicyID: "P", InceptionDay: 0, YearLength: 7,
			PerClaimDeductible: 100, AnnualDeductCap: 1_000_000_000,
			InpatientRatio: 80, OutpatientRatio: 60, AnnualOOPCap: 1_000_000_000,
			ExcludedCodes: map[string]struct{}{},
		}
		if err := e.RegisterPolicy(p); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			e.Submit(&Claim{PolicyID: "P", ClaimID: fmt.Sprintf("h%d", i),
				EventDay: i * 3, Items: []Item{inp("A", 500), outp("B", 200)}})
		}
		const probes = 2000
		start := time.Now()
		for k := 0; k < probes; k++ {
			id := fmt.Sprintf("probe%d", k)
			if _, err := e.Submit(&Claim{PolicyID: "P", ClaimID: id,
				EventDay: n*3 + 10, Items: []Item{inp("A", 500), outp("B", 200)}}); err != nil {
				t.Fatal(err)
			}
			if err := e.Cancel("P", id); err != nil {
				t.Fatal(err)
			}
		}
		elapsed := float64(time.Since(start).Nanoseconds()) / float64(probes)
		t.Logf("历史 %7d 笔、约 %d 个年度：单笔提交+撤销均耗 %.1f ns",
			n, (n*3)/7, elapsed)
		return elapsed
	}
	small := measure(1_000)
	large := measure(100_000)
	t.Logf("规模放大 100 倍，耗时比 = %.2fx（应≈1，硬阈值<10x）", large/small)
	if large > small*10 {
		t.Fatalf("单笔开销随历史增长: %.1fns vs %.1fns", large, small)
	}
}
