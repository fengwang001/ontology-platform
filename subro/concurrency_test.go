package subro_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/subro"
)

// 所有操作可并发调用，结果等价于某个串行顺序：
// 并发执行后，把已接受操作日志按应用顺序重放到新服务，
// 两服务的全部案件快照必须完全一致。
func TestConcurrentSerializable(t *testing.T) {
	s := subro.NewService()
	const numCases = 8
	for i := 0; i < numCases; i++ {
		id := fmt.Sprintf("c%d", i)
		if err := s.RegisterCase(0, subro.CaseInput{
			CaseID: id, TotalLoss: 1_000_000, InsurerPaid: 400_000, Deadline: 1 << 40, RatioBP: 7000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var now atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				n := now.Add(1)
				id := fmt.Sprintf("c%d", rng.Intn(numCases))
				switch rng.Intn(4) {
				case 0:
					gross := rng.Int63n(1000)
					_, _ = s.Recover(n, id, gross, rng.Int63n(gross+1))
				case 1:
					_, _ = s.AdjustRatio(n, id, rng.Int63n(10001))
				case 2:
					_, _ = s.Supplement(n, id, 1+rng.Int63n(50))
				case 3:
					_, _ = s.Waive(n, id)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	accepted := s.AcceptedOps()
	t.Logf("并发执行完成: 已接受操作 %d 笔", len(accepted))
	replayed := subro.NewService()
	for i, op := range accepted {
		if _, err := replayed.Apply(op); err != nil {
			t.Fatalf("重放第 %d 笔已接受操作 %+v 失败: %v", i, op, err)
		}
	}
	ids := s.CaseIDs()
	if len(ids) != numCases {
		t.Fatalf("案件数=%d, 期望 %d", len(ids), numCases)
	}
	for _, id := range ids {
		a, _ := s.Snapshot(id)
		b, _ := replayed.Snapshot(id)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("案件 %s 并发结果与串行重放不一致:\n并发 %+v\n重放 %+v", id, a, b)
		}
		ent := a.Entitlements
		if sum := ent.Insured + ent.Insurer + ent.ThirdParty; sum != a.NetTotal {
			t.Fatalf("案件 %s 不变式破坏: 应得之和=%d 净回收总额=%d", id, sum, a.NetTotal)
		}
		if a.Disbursed != ent {
			t.Fatalf("案件 %s 结清后已发放应等于应得: %+v != %+v", id, a.Disbursed, ent)
		}
		sums := map[string]int64{}
		for _, adj := range a.Adjustments {
			v := adj.Amount
			if adj.Direction.String() == "clawback" {
				v = -v
			}
			sums[adj.Party.String()] += v
		}
		if sums["insured"] != ent.Insured || sums["insurer"] != ent.Insurer || sums["third_party"] != ent.ThirdParty {
			t.Fatalf("案件 %s 调整记录与已发放不自洽: sums=%v ent=%+v", id, sums, ent)
		}
	}
}
