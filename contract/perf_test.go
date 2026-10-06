package contract

import "testing"

// TestQueryCostIndependentOfTotalAmendments 以可计数方式证明：
// 单条款查询只检查该条款的候选协议及其撤销子树，
// 与合同累计协议总数 N 无关。
func TestQueryCostIndependentOfTotalAmendments(t *testing.T) {
	for _, n := range []int{10, 100, 500} {
		s := NewService()
		mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100000)), "create")
		// 添加 n 份互不相关的协议：每份改 OTHER 条款（主合同没有会校验失败，
		// 故都改 PRICE，但查询条款用 LOCKED_TERM，其候选列表为空）。
		for i := 0; i < n; i++ {
			id := "X" + itoa(i)
			mustOK(t, s.AddAmendment(AddAmendmentInput{
				ContractID: "C", AmendmentID: id, Now: int64(1 + i*3),
				EffectiveDay: int64(100 + i),
				Changes:      map[string]int{"PRICE": i}}), "add "+id)
			mustOK(t, s.Sign("C", id, "A", int64(1+i*3)), "a")
			mustOK(t, s.Sign("C", id, "B", int64(2+i*3)), "b")
		}
		c := s.contracts["C"]
		activeMetrics = &queryMetrics{}
		_, err := s.EffectiveValue("C", "LOCKED_TERM", 50000)
		got := activeMetrics.AmendmentChecks
		activeMetrics = nil
		mustOK(t, err, "query")
		if got != 0 {
			t.Fatalf("n=%d: unrelated amendment checks=%d, want 0 (LOCKED_TERM 无候选)", n, got)
		}
		// PRICE 的候选随 n 线性（该条款确有 n 份修改），但对单条款 LOCKED_TERM
		// 的查询成本恒为 0，证明不随协议总数增长。
		if total := len(c.order); total != n {
			t.Fatalf("total amendments=%d", total)
		}
	}

	// 反向验证：查询 PRICE 时检查数仅与触及 PRICE 的协议数相关，
	// 再加入一批只改 LOCKED_TERM 的协议不会增加 PRICE 查询成本。
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("P", 0, 0, 100000)), "create")
	put := func(prefix string, price, locked int, baseNow int64) {
		for i := 0; i < 50; i++ {
			id := prefix + itoa(i)
			ch := map[string]int{}
			if price > 0 {
				ch["PRICE"] = price + i
			}
			if locked > 0 {
				ch["LOCKED_TERM"] = locked + i
			}
			mustOK(t, s.AddAmendment(AddAmendmentInput{
				ContractID: "P", AmendmentID: id, Now: baseNow + int64(i*3),
				EffectiveDay: 100, Changes: ch}), "add")
			mustOK(t, s.Sign("P", id, "A", baseNow+int64(1+i*3)), "a")
			mustOK(t, s.Sign("P", id, "B", baseNow+int64(2+i*3)), "b")
		}
	}
	put("Q", 1, 0, 1)
	activeMetrics = &queryMetrics{}
	_, _ = s.EffectiveValue("P", "PRICE", 5000)
	base := activeMetrics.AmendmentChecks
	activeMetrics = nil
	put("L", 0, 1, 1000) // 只改锁定条款，需要会签生效
	c := s.contracts["P"]
	cosignNow := int64(2000)
	for _, a := range c.order {
		if a.needsCosign && a.cosignedAt < 0 {
			cosignNow++
			mustOK(t, s.LegalCosign("P", a.id, cosignNow), "cosign")
		}
	}
	activeMetrics = &queryMetrics{}
	_, _ = s.EffectiveValue("P", "PRICE", 5000)
	after := activeMetrics.AmendmentChecks
	activeMetrics = nil
	if after != base {
		t.Fatalf("PRICE checks changed after adding LOCKED-only amendments: %d -> %d",
			base, after)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
