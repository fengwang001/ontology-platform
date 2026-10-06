package contract

import "testing"

func baseContract(id string, now, start, expiry int64) CreateContractInput {
	return CreateContractInput{
		ID: id,
		Parties: [2]PartyInput{
			{ID: "A", AuthFrom: 0, AuthUntil: 100000},
			{ID: "B", AuthFrom: 0, AuthUntil: 100000},
		},
		Clauses: map[string]int{
			"PRICE":           100,
			"LOCKED_TERM":     1,
			ClauseAutoRenew:   1,
			ClauseRenewalTerm: 30,
			ClauseNoticeDays:  10,
		},
		LockedClauses: map[string]bool{"LOCKED_TERM": true},
		StartDay:      start,
		ExpiryDay:     expiry,
		Now:           now,
	}
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func wantCode(t *testing.T, err error, code ErrorCode, ctx string) {
	t.Helper()
	se, ok := err.(*ServiceError)
	if !ok {
		t.Fatalf("%s: expected ServiceError, got %v", ctx, err)
	}
	if se.Code != code {
		t.Fatalf("%s: expected %s, got %s (%v)", ctx, code, se.Code, err)
	}
}

// 生效日恰等：声明生效日=签署完成日，当日即可取新值。
func TestEffectiveDayExact(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 5,
		EffectiveDay: 10, Changes: map[string]int{"PRICE": 200}}), "add")
	mustOK(t, s.Sign("C", "M1", "A", 10), "sign A")
	mustOK(t, s.Sign("C", "M1", "B", 10), "sign B")
	v, err := s.EffectiveValue("C", "PRICE", 10)
	mustOK(t, err, "query")
	if v.Value != 200 || v.Kind != "AMENDMENT" || v.AmendmentID != "M1" {
		t.Fatalf("day10 value = %+v", v)
	}
	v9, _ := s.EffectiveValue("C", "PRICE", 9)
	if v9.Value != 100 || v9.Kind != "MAIN" {
		t.Fatalf("day9 value = %+v", v9)
	}
}

// 同日多协议取舍。
func TestSameDayTieBreak(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1,
		EffectiveDay: 20, Changes: map[string]int{"PRICE": 1}}), "add1")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M2", Now: 2,
		EffectiveDay: 20, Changes: map[string]int{"PRICE": 2}}), "add2")
	mustOK(t, s.Sign("C", "M1", "A", 5), "m1a")
	mustOK(t, s.Sign("C", "M1", "B", 6), "m1b")
	mustOK(t, s.Sign("C", "M2", "A", 7), "m2a")
	mustOK(t, s.Sign("C", "M2", "B", 8), "m2b")
	v, _ := s.EffectiveValue("C", "PRICE", 20)
	if v.Value != 2 || v.AmendmentID != "M2" {
		t.Fatalf("tie break = %+v, want M2", v)
	}
}

// 撤销与再撤销。
func TestRevokeAndRerevoke(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1, EffectiveDay: 10,
		Changes: map[string]int{"PRICE": 200}}), "add m1")
	mustOK(t, s.Sign("C", "M1", "A", 2), "m1a")
	mustOK(t, s.Sign("C", "M1", "B", 3), "m1b")

	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "R1", Now: 4, EffectiveDay: 15,
		Revokes: "M1"}), "add r1")
	mustOK(t, s.Sign("C", "R1", "A", 5), "r1a")
	mustOK(t, s.Sign("C", "R1", "B", 6), "r1b")

	v14, _ := s.EffectiveValue("C", "PRICE", 14)
	if v14.Value != 200 {
		t.Fatalf("day14 = %d want 200", v14.Value)
	}
	v15, _ := s.EffectiveValue("C", "PRICE", 15)
	if v15.Value != 100 || v15.Kind != "MAIN" {
		t.Fatalf("day15 = %+v want MAIN 100", v15)
	}

	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "R2", Now: 7, EffectiveDay: 18,
		Revokes: "R1"}), "add r2")
	mustOK(t, s.Sign("C", "R2", "A", 8), "r2a")
	mustOK(t, s.Sign("C", "R2", "B", 9), "r2b")
	v18, _ := s.EffectiveValue("C", "PRICE", 18)
	if v18.Value != 200 || v18.AmendmentID != "M1" {
		t.Fatalf("day18 = %+v want M1 200", v18)
	}
	v16, _ := s.EffectiveValue("C", "PRICE", 16)
	if v16.Value != 100 {
		t.Fatalf("day16 = %d want 100", v16.Value)
	}
}

// 授权有效期两端（含端点）。
func TestAuthWindowEndpoints(t *testing.T) {
	s := NewService()
	in := baseContract("C", 0, 0, 100)
	in.Parties[0].AuthFrom = 10
	in.Parties[0].AuthUntil = 20
	mustOK(t, s.CreateContract(in), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1, EffectiveDay: 30,
		Changes: map[string]int{"PRICE": 2}}), "add")
	wantCode(t, s.Sign("C", "M1", "A", 9), ErrAuthExpired, "before window")
	mustOK(t, s.Sign("C", "M1", "A", 10), "lower endpoint")
	// B 在第 20 日签：距 A 的第 10 日恰为 10 天（=deadline），允许；第 21 日则超期。
	mustOK(t, s.Sign("C", "M1", "B", 20), "equal deadline")

	s2 := NewService()
	in2 := baseContract("D", 0, 0, 100)
	in2.Parties[1].AuthFrom = 30
	in2.Parties[1].AuthUntil = 40
	mustOK(t, s2.CreateContract(in2), "create2")
	mustOK(t, s2.AddAmendment(AddAmendmentInput{
		ContractID: "D", AmendmentID: "M1", Now: 1, EffectiveDay: 50,
		Changes: map[string]int{"PRICE": 2}}), "add2")
	// 用独立合同验证授权下端之外；时钟单调。
	s3 := NewService()
	in3 := baseContract("E", 0, 0, 100)
	in3.Parties[1].AuthFrom = 30
	in3.Parties[1].AuthUntil = 40
	mustOK(t, s3.CreateContract(in3), "create3")
	mustOK(t, s3.AddAmendment(AddAmendmentInput{
		ContractID: "E", AmendmentID: "M1", Now: 1, EffectiveDay: 50,
		Changes: map[string]int{"PRICE": 2}}), "add3")
	// 单独合同验证 B 授权下端之外（B 首签前无超期问题）。
	s4 := NewService()
	in4 := baseContract("F", 0, 0, 100)
	in4.Parties[1].AuthFrom = 30
	in4.Parties[1].AuthUntil = 40
	mustOK(t, s4.CreateContract(in4), "create4")
	mustOK(t, s4.AddAmendment(AddAmendmentInput{
		ContractID: "F", AmendmentID: "M1", Now: 1, EffectiveDay: 50,
		Changes: map[string]int{"PRICE": 2}}), "add4")
	wantCode(t, s4.Sign("F", "M1", "B", 29), ErrAuthExpired, "B before window")

	// s3：A 第 30 日签，B 恰在上界 40：授权含端点，且 40==30+10 签署未超期。
	mustOK(t, s3.Sign("E", "M1", "A", 30), "a sign at 30")
	mustOK(t, s3.Sign("E", "M1", "B", 40), "B upper endpoint + equal deadline")
	mustOK(t, s2.Sign("D", "M1", "A", 30), "a sign")
	mustOK(t, s2.Sign("D", "M1", "B", 40), "equal deadline")
}
