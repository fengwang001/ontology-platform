package contract

import (
	"reflect"
	"testing"
)

// stdParams 返回标准测试合同参数：
// 条款 1=100（普通）、2=200（锁定）、3=自动续签开关、4=续签期长度、5=通知提前天数；
// 双方 A/B，起始日 10，初始到期日 20，签署窗口 3 天。
func stdParams() ContractParams {
	return ContractParams{
		Parties:         [2]string{"A", "B"},
		Clauses:         map[int]int{1: 100, 2: 200, 3: 1, 4: 5, 5: 2},
		Locked:          []int{2},
		Start:           10,
		Expiry:          20,
		AutoRenewClause: 3,
		PeriodClause:    4,
		NoticeClause:    5,
		SignWindowDays:  3,
	}
}

func newStdService(t *testing.T) *Service {
	t.Helper()
	s := NewService()
	res := s.Apply(Op{Kind: OpCreateContract, Now: 0, Params: stdParams()})
	if res.Err != None {
		t.Fatalf("create contract: %v", res.Message)
	}
	return s
}

func ok(t *testing.T, s *Service, op Op) OpResult {
	t.Helper()
	res := s.Apply(op)
	if res.Err != None {
		t.Fatalf("op %+v: unexpected error %v: %v", op, res.Err, res.Message)
	}
	return res
}

func rejected(t *testing.T, s *Service, op Op, want Category) {
	t.Helper()
	res := s.Apply(op)
	if res.Err != want {
		t.Fatalf("op %+v: got %v (%v), want %v", op, res.Err, res.Message, want)
	}
}

// addAmendment 创建修改类协议并返回其编号。
func addAmendment(t *testing.T, s *Service, now, declared int, mods map[int]int) int {
	t.Helper()
	res := ok(t, s, Op{Kind: OpCreateAmendment, Now: now, Contract: 0, DeclaredEffDay: declared, Mods: mods})
	return res.AmendmentID
}

// signBoth 双方依次签署（授权窗口足够宽）。
func signBoth(t *testing.T, s *Service, nowA, nowB, aid int) {
	t.Helper()
	ok(t, s, Op{Kind: OpSign, Now: nowA, Contract: 0, Amendment: aid, Party: "A", AuthFrom: 0, AuthTo: 1000})
	ok(t, s, Op{Kind: OpSign, Now: nowB, Contract: 0, Amendment: aid, Party: "B", AuthFrom: 0, AuthTo: 1000})
}

// revoke 创建撤销协议并双方签署，返回其编号。
func revoke(t *testing.T, s *Service, now, declared, target, signA, signB int) int {
	t.Helper()
	res := ok(t, s, Op{Kind: OpCreateAmendment, Now: now, Contract: 0, DeclaredEffDay: declared, IsRevocation: true, RevokeTarget: target})
	signBoth(t, s, signA, signB, res.AmendmentID)
	return res.AmendmentID
}

// queryValue 查询条款 1 在某日有效值。
func queryValue(t *testing.T, s *Service, now, clause, day int) OpResult {
	t.Helper()
	return ok(t, s, Op{Kind: OpQueryValue, Now: now, Contract: 0, Clause: clause, Day: day})
}

func wantValue(t *testing.T, s *Service, now, clause, day, wantVal int, wantMaster bool, wantAID int) {
	t.Helper()
	res := queryValue(t, s, now, clause, day)
	if res.Value != wantVal || res.Source.Master != wantMaster || res.Source.AmendmentID != wantAID {
		t.Fatalf("value(clause=%d, day=%d) = (%d, %v), want (%d, master=%v aid=%d)",
			clause, day, res.Value, res.Source, wantVal, wantMaster, wantAID)
	}
}

func wantInForce(t *testing.T, s *Service, now, day int, want bool) {
	t.Helper()
	res := ok(t, s, Op{Kind: OpQueryInForce, Now: now, Contract: 0, Day: day})
	if res.InForce != want {
		t.Fatalf("inForce(day=%d) = %v, want %v", day, res.InForce, want)
	}
}

// wantInForceAt 指定合同的在期断言。
func wantInForceAt(t *testing.T, s *Service, now, cid, day int, want bool) {
	t.Helper()
	res := ok(t, s, Op{Kind: OpQueryInForce, Now: now, Contract: cid, Day: day})
	if res.InForce != want {
		t.Fatalf("contract %d inForce(day=%d) = %v, want %v", cid, day, res.InForce, want)
	}
}

func wantRenewals(t *testing.T, s *Service, now, cid int, want []Renewal) {
	t.Helper()
	res := ok(t, s, Op{Kind: OpQueryRenewals, Now: now, Contract: cid})
	got := res.Renewals
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contract %d renewals = %+v, want %+v", cid, got, want)
	}
}

func wantExpiry(t *testing.T, s *Service, now, cid, want int) {
	t.Helper()
	res := ok(t, s, Op{Kind: OpQueryExpiry, Now: now, Contract: cid})
	if res.Expiry != want {
		t.Fatalf("contract %d expiry = %d, want %d", cid, res.Expiry, want)
	}
}

// 生效日恰等：生效日当日即生效；声明生效日早于签署完成日时以签署完成日为准。
func TestEffectiveDayExactBoundary(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 15, map[int]int{1: 111})
	signBoth(t, s, 12, 13, am0) // 签署完成日 13 < 声明生效日 15 → 生效日 15

	wantValue(t, s, 14, 1, 14, 100, true, -1)
	wantValue(t, s, 15, 1, 15, 111, false, am0) // 恰等生效日，生效

	am1 := addAmendment(t, s, 15, 12, map[int]int{1: 222})
	signBoth(t, s, 15, 16, am1) // 声明生效日 12 < 签署完成日 16 → 以 16 为准

	wantValue(t, s, 16, 1, 15, 111, false, am0)
	wantValue(t, s, 16, 1, 16, 222, false, am1)
}

// 同日多协议取舍：生效日相同取签署完成时刻较后者。
func TestSameDayTieBreak(t *testing.T) {
	// 子场景 1：生效日相同，签署完成日较晚者胜。
	s1 := newStdService(t)
	am0 := addAmendment(t, s1, 10, 15, map[int]int{1: 111})
	am1 := addAmendment(t, s1, 10, 15, map[int]int{1: 222})
	signBoth(t, s1, 11, 12, am0) // 完成于 12
	signBoth(t, s1, 12, 13, am1) // 完成于 13
	wantValue(t, s1, 15, 1, 15, 222, false, am1)
	wantValue(t, s1, 15, 1, 14, 100, true, -1)

	// 子场景 2：签署完成日也相同，操作序号较晚者胜。
	s2 := newStdService(t)
	bm0 := addAmendment(t, s2, 10, 15, map[int]int{1: 111})
	bm1 := addAmendment(t, s2, 10, 15, map[int]int{1: 222})
	bm2 := addAmendment(t, s2, 10, 15, map[int]int{1: 333})
	signBoth(t, s2, 11, 12, bm0)
	signBoth(t, s2, 12, 13, bm1)
	signBoth(t, s2, 13, 13, bm2) // 与 bm1 同日完成，序号更晚
	wantValue(t, s2, 15, 1, 15, 333, false, bm2)
}

// 授权有效期两端：now 落在 [authFrom, authTo] 内（含两端）方可签署。
func TestAuthWindowBounds(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 12, map[int]int{1: 111})
	ok(t, s, Op{Kind: OpSign, Now: 10, Contract: 0, Amendment: am0, Party: "A", AuthFrom: 10, AuthTo: 12}) // 下端恰等
	ok(t, s, Op{Kind: OpSign, Now: 12, Contract: 0, Amendment: am0, Party: "B", AuthFrom: 10, AuthTo: 12}) // 上端恰等

	am1 := addAmendment(t, s, 13, 16, map[int]int{1: 222})
	rejected(t, s, Op{Kind: OpSign, Now: 13, Contract: 0, Amendment: am1, Party: "A", AuthFrom: 14, AuthTo: 20}, AuthExpired) // now < authFrom
	rejected(t, s, Op{Kind: OpSign, Now: 13, Contract: 0, Amendment: am1, Party: "A", AuthFrom: 10, AuthTo: 12}, AuthExpired) // now > authTo
	ok(t, s, Op{Kind: OpSign, Now: 13, Contract: 0, Amendment: am1, Party: "A", AuthFrom: 13, AuthTo: 13})                    // 窗口恰为单日
}

// 签署超期恰等：首签后第 signWindow 天仍可签，第 signWindow+1 天拒绝。
func TestSignExpiryExactBound(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 15, map[int]int{1: 111})
	am1 := addAmendment(t, s, 10, 15, map[int]int{1: 222})
	ok(t, s, Op{Kind: OpSign, Now: 10, Contract: 0, Amendment: am0, Party: "A", AuthFrom: 0, AuthTo: 100})
	ok(t, s, Op{Kind: OpSign, Now: 10, Contract: 0, Amendment: am1, Party: "A", AuthFrom: 0, AuthTo: 100})

	ok(t, s, Op{Kind: OpSign, Now: 13, Contract: 0, Amendment: am0, Party: "B", AuthFrom: 0, AuthTo: 100}) // 10+3，恰等窗口，可签

	rejected(t, s, Op{Kind: OpSign, Now: 14, Contract: 0, Amendment: am1, Party: "B", AuthFrom: 0, AuthTo: 100}, SignExpired) // 超过 10+3

	// 被拒绝的操作不推进时钟：now=13 仍被接受（对 am1 已超期，再验证一次拒绝）。
	rejected(t, s, Op{Kind: OpSign, Now: 14, Contract: 0, Amendment: am1, Party: "B", AuthFrom: 0, AuthTo: 100}, SignExpired)
	ok(t, s, Op{Kind: OpQueryInForce, Now: 14, Contract: 0, Day: 10})
}

// 缺会签后补会签：锁定条款协议双方签完仍不生效，补齐会签的时刻起方可生效。
func TestCountersignSupplement(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 15, map[int]int{2: 999}) // 锁定条款
	signBoth(t, s, 12, 13, am0)

	am1 := addAmendment(t, s, 14, 16, map[int]int{2: 888}) // 锁定条款
	signBoth(t, s, 15, 16, am1)

	// 双方已签但缺会签：不生效
	wantValue(t, s, 17, 2, 17, 200, true, -1)
	// 对缺会签的协议发起撤销：报缺少会签
	rejected(t, s, Op{Kind: OpCreateAmendment, Now: 17, Contract: 0, DeclaredEffDay: 20, IsRevocation: true, RevokeTarget: am1}, CountersignMissing)

	ok(t, s, Op{Kind: OpCountersign, Now: 18, Contract: 0, Amendment: am0}) // 补会签 → 生效日 max(15,13,18)=18
	wantValue(t, s, 18, 2, 17, 200, true, -1)
	wantValue(t, s, 18, 2, 18, 999, false, am0)

	ok(t, s, Op{Kind: OpCountersign, Now: 19, Contract: 0, Amendment: am1}) // 生效日 max(16,16,19)=19
	wantValue(t, s, 19, 2, 19, 888, false, am1)

	// 会签补齐后可被撤销
	rev := revoke(t, s, 19, 20, am1, 19, 20)
	wantValue(t, s, 20, 2, 20, 999, false, am0)
	_ = rev
}

// 不续签通知恰等提前天数：通知日 == 到期日-提前天数 算及时，到期终止；晚一天则续签。
func TestNoticeExactAdvanceDays(t *testing.T) {
	s := NewService()
	ok(t, s, Op{Kind: OpCreateContract, Now: 0, Params: stdParams()}) // 合同 0
	ok(t, s, Op{Kind: OpCreateContract, Now: 0, Params: stdParams()}) // 合同 1
	p2 := stdParams()
	p2.Clauses[3] = 0                                        // 关闭自动续签
	ok(t, s, Op{Kind: OpCreateContract, Now: 0, Params: p2}) // 合同 2

	// 合同 0：通知日 18 == 20-2，恰等提前天数 → 及时，到期终止
	ok(t, s, Op{Kind: OpNotice, Now: 18, Contract: 0, Party: "A"})
	// 合同 1：通知日 19 > 20-2，不及时 → 自动续签
	ok(t, s, Op{Kind: OpNotice, Now: 19, Contract: 1, Party: "B"})

	wantInForceAt(t, s, 21, 0, 20, true)
	wantInForceAt(t, s, 21, 0, 21, false)
	wantInForceAt(t, s, 21, 1, 21, true)
	wantInForceAt(t, s, 21, 2, 21, false) // 未开自动续签，到期自然终止

	wantRenewals(t, s, 22, 0, nil)
	wantRenewals(t, s, 22, 1, []Renewal{{OldExpiry: 20, NewExpiry: 25, Period: 5, NoticeDays: 2}})
	wantRenewals(t, s, 22, 2, nil)

	wantExpiry(t, s, 22, 0, 20)
	wantExpiry(t, s, 22, 1, 25)

	// 合同 1 第二期到期 25 时无新通知（首任期通知不跨期），继续续签到 30
	wantInForceAt(t, s, 26, 1, 26, true)
	wantRenewals(t, s, 26, 1, []Renewal{
		{OldExpiry: 20, NewExpiry: 25, Period: 5, NoticeDays: 2},
		{OldExpiry: 25, NewExpiry: 30, Period: 5, NoticeDays: 2},
	})
}

// 续签期长度来自被修改的条款：到期日当日有效值决定续签判定。
func TestRenewalPeriodFromAmendedClause(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 15, map[int]int{4: 7}) // 续签期长度 5 → 7
	signBoth(t, s, 12, 13, am0)
	am1 := addAmendment(t, s, 20, 28, map[int]int{3: 0}) // 生效日 28 起关闭自动续签
	signBoth(t, s, 25, 26, am1)

	wantRenewals(t, s, 30, 0, []Renewal{
		{OldExpiry: 20, NewExpiry: 27, Period: 7, NoticeDays: 2},
		{OldExpiry: 27, NewExpiry: 34, Period: 7, NoticeDays: 2},
	})
	wantExpiry(t, s, 30, 0, 34)
	wantInForce(t, s, 30, 33, true)

	// 到期日 34 当日自动续签开关已为 0 → 到期终止
	wantInForce(t, s, 40, 34, true)
	wantInForce(t, s, 40, 35, false)
	wantRenewals(t, s, 40, 0, []Renewal{
		{OldExpiry: 20, NewExpiry: 27, Period: 7, NoticeDays: 2},
		{OldExpiry: 27, NewExpiry: 34, Period: 7, NoticeDays: 2},
	})
	wantExpiry(t, s, 40, 0, 34)
}

// 提前终止后协议：终止日之后的协议一律不生效，当日及之前已生效的保持；
// 终止后只允许查询。
func TestEarlyTermination(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 15, map[int]int{1: 111})
	signBoth(t, s, 12, 13, am0) // 生效日 15
	am1 := addAmendment(t, s, 14, 25, map[int]int{1: 222})
	signBoth(t, s, 14, 15, am1) // 生效日 25，晚于终止日

	ok(t, s, Op{Kind: OpTerminate, Now: 18, Contract: 0})

	wantValue(t, s, 19, 1, 17, 111, false, am0)
	wantValue(t, s, 19, 1, 18, 111, false, am0) // 终止日当日已生效的保持
	wantValue(t, s, 19, 1, 20, 111, false, am0) // 终止日之后的协议（am1）不生效
	wantValue(t, s, 19, 1, 30, 111, false, am0)

	wantInForce(t, s, 19, 18, true)
	wantInForce(t, s, 19, 19, false)
	wantRenewals(t, s, 19, 0, nil) // 不再续签
	wantExpiry(t, s, 19, 0, 18)

	// 终止后只允许查询
	rejected(t, s, Op{Kind: OpCreateAmendment, Now: 19, Contract: 0, DeclaredEffDay: 20, Mods: map[int]int{1: 1}}, StateNotAllowed)
	rejected(t, s, Op{Kind: OpSign, Now: 19, Contract: 0, Amendment: am0, Party: "A", AuthFrom: 0, AuthTo: 100}, StateNotAllowed)
	rejected(t, s, Op{Kind: OpNotice, Now: 19, Contract: 0, Party: "A"}, StateNotAllowed)
	rejected(t, s, Op{Kind: OpTerminate, Now: 19, Contract: 0}, StateNotAllowed)
	rejected(t, s, Op{Kind: OpCountersign, Now: 19, Contract: 0, Amendment: am1}, StateNotAllowed)
}

// 拒绝优先级：多重违规同时成立时只报优先级最高者；
// 优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许 > 授权失效 > 缺少会签 > 签署超期。
func TestErrorPriority(t *testing.T) {
	s := newStdService(t) // 时钟推进到 0
	ok(t, s, Op{Kind: OpQueryInForce, Now: 10, Contract: 0, Day: 10})

	am0 := addAmendment(t, s, 10, 12, map[int]int{1: 111})
	signBoth(t, s, 10, 11, am0) // am0 已签完，生效日 12
	am1 := addAmendment(t, s, 11, 15, map[int]int{1: 222})
	ok(t, s, Op{Kind: OpSign, Now: 11, Contract: 0, Amendment: am1, Party: "A", AuthFrom: 0, AuthTo: 100}) // 首签于 11，窗口 3

	// 参数非法 > 时钟回退：授权窗口倒置 且 now 回退
	rejected(t, s, Op{Kind: OpSign, Now: 5, Contract: 0, Amendment: am1, Party: "B", AuthFrom: 9, AuthTo: 8}, InvalidParam)
	// 时钟回退 > 不存在：now 回退 且 合同不存在
	rejected(t, s, Op{Kind: OpSign, Now: 5, Contract: 99, Amendment: 0, Party: "B", AuthFrom: 0, AuthTo: 9}, ClockRollback)
	// 不存在 > 状态不允许：协议不存在 且 该方重复签署（am0 中 A 已签）
	rejected(t, s, Op{Kind: OpSign, Now: 12, Contract: 0, Amendment: 99, Party: "A", AuthFrom: 0, AuthTo: 100}, NotFound)
	// 状态不允许 > 授权失效：am0 已签完（状态）且授权窗口不含 now
	rejected(t, s, Op{Kind: OpSign, Now: 12, Contract: 0, Amendment: am0, Party: "A", AuthFrom: 13, AuthTo: 20}, StateNotAllowed)
	// 授权失效 > 签署超期：授权窗口不含 now 且 已超期（11+3=14 < 16）
	rejected(t, s, Op{Kind: OpSign, Now: 16, Contract: 0, Amendment: am1, Party: "B", AuthFrom: 20, AuthTo: 30}, AuthExpired)
	// 签署超期：授权合法但超期
	rejected(t, s, Op{Kind: OpSign, Now: 16, Contract: 0, Amendment: am1, Party: "B", AuthFrom: 0, AuthTo: 100}, SignExpired)

	// 缺少会签：目标已签完但缺会签（am2 修改锁定条款）
	am2 := addAmendment(t, s, 16, 18, map[int]int{2: 999})
	signBoth(t, s, 16, 17, am2)
	rejected(t, s, Op{Kind: OpCreateAmendment, Now: 18, Contract: 0, DeclaredEffDay: 20, IsRevocation: true, RevokeTarget: am2}, CountersignMissing)
	// 状态不允许 > 缺少会签：目标尚未签完（am3），谈不上会签
	am3 := addAmendment(t, s, 18, 20, map[int]int{2: 888})
	rejected(t, s, Op{Kind: OpCreateAmendment, Now: 18, Contract: 0, DeclaredEffDay: 20, IsRevocation: true, RevokeTarget: am3}, StateNotAllowed)

	// 被拒绝的操作不改变时钟：now=18 与当前一致，后续 now=18 的操作仍被接受
	ok(t, s, Op{Kind: OpQueryInForce, Now: 18, Contract: 0, Day: 18})
}

// 单条款有效值查询开销不随合同累计协议总数增长：
// 以二分查找比较次数为可验证指标。
func TestQueryCostIndependentOfAmendmentCount(t *testing.T) {
	params := stdParams()
	params.Start = 0
	params.Expiry = 1 << 29 // 足够远，避免续签判定干扰计数
	params.Locked = nil
	s := NewService()
	ok(t, s, Op{Kind: OpCreateContract, Now: 0, Params: params})

	const K = 2000
	// 条款 1 上叠加 K 份协议，生效日分别为 0..K-1
	for i := 0; i < K; i++ {
		res := ok(t, s, Op{Kind: OpCreateAmendment, Now: i, Contract: 0, DeclaredEffDay: i, Mods: map[int]int{1: i}})
		ok(t, s, Op{Kind: OpSign, Now: i, Contract: 0, Amendment: res.AmendmentID, Party: "A", AuthFrom: 0, AuthTo: 1 << 30})
		ok(t, s, Op{Kind: OpSign, Now: i, Contract: 0, Amendment: res.AmendmentID, Party: "B", AuthFrom: 0, AuthTo: 1 << 30})
	}

	// 未被任何协议触及的条款：0 次比较，与协议总数无关
	resetQueryComparisons()
	ok(t, s, Op{Kind: OpQueryValue, Now: K, Contract: 0, Clause: 2, Day: K - 1})
	if got := getQueryComparisons(); got != 0 {
		t.Fatalf("untouched clause query used %d comparisons, want 0", got)
	}

	// 条款 1：先预热（触发时间线重建），再测量稳态查询比较次数
	ok(t, s, Op{Kind: OpQueryValue, Now: K, Contract: 0, Clause: 1, Day: K - 1})
	resetQueryComparisons()
	res := ok(t, s, Op{Kind: OpQueryValue, Now: K, Contract: 0, Clause: 1, Day: K - 1})
	if res.Value != K-1 {
		t.Fatalf("value = %d, want %d", res.Value, K-1)
	}
	first := getQueryComparisons()
	if first == 0 || first > 32 { // log2(2000) ≈ 11
		t.Fatalf("clause-1 query used %d comparisons, want in (0, 32]", first)
	}

	// 再向条款 4 叠加 K 份协议（合同累计协议数翻倍）
	for i := 0; i < K; i++ {
		now := K + i
		res := ok(t, s, Op{Kind: OpCreateAmendment, Now: now, Contract: 0, DeclaredEffDay: now, Mods: map[int]int{4: i}})
		ok(t, s, Op{Kind: OpSign, Now: now, Contract: 0, Amendment: res.AmendmentID, Party: "A", AuthFrom: 0, AuthTo: 1 << 30})
		ok(t, s, Op{Kind: OpSign, Now: now, Contract: 0, Amendment: res.AmendmentID, Party: "B", AuthFrom: 0, AuthTo: 1 << 30})
	}

	// 条款 1 的查询比较次数不变：开销不随合同累计协议总数增长
	resetQueryComparisons()
	ok(t, s, Op{Kind: OpQueryValue, Now: 2 * K, Contract: 0, Clause: 1, Day: K - 1})
	if got := getQueryComparisons(); got != first {
		t.Fatalf("clause-1 query comparisons changed from %d to %d after doubling total amendments", first, got)
	}
	t.Logf("clause-1 steady-state query comparisons: %d (K=%d amendments on the clause, %d total)",
		first, K, 2*K)
}

// 撤销与再撤销：撤销自其生效日起使目标修改失效，历史查询不受影响；
// 后续协议仍按自己的取值生效；撤销可被再次撤销。
func TestRevokeAndReRevoke(t *testing.T) {
	s := newStdService(t)

	am0 := addAmendment(t, s, 10, 12, map[int]int{1: 111})
	signBoth(t, s, 10, 11, am0) // 生效日 12
	am1 := addAmendment(t, s, 11, 15, map[int]int{1: 222})
	signBoth(t, s, 11, 12, am1) // 生效日 15

	wantValue(t, s, 14, 1, 14, 111, false, am0)
	wantValue(t, s, 16, 1, 16, 222, false, am1)

	am2 := revoke(t, s, 16, 18, am0, 16, 17) // 撤销 am0，生效日 18
	wantValue(t, s, 17, 1, 17, 222, false, am1)
	// am0 被撤销，但后续协议 am1 仍按自己的取值生效（不回落主合同）
	wantValue(t, s, 18, 1, 18, 222, false, am1)

	_ = revoke(t, s, 18, 21, am1, 18, 19) // 撤销 am1，生效日 21
	wantValue(t, s, 20, 1, 20, 222, false, am1)
	wantValue(t, s, 21, 1, 21, 100, true, -1) // am0、am1 均被撤销 → 主合同原值

	_ = revoke(t, s, 21, 25, am2, 21, 22) // 再撤销（撤销 am2）→ 恢复 am0
	wantValue(t, s, 24, 1, 24, 100, true, -1)
	wantValue(t, s, 25, 1, 25, 111, false, am0)

	_ = revoke(t, s, 25, 29, am0, 25, 26) // 再次直接撤销 am0
	wantValue(t, s, 28, 1, 28, 111, false, am0)
	wantValue(t, s, 29, 1, 29, 100, true, -1)

	// 历史查询不受后续撤销影响
	wantValue(t, s, 30, 1, 14, 111, false, am0)
	wantValue(t, s, 30, 1, 16, 222, false, am1)
}
