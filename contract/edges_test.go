package contract

import "testing"

// 签署超期恰等：second day == first + deadline 允许；+1 拒绝。
func TestSigningDeadlineExact(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 500)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1, EffectiveDay: 100,
		Changes: map[string]int{"PRICE": 2}}), "add")
	mustOK(t, s.Sign("C", "M1", "A", signingDeadlineDays), "a")
	mustOK(t, s.Sign("C", "M1", "B", 2*signingDeadlineDays), "b equal")

	s2 := NewService()
	mustOK(t, s2.CreateContract(baseContract("D", 0, 0, 500)), "create2")
	mustOK(t, s2.AddAmendment(AddAmendmentInput{
		ContractID: "D", AmendmentID: "M1", Now: 0, EffectiveDay: 100,
		Changes: map[string]int{"PRICE": 2}}), "add2")
	mustOK(t, s2.Sign("D", "M1", "A", 0), "a2")
	wantCode(t, s2.Sign("D", "M1", "B", signingDeadlineDays+1),
		ErrSigningExpired, "one day late")
	// 被拒绝的操作不得改变状态：恰等日仍可签。
	mustOK(t, s2.Sign("D", "M1", "B", signingDeadlineDays), "b equal after reject")
}

// 缺会签时即使双方已签也不生效；补齐会签之日起生效（生效日后移到会签日）。
func TestLockedCosignLate(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 500)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1, EffectiveDay: 5,
		Changes: map[string]int{"LOCKED_TERM": 9}}), "add")
	mustOK(t, s.Sign("C", "M1", "A", 5), "a")
	mustOK(t, s.Sign("C", "M1", "B", 6), "b")
	v, _ := s.EffectiveValue("C", "LOCKED_TERM", 6)
	if v.Kind != "MAIN" {
		t.Fatalf("without cosign, value = %+v", v)
	}
	mustOK(t, s.LegalCosign("C", "M1", 20), "cosign")
	v2, _ := s.EffectiveValue("C", "LOCKED_TERM", 19)
	if v2.Kind != "MAIN" {
		t.Fatalf("day19 = %+v", v2)
	}
	v3, _ := s.EffectiveValue("C", "LOCKED_TERM", 20)
	if v3.Value != 9 || v3.EffectiveDay != 20 {
		t.Fatalf("day20 = %+v, want 9@20", v3)
	}
}

// 不续签通知恰等于提前天数即算及时：到期终止、不续签。
func TestNoticeExactlyInTime(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100)), "create")
	// expiry=100, notice=10 -> 第 90 日通知恰及时。
	mustOK(t, s.NoticeNonRenewal("C", "A", 90), "notice")
	in99, _ := s.InTerm("C", 99)
	if !in99 {
		t.Fatalf("day99 should be in term")
	}
	in100, _ := s.InTerm("C", 100)
	if !in100 {
		t.Fatalf("expiry day itself is in term")
	}
	in101, _ := s.InTerm("C", 101)
	if in101 {
		t.Fatalf("day101 must be terminated")
	}
	recs, _ := s.Renewals("C", 200)
	if len(recs) != 0 {
		t.Fatalf("no renewal expected, got %+v", recs)
	}
	exp, _ := s.CurrentExpiry("C", 200)
	if exp != 100 {
		t.Fatalf("expiry=%d", exp)
	}
}

// 通知晚一天则续签。
func TestNoticeOneDayLateRenews(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100)), "create")
	mustOK(t, s.NoticeNonRenewal("C", "A", 91), "notice late")
	in101, _ := s.InTerm("C", 101)
	if !in101 {
		t.Fatalf("should renew into next term")
	}
	exp, _ := s.CurrentExpiry("C", 101)
	if exp != 130 {
		t.Fatalf("new expiry=%d want 130", exp)
	}
	recs, _ := s.Renewals("C", 101)
	if len(recs) != 1 || recs[0].FromExpiryDay != 100 || recs[0].NewExpiryDay != 130 ||
		recs[0].TermLength != 30 {
		t.Fatalf("recs=%+v", recs)
	}
}

// 续签期长度取到期日当日有效的条款值，且可连续续签。
func TestRenewalTermFromAmendedClause(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 100)), "create")
	// 在第 100 日把续签期改为 15。
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 50, EffectiveDay: 100,
		Changes: map[string]int{ClauseRenewalTerm: 15}}), "add")
	mustOK(t, s.Sign("C", "M1", "A", 51), "a")
	mustOK(t, s.Sign("C", "M1", "B", 52), "b")
	exp, _ := s.CurrentExpiry("C", 101)
	if exp != 115 {
		t.Fatalf("first renewed expiry=%d want 115", exp)
	}
	// 第 115 日当日 M1 仍有效，第二期仍为 15。
	exp2, _ := s.CurrentExpiry("C", 116)
	if exp2 != 130 {
		t.Fatalf("second renewed expiry=%d want 130", exp2)
	}
	recs, _ := s.Renewals("C", 116)
	if len(recs) != 2 {
		t.Fatalf("recs=%+v", recs)
	}
}

// 提前终止后：终止日之后的协议不生效；当日及之前的保持。
func TestEarlyTerminationFreeze(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 500)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "OLD", Now: 1, EffectiveDay: 40,
		Changes: map[string]int{"PRICE": 200}}), "add old")
	mustOK(t, s.Sign("C", "OLD", "A", 2), "old a")
	mustOK(t, s.Sign("C", "OLD", "B", 3), "old b")

	// NEW 双方已签但需要会签，拖到终止之后才补会签：不生效。
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "NEW", Now: 4, EffectiveDay: 10,
		Changes: map[string]int{"LOCKED_TERM": 2}}), "add new")
	mustOK(t, s.Sign("C", "NEW", "A", 5), "new a")
	mustOK(t, s.Sign("C", "NEW", "B", 6), "new b")

	mustOK(t, s.AgreeEarlyTermination("C", "A", 50), "term a")
	mustOK(t, s.AgreeEarlyTermination("C", "B", 50), "term b")

	wantCode(t, s.LegalCosign("C", "NEW", 51), ErrIllegalState, "no op after term")
	wantCode(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "X", Now: 52, EffectiveDay: 52,
		Changes: map[string]int{"PRICE": 1}}), ErrIllegalState, "add after term")

	in, _ := s.InTerm("C", 50)
	if !in {
		t.Fatalf("term day in term")
	}
	in2, _ := s.InTerm("C", 51)
	if in2 {
		t.Fatalf("after term not in term")
	}
	v, _ := s.EffectiveValue("C", "PRICE", 60)
	if v.Value != 200 {
		t.Fatalf("old amendment survives: %+v", v)
	}
	v2, _ := s.EffectiveValue("C", "LOCKED_TERM", 60)
	if v2.Value != 1 || v2.Kind != "MAIN" {
		t.Fatalf("new amendment must not take effect: %+v", v2)
	}
}

// 时钟回退：now 小于上一次被接受操作即拒绝，且不改变状态。
func TestClockRollback(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 10, 0, 100)), "create")
	wantCode(t, s.NoticeNonRenewal("C", "A", 9), ErrClockRollback, "rollback")
	mustOK(t, s.NoticeNonRenewal("C", "A", 10), "same now allowed")
}

// 错误优先级：参数非法 > 时钟回退 > 不存在 > 状态 > 授权 > 会签 > 超期。
func TestErrorPriority(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 100, 0, 500)), "create")
	// 空白 ID（参数非法）且时钟回退：先报参数非法。
	wantCode(t, s.Sign("", "M", "", 0), ErrInvalidParam, "blank first")
	// 合同不存在 + 时钟回退：时钟优先。
	wantCode(t, s.Sign("NOPE", "M", "A", 0), ErrClockRollback, "clock over missing")
	// 合同存在、协议不存在 + 时钟正常：不存在。
	wantCode(t, s.Sign("C", "NOPE", "A", 100), ErrNotFound, "missing amendment")
	// 授权失效优先于超期：构造 A 在 0 日签，B 授权窗口很早且已过，同时签署超期。
	in := baseContract("D", 100, 0, 500)
	in.Parties[1].AuthFrom = 0
	in.Parties[1].AuthUntil = 5
	mustOK(t, s.CreateContract(in), "create2")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "D", AmendmentID: "M1", Now: 100, EffectiveDay: 200,
		Changes: map[string]int{"PRICE": 2}}), "add")
	mustOK(t, s.Sign("D", "M1", "A", 100), "a")
	wantCode(t, s.Sign("D", "M1", "B", 100), ErrAuthExpired, "auth over expiry")
}

// 被撤销协议曾被后续协议依赖：撤销不影响后续协议自身取值。
func TestRevokeDoesNotUnwindDependents(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 500)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1, EffectiveDay: 10,
		Changes: map[string]int{"PRICE": 200}}), "m1")
	mustOK(t, s.Sign("C", "M1", "A", 2), "m1a")
	mustOK(t, s.Sign("C", "M1", "B", 3), "m1b")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M2", Now: 4, EffectiveDay: 20,
		Changes: map[string]int{"PRICE": 300}}), "m2")
	mustOK(t, s.Sign("C", "M2", "A", 5), "m2a")
	mustOK(t, s.Sign("C", "M2", "B", 6), "m2b")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "R1", Now: 7, EffectiveDay: 30,
		Revokes: "M1"}), "r1")
	mustOK(t, s.Sign("C", "R1", "A", 8), "r1a")
	mustOK(t, s.Sign("C", "R1", "B", 9), "r1b")
	v, _ := s.EffectiveValue("C", "PRICE", 30)
	if v.Value != 300 || v.AmendmentID != "M2" {
		t.Fatalf("M2 must survive M1 revocation: %+v", v)
	}
}

// 声明生效日早于签署完成日：生效日后移至完成日。
func TestEffectiveDayMovedToCompletion(t *testing.T) {
	s := NewService()
	mustOK(t, s.CreateContract(baseContract("C", 0, 0, 500)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "C", AmendmentID: "M1", Now: 1, EffectiveDay: 5,
		Changes: map[string]int{"PRICE": 200}}), "add")
	mustOK(t, s.Sign("C", "M1", "A", 20), "a")
	mustOK(t, s.Sign("C", "M1", "B", 21), "b")
	v20, _ := s.EffectiveValue("C", "PRICE", 20)
	if v20.Kind != "MAIN" {
		t.Fatalf("day20 before completion = %+v", v20)
	}
	v21, _ := s.EffectiveValue("C", "PRICE", 21)
	if v21.Value != 200 || v21.EffectiveDay != 21 {
		t.Fatalf("day21 = %+v, want 200@21", v21)
	}
}
