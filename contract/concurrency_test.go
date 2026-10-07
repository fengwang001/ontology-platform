package contract

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发等价：并发调用的结果等价于某个串行顺序。
// 验证方式：并发执行后取出服务记录的操作日志（即实际串行化顺序），
// 在全新服务上按日志重放，逐步结果与最终状态查询必须完全一致。
func TestConcurrentEquivalence(t *testing.T) {
	s := NewService()
	var clock atomic.Int64
	clock.Store(1)

	// 串行建立两份合同
	for i := 0; i < 2; i++ {
		p := stdParams()
		p.Start = 0
		p.Expiry = 1 << 28
		res := s.Apply(Op{Kind: OpCreateContract, Now: int(clock.Load()), Params: p})
		if res.Err != None {
			t.Fatalf("create contract %d: %v", i, res.Message)
		}
	}

	var amendCount [2]atomic.Int64
	const workers = 8
	const opsPerWorker = 120
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			nextNow := func() int {
				if rng.Intn(100) < 40 {
					clock.Add(1)
				}
				return int(clock.Load())
			}
			for i := 0; i < opsPerWorker; i++ {
				cid := rng.Intn(2)
				now := nextNow()
				var op Op
				switch rng.Intn(10) {
				case 0, 1, 2: // 创建协议
					op = Op{Kind: OpCreateAmendment, Now: now, Contract: cid,
						DeclaredEffDay: now + rng.Intn(5), Mods: map[int]int{1 + rng.Intn(2): rng.Intn(100)}}
					res := s.Apply(op)
					if res.Err == None {
						amendCount[cid].Add(1)
					}
					continue
				case 3, 4, 5: // 签署
					n := int(amendCount[cid].Load())
					aid := 0
					if n > 0 {
						aid = rng.Intn(n)
					}
					party := "A"
					if rng.Intn(2) == 0 {
						party = "B"
					}
					op = Op{Kind: OpSign, Now: now, Contract: cid, Amendment: aid,
						Party: party, AuthFrom: 0, AuthTo: 1 << 29}
				case 6: // 会签
					n := int(amendCount[cid].Load())
					aid := 0
					if n > 0 {
						aid = rng.Intn(n)
					}
					op = Op{Kind: OpCountersign, Now: now, Contract: cid, Amendment: aid}
				case 7: // 不续签通知
					op = Op{Kind: OpNotice, Now: now, Contract: cid, Party: "A"}
				case 8: // 查询有效值
					op = Op{Kind: OpQueryValue, Now: now, Contract: cid, Clause: 1 + rng.Intn(2), Day: rng.Intn(now + 1)}
				default: // 查询在期
					op = Op{Kind: OpQueryInForce, Now: now, Contract: cid, Day: rng.Intn(now + 1)}
				}
				s.Apply(op)
			}
		}(int64(w)*7919 + 1)
	}
	wg.Wait()

	// 日志即串行化顺序：重放必须逐步一致
	log := s.Log()
	ops := make([]Op, len(log))
	for i, e := range log {
		ops[i] = e.Op
	}
	replayed := Replay(ops)
	for i := range log {
		got, want := replayed[i], log[i].Result
		if got.Err != want.Err || got.ContractID != want.ContractID || got.AmendmentID != want.AmendmentID ||
			got.Value != want.Value || got.Source != want.Source || got.InForce != want.InForce ||
			got.Expiry != want.Expiry || !equalRenewals(got.Renewals, want.Renewals) {
			t.Fatalf("replay diverged at step %d [%s]: got %+v, want %+v", i, describeOp(ops[i]), got, want)
		}
	}

	// 最终状态一致：重放服务与原服务对同一查询电池给出相同答案
	s2 := NewService()
	for _, op := range ops {
		s2.Apply(op)
	}
	now := int(clock.Load()) + 1
	for cid := 0; cid < 2; cid++ {
		for clause := 1; clause <= 2; clause++ {
			for day := 0; day <= now; day += 7 {
				op := Op{Kind: OpQueryValue, Now: now, Contract: cid, Clause: clause, Day: day}
				r1, r2 := s.Apply(op), s2.Apply(op)
				if r1.Value != r2.Value || r1.Source != r2.Source {
					t.Fatalf("final state mismatch: %+v vs %+v for %+v", r1, r2, op)
				}
			}
		}
		op := Op{Kind: OpQueryRenewals, Now: now, Contract: cid}
		if r1, r2 := s.Apply(op), s2.Apply(op); !equalRenewals(r1.Renewals, r2.Renewals) {
			t.Fatalf("renewals mismatch: %+v vs %+v", r1, r2)
		}
	}
	t.Logf("concurrent run serialized into %d logged ops; replay and final state fully consistent", len(log))
}
