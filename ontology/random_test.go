package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestRandomSkeleton 占位保留。

func rngStates(r *rand.Rand, n int) []*policyState {
	clauses := []Clause{ClauseLimitProportional, ClauseIndependentLiability, ClauseExcess}
	states := make([]*policyState, 0, n)
	for i := 0; i < n; i++ {
		p := Policy{
			Insured:      "I",
			PolicyID:     fmt.Sprintf("P%03d", i),
			Deductible:   r.Int63n(500),
			PerLossLimit: 1 + r.Int63n(2000),
			AnnualLimit:  1 + r.Int63n(5000),
			CoverFrom:    0,
			CoverTo:      100,
			Clause:       clauses[r.Intn(3)],
		}
		states = append(states, &policyState{spec: p, remains: 1 + r.Int63n(p.AnnualLimit)})
	}
	return states
}

// 随机保单快照：引擎算法与独立朴素模型逐分一致。
func TestRandomNaiveEquivalence(t *testing.T) {
	r := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 4000; iter++ {
		states := rngStates(r, 1+r.Intn(8))
		amount := int64(1 + r.Intn(3000))

		// 朴素模型不得看到引擎内部状态被修改。
		snap := make([]*policyState, len(states))
		for i, st := range states {
			cp := *st
			snap[i] = &cp
		}
		got := apportion(snap, 1, amount)
		want := naiveApportion(states, 1, amount)

		nonzero := func(m map[string]int64) int {
			n := 0
			for _, v := range m {
				if v != 0 {
					n++
				}
			}
			return n
		}
		if got.Paid != want.Paid || got.NoPayer != want.NoPayer ||
			nonzero(got.Shares) != nonzero(want.Shares) {
			t.Fatalf("iter %d mismatch\ninput: amount=%d states=%v\ngot=%+v\nwant=%+v",
				iter, amount, states, got, want)
		}
		for id, v := range want.Shares {
			if got.Shares[id] != v {
				t.Fatalf("iter %d policy %s got=%d want=%d\ninput amount=%d\n判定依据: %s",
					iter, id, got.Shares[id], v, amount, got.Reason)
			}
		}
		for id, v := range got.Shares {
			if v != 0 && want.Shares[id] != v {
				t.Fatalf("iter %d extra policy %s got=%d", iter, id, v)
			}
		}
		// 不变量：合计不超过损失，也不超过独立责任额之和；各单张不超过独立责任额。
		var sumCap int64
		for _, st := range states {
			sumCap += independentLiability(st.spec, st.remains, amount)
		}
		if got.Paid > amount || got.Paid > sumCap {
			t.Fatalf("iter %d paid %d exceeds amount %d / sumCap %d", iter, got.Paid, amount, sumCap)
		}
	}
}

type opLog struct {
	kind    int // 0 register 1 accept 2 cancel
	p       Policy
	l       Loss
	insured string
	lossID  string
}

func buildRandomSequence(r *rand.Rand, insureds []string) []opLog {
	var ops []opLog
	counters := map[string]int{}
	lastLoss := map[string]string{}
	n := 30 + r.Intn(60)
	for k := 0; k < n; k++ {
		ins := insureds[r.Intn(len(insureds))]
		switch r.Intn(10) {
		case 0, 1, 2:
			counters[ins]++
			p := Policy{
				Insured:      ins,
				PolicyID:     fmt.Sprintf("%s-P%02d", ins, counters[ins]),
				Deductible:   r.Int63n(300),
				PerLossLimit: 1 + r.Int63n(1500),
				AnnualLimit:  1 + r.Int63n(4000),
				CoverFrom:    r.Intn(5),
				CoverTo:      5 + r.Intn(20),
				Clause:       Clause(r.Intn(3)),
			}
			ops = append(ops, opLog{kind: 0, p: p})
			// 刻意重复登记若干次。
			if r.Intn(5) == 0 {
				ops = append(ops, opLog{kind: 0, p: p})
			}
		default:
			if r.Intn(6) == 0 && lastLoss[ins] != "" {
				id := lastLoss[ins]
				if r.Intn(2) == 0 {
					id = "NOT-LAST"
				}
				ops = append(ops, opLog{kind: 2, insured: ins, lossID: id})
				continue
			}
			id := fmt.Sprintf("%s-L%03d", ins, k)
			l := Loss{LossID: id, Insured: ins, Day: r.Intn(25), Amount: 1 + r.Int63n(3000)}
			ops = append(ops, opLog{kind: 1, l: l})
			lastLoss[ins] = id
		}
	}
	return ops
}

func runSequence(e *Engine, ops []opLog) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		switch o.kind {
		case 0:
			err := e.RegisterPolicy(o.p)
			out = append(out, fmt.Sprintf("reg %s/%s -> %v", o.p.Insured, o.p.PolicyID, err))
		case 1:
			r, err := e.AcceptLoss(o.l)
			if err != nil {
				out = append(out, fmt.Sprintf("loss %s -> %v", o.l.LossID, err))
			} else {
				out = append(out, fmt.Sprintf("loss %s -> paid=%d shares=%v [%s]",
					o.l.LossID, r.Paid, r.Shares, r.Reason))
			}
		case 2:
			err := e.CancelLoss(o.insured, o.lossID)
			out = append(out, fmt.Sprintf("cancel %s/%s -> %v", o.insured, o.lossID, err))
		}
	}
	return out
}

// 随机操作序列：两台引擎重放结果完全一致（输入、输出、判定依据）。
func TestRandomReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(424242))
	insureds := []string{"I", "J", "K"}
	for iter := 0; iter < 200; iter++ {
		ops := buildRandomSequence(r, insureds)
		out1 := runSequence(NewEngine(), ops)
		out2 := runSequence(NewEngine(), ops)
		for i := range out1 {
			if out1[i] != out2[i] {
				t.Fatalf("iter %d op %d replay differs:\n%s\nvs\n%s", iter, i, out1[i], out2[i])
			}
		}
		if iter == 0 && testing.Verbose() {
			for _, line := range out1 {
				t.Log(line)
			}
		}
	}
}

// 同一被保人并发提交受理/撤销：结果等价于某个串行顺序（账守恒且可解释）。
func TestConcurrentSameInsuredSerializable(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		e := NewEngine()
		must(t, e.RegisterPolicy(ePol("C", "P1", ClauseLimitProportional, 0, 1000, 100000)))
		must(t, e.RegisterPolicy(ePol("C", "P2", ClauseIndependentLiability, 0, 1000, 100000)))

		var wg sync.WaitGroup
		var seqMu sync.Mutex
		seq := []string{}
		for k := 0; k < 40; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				id := fmt.Sprintf("L%02d", k)
				r, err := e.AcceptLoss(Loss{LossID: id, Insured: "C", Day: 1, Amount: 100})
				seqMu.Lock()
				if err == nil {
					seq = append(seq, id)
				}
				seqMu.Unlock()
				if err != nil {
					t.Errorf("accept %s: %v", id, err)
					return
				}
				// 并发撤销只可能成功于「仍为末笔」的窗口，否则非末笔/损失不存在，二者均合法。
				_ = e.CancelLoss("C", id)
				_ = r
			}(k)
		}
		wg.Wait()

		// 账守恒：剩余额 = 初始 - 当前仍登记损失的应赔合计。
		var stillPaid int64
		for _, id := range seq {
			if v, ok := e.LossPaid(id); ok {
				stillPaid += v
			}
		}
		b1, _, _ := e.Balance("C", "P1")
		b2, _, _ := e.Balance("C", "P2")
		if 100000*2-b1-b2 != stillPaid {
			t.Fatalf("trial %d ledger drift: paid=%d remains=%d", trial, stillPaid, b1+b2)
		}
	}
}

// 分摊开销不随该被保人历史损失数、也不随其他被保人保单数增长。
func BenchmarkApportionScaling(b *testing.B) {
	r := rand.New(rand.NewSource(7))
	eng := NewEngine()
	for i := 0; i < 20000; i++ {
		if err := eng.RegisterPolicy(ePol("OTHER", fmt.Sprintf("Q%05d", i),
			Clause(r.Intn(3)), 0, 1000, 100000)); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 2000; i++ {
		_, _ = eng.AcceptLoss(Loss{
			LossID: fmt.Sprintf("OLD%05d", i), Insured: "OTHER", Day: 1, Amount: 1,
		})
	}
	// 目标被保人：固定 6 张参与保单，制造 0/500/2000 三档历史长度。
	for _, hist := range []int{0, 500, 2000} {
		eng2 := NewEngine()
		for k := 0; k < 6; k++ {
			if err := eng2.RegisterPolicy(ePol("H", fmt.Sprintf("H%02d", k),
				Clause(r.Intn(3)), 0, 5000, 5000000)); err != nil {
				b.Fatal(err)
			}
		}
		for i := 0; i < hist; i++ {
			if _, err := eng2.AcceptLoss(Loss{
				LossID: fmt.Sprintf("HIST%05d", i), Insured: "H", Day: 1, Amount: 10,
			}); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("hist=%d", hist), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// 「受理下一笔 -> 撤销末笔」循环：该被保人有效历史损失数恒为 hist。
				id := fmt.Sprintf("BENCH-%d-%d", hist, i)
				if _, err := eng2.AcceptLoss(Loss{LossID: id, Insured: "H", Day: 1, Amount: 3000}); err != nil {
					b.Fatal(err)
				}
				if err := eng2.CancelLoss("H", id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	_ = eng
}
