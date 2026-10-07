package settlement_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/settlement"
)

// genContractScript 为单个合同生成固定 now 的随机操作脚本。
// 固定 now 使跨合同的并发交错不影响全局时钟判定，
// 从而并发结果必须与串行执行完全一致。
func genContractScript(r *rand.Rand, c settlement.ContractParams, now int64, n int) []testOp {
	var ops []testOp
	defectSeq := 0
	for i := 0; i < n; i++ {
		mid := c.Milestones[r.Intn(len(c.Milestones))].ID
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39: // 40%
			ops = append(ops, testOp{kind: "accept", contractID: c.ID, milestone: mid, passed: r.Intn(4) != 0, now: now})
		case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54: // 15%
			ops = append(ops, testOp{kind: "release", contractID: c.ID, milestone: mid, now: now})
		case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64: // 10%
			did := fmt.Sprintf("d%d", defectSeq)
			defectSeq++
			ops = append(ops, testOp{kind: "regdef", contractID: c.ID, milestone: mid, defectID: did, penalty: r.Int63n(20000), now: now})
		case 65, 66, 67, 68, 69: // 5%
			ops = append(ops, testOp{kind: "closedef", contractID: c.ID, milestone: mid, defectID: fmt.Sprintf("d%d", r.Intn(defectSeq+1)), now: now})
		case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79: // 10%
			ops = append(ops, testOp{
				kind: "change", contractID: c.ID, effDay: now, now: now,
				adjs: []settlement.Adjustment{{MilestoneID: mid, Payable: r.Int63n(25000), PlanDay: r.Int63n(50)}},
			})
		case 80, 81: // 2%
			ops = append(ops, testOp{kind: "terminate", contractID: c.ID, now: now})
		default: // 其余为查询占位（不计入脚本）
			continue
		}
	}
	return ops
}

// TestConcurrentEquivalence 并发调用等价于某个串行顺序：
// 每个合同的脚本由一个 goroutine 顺序执行（合同间并发），
// 最终状态必须与串行执行同一组脚本完全一致。
func TestConcurrentEquivalence(t *testing.T) {
	const now = 100
	for seed := int64(0); seed < 30; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			contracts := genContracts(r)
			scripts := make(map[string][]testOp, len(contracts))
			for _, c := range contracts {
				scripts[c.ID] = genContractScript(r, c, now, 120)
			}

			// 串行参照。
			seq := settlement.NewService()
			for _, c := range contracts {
				if err := seq.CreateContract(c, now); err != nil {
					t.Fatalf("create: %v", err)
				}
			}
			for _, c := range contracts {
				for _, op := range scripts[c.ID] {
					applySvcOnly(seq, op)
				}
			}

			// 并发执行：每合同一个 goroutine，另有并发只读查询。
			conc := settlement.NewService()
			for _, c := range contracts {
				if err := conc.CreateContract(c, now); err != nil {
					t.Fatalf("create: %v", err)
				}
			}
			var wg sync.WaitGroup
			for _, c := range contracts {
				c := c
				wg.Add(1)
				go func() {
					defer wg.Done()
					for _, op := range scripts[c.ID] {
						applySvcOnly(conc, op)
					}
				}()
			}
			// 并发只读查询（汇总必须始终守恒）。
			stop := make(chan struct{})
			var readerWg sync.WaitGroup
			readerWg.Add(1)
			go func() {
				defer readerWg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					for _, c := range contracts {
						s, err := conc.Summary(c.ID)
						if err == nil && !s.Conserved() {
							t.Errorf("conservation violated during concurrent run: %+v", s)
							return
						}
					}
				}
			}()
			wg.Wait()
			close(stop)
			readerWg.Wait()

			// 比较每个合同的最终汇总。
			for _, c := range contracts {
				want, err := seq.Summary(c.ID)
				if err != nil {
					t.Fatalf("seq summary: %v", err)
				}
				got, err := conc.Summary(c.ID)
				if err != nil {
					t.Fatalf("conc summary: %v", err)
				}
				if want != got {
					t.Fatalf("seed=%d contract %s divergence:\n seq=%+v\nconc=%+v", seed, c.ID, want, got)
				}
			}
		})
	}
}
