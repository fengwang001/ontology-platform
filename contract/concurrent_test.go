package contract

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentEquivalence 多 goroutine 并发施加同一组互不冲突的操作，
// 结果必须等价于某个串行顺序：最终所有协议均生效，查询结果与朴素串行重放一致。
func TestConcurrentEquivalence(t *testing.T) {
	for trial := 0; trial < 8; trial++ {
		svc := NewService()
		cid := fmt.Sprintf("CC%d", trial)
		mustOK(t, svc.CreateContract(baseContract(cid, 0, 0, 500)), "create")

		const n = 20
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				base := int64(10 + idx)
				mid := fmt.Sprintf("P%d", idx)
				// 时钟回退由全局互斥保证；若因调度得到回退错误，按同样顺序在朴素模型也会回退。
				_ = svc.AddAmendment(AddAmendmentInput{
					ContractID: cid, AmendmentID: mid, Now: base,
					EffectiveDay: 50, Changes: map[string]int{"PRICE": 1000 + idx}})
				_ = svc.Sign(cid, mid, "A", base+1)
				_ = svc.Sign(cid, mid, "B", base+2)
			}(i)
		}
		wg.Wait()

		// 并发下部分操作可能因时钟回退被拒；统计被接受的协议，并在朴素模型上
		// 按服务实际接受的串行顺序重放，验证最终查询一致。
		nav := NewNaiveModel()
		mustOK(t, nav.Create(baseContract(cid, 0, 0, 500)), "naive create")
		c := svc.contracts[cid]
		type accepted struct {
			id        string
			add, a, b int64
		}
		var list []accepted
		for _, a := range c.order {
			d0, ok1 := a.signatureDay["A"]
			d1, ok2 := a.signatureDay["B"]
			if ok1 && ok2 {
				list = append(list, accepted{a.id, int64(10 + indexOf(a.id)), d0, d1})
			}
		}
		for _, x := range list {
			idx := indexOf(x.id)
			if err := nav.Add(AddAmendmentInput{
				ContractID: cid, AmendmentID: x.id, Now: int64(10 + idx),
				EffectiveDay: 50, Changes: map[string]int{"PRICE": 1000 + idx}}); err != nil {
				t.Fatalf("naive add: %v", err)
			}
			if err := nav.Sign(cid, x.id, "A", x.a); err != nil {
				t.Fatalf("naive sign A: %v", err)
			}
			if err := nav.Sign(cid, x.id, "B", x.b); err != nil {
				t.Fatalf("naive sign B: %v", err)
			}
		}
		nc := nav.Contract(cid)
		for day := int64(0); day <= 520; day += 7 {
			g, err := svc.EffectiveValue(cid, "PRICE", day)
			if err != nil {
				t.Fatal(err)
			}
			w := nc.Value("PRICE", day)
			if g.Value != w.Value || g.AmendmentID != w.AmendmentID {
				t.Fatalf("day %d concurrent=%+v serial=%+v", day, g, w)
			}
		}
	}
}

func indexOf(id string) int {
	var n int
	fmt.Sscanf(id, "P%d", &n)
	return n
}

// TestReplayDeterminism 同一操作序列两次重放结果逐字节一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() (int64, string, []RenewalRecord) {
		s := NewService()
		_ = s.CreateContract(baseContract("R", 0, 0, 100))
		_ = s.NoticeNonRenewal("R", "A", 91)
		exp, _ := s.CurrentExpiry("R", 130)
		v, _ := s.EffectiveValue("R", "PRICE", 130)
		recs, _ := s.Renewals("R", 130)
		return exp, fmt.Sprintf("%+v", v), recs
	}
	e1, v1, r1 := run()
	e2, v2, r2 := run()
	if e1 != e2 || v1 != v2 || !recsEqual(r1, r2) {
		t.Fatalf("replay differs: %d/%s/%+v vs %d/%s/%+v", e1, v1, r1, e2, v2, r2)
	}
}
