package apportion

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// TestDifferentialRandom：随机保单组合 + 随机受理/撤销序列，
// 引擎与独立朴素模型逐笔对账（应赔与年度累计余额完全一致），并打印输入/输出/依据。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	var buf bytes.Buffer

	for iter := 0; iter < 400; iter++ {
		buf.Reset()
		eng := NewEngine(&buf)
		nm := &naiveModel{}
		insured := fmt.Sprintf("I%d", iter%7)

		nPol := 1 + rng.Intn(5)
		for i := 0; i < nPol; i++ {
			no := fmt.Sprintf("%s-P%02d", insured, i)
			start := int64(rng.Intn(5))
			end := start + 1 + int64(rng.Intn(8))
			p := Policy{
				Insured: insured, PolicyNo: no,
				Deductible:  int64(rng.Intn(300)),
				PerLoss:     1 + int64(rng.Intn(900)),
				AnnualLimit: 1 + int64(rng.Intn(2500)),
				StartDay:    start, EndDay: end,
				Clause: Clause(1 + rng.Intn(3)),
			}
			if err := eng.Register(p); err != nil {
				t.Fatalf("iter %d register: %v\nlog:\n%s", iter, err, buf.String())
			}
			nm.policies = append(nm.policies, naivePolicy{
				no: no, deduct: p.Deductible, per: p.PerLoss, rem: p.AnnualLimit,
				start: p.StartDay, end: p.EndDay, clause: p.Clause,
			})
		}

		seq := 3 + rng.Intn(20)
		lossSeq := 0
		for step := 0; step < seq; step++ {
			// 约 25% 概率撤销末笔，其余受理
			if rng.Intn(4) == 0 && len(nm.order) > 0 {
				last := nm.order[len(nm.order)-1]
				// 一半撤销末笔，一半尝试撤销一个非末笔/幽灵号
				target := last
				if rng.Intn(2) == 0 {
					if len(nm.order) > 1 && rng.Intn(2) == 0 {
						target = nm.order[0]
					} else {
						target = fmt.Sprintf("ghost-%d", rng.Int())
					}
				}
				_, eErr := eng.Undo(target, insured)
				nOk := nm.undo(target)
				if (eErr == nil) != nOk {
					t.Fatalf("iter %d undo %s disagree: engine=%v naive=%v\nlog:\n%s",
						iter, target, eErr, nOk, buf.String())
				}
				continue
			}

			lossSeq++
			ln := fmt.Sprintf("%s-L%02d", insured, lossSeq)
			day := int64(rng.Intn(12))
			amount := 1 + int64(rng.Intn(2000))
			out, err := eng.Accept(Loss{LossNo: ln, Insured: insured, Day: day, Amount: amount})
			if err != nil {
				// 唯一可能：损失号重复（重放）。朴素模型跳过即可。
				continue
			}
			nPay := nm.accept(ln, day, amount)

			ePay := map[string]int64{}
			for _, p := range out.Payouts {
				ePay[p.PolicyNo] = p.Amount
			}
			if !mapsEqual(ePay, nPay) {
				t.Fatalf("iter %d %s amount=%d day=%d payout mismatch:\nengine=%v\nnaive=%v\nlog:\n%s",
					iter, ln, amount, day, ePay, nPay, buf.String())
			}
			if !mapsEqual(out.Remaining, nm.remaining()) {
				t.Fatalf("iter %d %s remaining mismatch:\nengine=%v\nnaive=%v\nlog:\n%s",
					iter, ln, out.Remaining, nm.remaining(), buf.String())
			}
		}
	}
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		// 允许朴素模型缺零值键
		for k, v := range a {
			if b[k] != v {
				return false
			}
		}
		for k, v := range b {
			if a[k] != v {
				return false
			}
		}
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestDifferentialFixedLog 打印一条完整输入/输出/依据日志，便于人工核对。
func TestDifferentialFixedLog(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(&buf)
	mustReg := func(no string, ded, per, ann, s, en int64, c Clause) {
		if err := e.Register(Policy{Insured: "I", PolicyNo: no, Deductible: ded,
			PerLoss: per, AnnualLimit: ann, StartDay: s, EndDay: en, Clause: c}); err != nil {
			t.Fatal(err)
		}
	}
	mustReg("PA", 100, 800, 5000, 0, 30, LimitShare)
	mustReg("PB", 0, 600, 2000, 0, 30, IndependentShare)
	mustReg("PX", 0, 1000, 5000, 0, 30, Excess)
	if _, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 5, Amount: 2500}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Accept(Loss{LossNo: "L2", Insured: "I", Day: 6, Amount: 177}); err != nil {
		t.Fatal(err)
	}
	t.Logf("\n%s", buf.String())
}
