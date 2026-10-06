package leasechain

import (
	"math/rand"
	"testing"
)

// genOps 生成一条确定性随机操作序列。生成器只依赖自己的簿记，
// 不读取被测状态，因此被拒操作也会自然大量出现（用于验证拒绝不留痕）。
func genOps(t *testing.T, rng *rand.Rand, n int) []Op {
	t.Helper()
	type lrec struct {
		id                int64
		landlord, tenant  string
		parent            int64
		start, end, depth int
		rent              int64
		alive, recognized bool
		hasChild          bool
	}
	var leases []*lrec
	now := 0
	nextID := int64(1)
	tenants := []string{"TA", "TB", "TC", "TD", "TE", "TF"}
	var ops []Op

	alive := func() []*lrec {
		var out []*lrec
		for _, l := range leases {
			if l.alive {
				out = append(out, l)
			}
		}
		return out
	}
	activeMasters := func() []*lrec {
		var out []*lrec
		for _, l := range leases {
			if l.alive && l.parent == 0 {
				out = append(out, l)
			}
		}
		return out
	}

	for i := 0; i < n; i++ {
		now += rng.Intn(4)
		roll := rng.Intn(100)
		switch {
		case len(leases) == 0 || roll < 18:
			tenant := tenants[rng.Intn(len(tenants))]
			start := now + rng.Intn(5)
			end := start + 30 + rng.Intn(160)
			rent := int64(500 + rng.Intn(1500))
			op := Op{Kind: opMaster, Now: now, A: "LL", B: tenant, X: start, Y: end, R: rent}
			ops = append(ops, op)
			leases = append(leases, &lrec{
				id: nextID, landlord: "LL", tenant: tenant, start: start,
				end: end, rent: rent, alive: true,
			})
			nextID++
		case roll < 55:
			al := alive()
			if len(al) == 0 {
				ops = append(ops, Op{Kind: opAdvance, Now: now})
				continue
			}
			p := al[rng.Intn(len(al))]
			tenant := tenants[rng.Intn(len(tenants))]
			// 70% 概率构造合法期限/租金；30% 故意越界以触发错误分支。
			start, end, rent := p.start, p.end, p.rent
			if rng.Intn(10) < 7 {
				start = p.start + rng.Intn(max1(p.end-p.start-1))
				end = start + 1 + rng.Intn(max1(p.end-start))
				if end > p.end {
					end = p.end
				}
				rent = p.rent * int64(80+rng.Intn(50)) / 100
				if rent <= 0 {
					rent = p.rent
				}
			} else {
				start = p.start + rng.Intn(5)
				end = p.end + rng.Intn(10)
				rent = p.rent*2 + 1
			}
			// 同意：一半概括、一半一次性；10% 完全不给。
			mode := rng.Intn(10)
			if mode < 4 {
				ops = append(ops, Op{Kind: opGrantGeneral, Now: now, A: "LL", B: tenant})
				if rng.Intn(3) == 0 {
					ops = append(ops, Op{Kind: opRevokeGeneral, Now: now, A: "LL", B: tenant})
				}
			} else if mode < 9 {
				ops = append(ops, Op{Kind: opGrantOneTime, Now: now, A: "LL", B: tenant,
					I: p.id, X: start, Y: end, R: rent})
			}
			ops = append(ops, Op{Kind: opSublease, Now: now, I: p.id, B: tenant,
				X: start, Y: end, R: rent})
		case roll < 63:
			al := alive()
			if len(al) == 0 {
				ops = append(ops, Op{Kind: opAdvance, Now: now})
				continue
			}
			l := al[rng.Intn(len(al))]
			who := l.landlord
			if rng.Intn(2) == 0 {
				who = l.tenant
			}
			ops = append(ops, Op{Kind: opRecognize, Now: now, I: l.id, A: who})
		case roll < 72:
			al := alive()
			if len(al) == 0 {
				ops = append(ops, Op{Kind: opAdvance, Now: now})
				continue
			}
			l := al[rng.Intn(len(al))]
			who := l.tenant
			if rng.Intn(2) == 0 {
				who = l.landlord
			}
			ops = append(ops, Op{Kind: opTerminate, Now: now, I: l.id, A: who})
		case roll < 78:
			al := alive()
			if len(al) == 0 {
				ops = append(ops, Op{Kind: opAdvance, Now: now})
				continue
			}
			l := al[rng.Intn(len(al))]
			ops = append(ops, Op{Kind: opExit, Now: now, I: l.id, A: l.tenant})
		default:
			ops = append(ops, Op{Kind: opAdvance, Now: now})
		}
	}

	// 用朴素模型做一次“记账重放”以收集真实欠费 ID，随后追加清偿操作。
	cfg := Config{P: 120, D: 3, G: 4, PayDay: 12}
	probe := NewNaive(cfg)
	var payOps []Op
	for _, op := range ops {
		_ = probe.Apply(op)
		if len(probe.arrears) > 0 && rng.Intn(3) == 0 {
			a := probe.arrears[rng.Intn(len(probe.arrears))]
			chain := probe.ancestors(a.leaseID)
			if len(chain) > 0 {
				payer := chain[rng.Intn(len(chain))]
				remaining := a.amount - a.paid
				amount := remaining
				if rng.Intn(2) == 0 && remaining > 1 {
					amount = 1 + rng.Int63n(remaining)
				}
				if amount > 0 {
					pop := Op{Kind: opPay, Now: op.Now, I: a.id, ID: payer, R: amount}
					payOps = append(payOps, pop)
					_ = probe.Apply(pop)
				}
			}
		}
	}
	ops = append(ops, payOps...)

	// 偶发时钟回退调用（必须报时钟回退且不留痕）。
	if len(ops) > 10 {
		idx := 5 + rng.Intn(len(ops)-6)
		prev := ops[idx-1].Now
		if prev > 0 {
			ops = append(ops, Op{Kind: opAdvance, Now: prev - 1})
		}
	}
	_ = activeMasters
	return ops
}

func max1(x int) int {
	if x <= 1 {
		return 1
	}
	return x
}
