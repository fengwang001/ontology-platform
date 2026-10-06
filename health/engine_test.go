package health

import (
	"errors"
	"testing"
)

func mustCode(t *testing.T, e *Engine, in CodeInput) {
	t.Helper()
	if err := e.AddCode(in); err != nil {
		t.Fatalf("AddCode(%+v): %v", in, err)
	}
}

func mustPolicy(t *testing.T, e *Engine, in PolicyInput) {
	t.Helper()
	if err := e.RegisterPolicy(in); err != nil {
		t.Fatalf("RegisterPolicy(%+v): %v", in, err)
	}
}

func reasons(r *ClaimResult) []string {
	out := make([]string, len(r.Verdicts))
	for i, v := range r.Verdicts {
		out[i] = v.Reason
	}
	return out
}

func testCodes(t *testing.T, e *Engine) {
	t.Helper()
	// 层级：ROOT -> CARD -> A；ROOT -> OTHER；ACC 为意外类，ACC2 继承意外。
	mustCode(t, e, CodeInput{Code: "ROOT"})
	mustCode(t, e, CodeInput{Code: "CARD", Parent: "ROOT"})
	mustCode(t, e, CodeInput{Code: "A", Parent: "CARD"})
	mustCode(t, e, CodeInput{Code: "OTHER", Parent: "ROOT"})
	mustCode(t, e, CodeInput{Code: "ACC", Accident: true})
	mustCode(t, e, CodeInput{Code: "ACC2", Parent: "ACC"})
}

// 等待期最后一天不赔；次日零点赔付；生效日当天算第 1 天。
func TestWaitingBoundary(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 10, End: 40, WaitDays: 3, Amount: 100000})

	day10, err := e.SubmitClaim(ClaimInput{ID: "c1", Person: "u", Day: 10,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 100}}})
	if err != nil || day10.Payout != 0 || day10.Verdicts[0].Reason != ReasonWaiting {
		t.Fatalf("生效日当天应在等待期: %+v err=%v", day10, err)
	}
	if got := e.ArchiveSnapshot("u"); len(got) != 1 || got[0] != "A" {
		t.Fatalf("等待期诊断应入档案: %v", got)
	}
	day12, err := e.SubmitClaim(ClaimInput{ID: "c2", Person: "u", Day: 12,
		Diagnoses: []Diagnosis{{Code: "OTHER", Fee: 100}}})
	if err != nil || day12.Payout != 0 || day12.Verdicts[0].Reason != ReasonWaiting {
		t.Fatalf("等待期最后一天(12)应不赔: %+v err=%v", day12, err)
	}
	day13, err := e.SubmitClaim(ClaimInput{ID: "c3", Person: "u", Day: 13,
		Diagnoses: []Diagnosis{{Code: "CARD", Fee: 100}}})
	if err != nil || day13.Payout != 100 || day13.Verdicts[0].Reason != ReasonPaid {
		t.Fatalf("次日零点(13)应赔付: %+v err=%v", day13, err)
	}
}

// 到期日当天不在覆盖区间 [Start,End)。
func TestExpiryDayNotCovered(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 10, WaitDays: 0, Amount: 100})
	_, err := e.SubmitClaim(ClaimInput{ID: "x", Person: "u", Day: 10,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}})
	if !errors.Is(err, ErrUninsuredDate) {
		t.Fatalf("到期日当天应未承保, got %v", err)
	}
	r, err := e.SubmitClaim(ClaimInput{ID: "y", Person: "u", Day: 9,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}})
	if err != nil || r.Payout != 1 {
		t.Fatalf("到期前一天应承保: %v %v", r, err)
	}
}

// 续保恰在到期日登记且生效日恰等于到期日：无等待期；晚一天登记为新投保。
func TestRenewalVsNewBusiness(t *testing.T) {
	mk := func(regAt int64) *Engine {
		e := NewEngine()
		testCodes(t, e)
		mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 10, WaitDays: 0, Amount: 1000})
		if err := e.RegisterPolicy(PolicyInput{Person: "u", RegisteredAt: regAt, Start: 10, End: 20, WaitDays: 5, Amount: 1000}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	cont := mk(10)
	r, err := cont.SubmitClaim(ClaimInput{ID: "r", Person: "u", Day: 10,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 300}}})
	if err != nil || r.Payout != 300 {
		t.Fatalf("连续续保不应有等待期: %v %v", r, err)
	}
	late := mk(11)
	r2, err := late.SubmitClaim(ClaimInput{ID: "r", Person: "u", Day: 10,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 300}}})
	if err != nil || r2.Payout != 0 || r2.Verdicts[0].Reason != ReasonWaiting {
		t.Fatalf("晚登记应重新适用等待期: %v %v", r2, err)
	}

	// 新生效日早于原到期日 -> 与旧单重叠。
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 10, WaitDays: 0, Amount: 1000})
	err = e.RegisterPolicy(PolicyInput{Person: "u", RegisteredAt: 5, Start: 9, End: 20, WaitDays: 5, Amount: 1000})
	if !errors.Is(err, ErrIntervalOverlap) {
		t.Fatalf("重叠保单应拒绝, got %v", err)
	}
}

// 续保提高保额：等待期内非意外以原保额为上限；意外按全额；期满恢复全额。
func TestRenewalHigherAmountCap(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 10, WaitDays: 0, Amount: 1000})
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 10, Start: 10, End: 30, WaitDays: 10, Amount: 3000})

	r, err := e.SubmitClaim(ClaimInput{ID: "k1", Person: "u", Day: 11,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 2500}}})
	if err != nil || r.Payout != 1000 {
		t.Fatalf("提额等待期内非意外应以原保额为上限: %v %v", r, err)
	}
	r, err = e.SubmitClaim(ClaimInput{ID: "k2", Person: "u", Day: 11,
		Diagnoses: []Diagnosis{{Code: "ACC2", Fee: 2500}}})
	if err != nil || r.Payout != 2500 {
		t.Fatalf("意外应按全额赔付: %v %v", r, err)
	}
	r, err = e.SubmitClaim(ClaimInput{ID: "k3", Person: "u", Day: 21,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 2500}}})
	if err != nil || r.Payout != 2500 {
		t.Fatalf("等待期结束应按全额: %v %v", r, err)
	}

	e2 := NewEngine()
	testCodes(t, e2)
	mustPolicy(t, e2, PolicyInput{Person: "v", RegisteredAt: 0, Start: 0, End: 10, WaitDays: 0, Amount: 1000})
	mustPolicy(t, e2, PolicyInput{Person: "v", RegisteredAt: 10, Start: 10, End: 20, WaitDays: 9, Amount: 1000})
	r, err = e2.SubmitClaim(ClaimInput{ID: "z", Person: "v", Day: 10,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 500}}})
	if err != nil || r.Payout != 500 {
		t.Fatalf("保额未提高不应有等待期: %v %v", r, err)
	}
}

// 层级除外：下级入档不影响上级及其他下级；上级入档则下级除外；后新增下级也除外。
func TestHierarchyExclusion(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 0,
		Amount: 1_000_000, Declared: []string{"A"}})
	r, _ := e.SubmitClaim(ClaimInput{ID: "d1", Person: "u", Day: 10, Diagnoses: []Diagnosis{
		{Code: "A", Fee: 10}, {Code: "CARD", Fee: 20}, {Code: "ROOT", Fee: 30}, {Code: "OTHER", Fee: 40},
	}})
	if got := reasons(r); got[0] != ReasonExcluded ||
		got[1] != ReasonPaid || got[2] != ReasonPaid || got[3] != ReasonPaid {
		t.Fatalf("下级入档不应影响上级/其他下级: %v", got)
	}
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 100, Start: 100, End: 200, WaitDays: 0,
		Amount: 1_000_000, Declared: []string{"CARD"}})
	r, _ = e.SubmitClaim(ClaimInput{ID: "d2", Person: "u", Day: 110, Diagnoses: []Diagnosis{
		{Code: "CARD", Fee: 1}, {Code: "A", Fee: 1},
	}})
	if got := reasons(r); got[0] != ReasonExcluded || got[1] != ReasonExcluded {
		t.Fatalf("上级入档后下级也应除外: %v", got)
	}
	mustCode(t, e, CodeInput{Code: "NEWCHILD", Parent: "CARD"})
	r, _ = e.SubmitClaim(ClaimInput{ID: "d3", Person: "u", Day: 110,
		Diagnoses: []Diagnosis{{Code: "NEWCHILD", Fee: 1}}})
	if r.Verdicts[0].Reason != ReasonExcluded {
		t.Fatalf("新登记的下级也应被祖先除外: %v", r.Verdicts)
	}
}

// 意外在等待期内仍赔；既往症除外优先于意外。
func TestAccidentWaitingButExclusionFirst(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 30, Amount: 1000})
	r, err := e.SubmitClaim(ClaimInput{ID: "m1", Person: "u", Day: 1, Diagnoses: []Diagnosis{
		{Code: "ACC2", Fee: 700}, {Code: "A", Fee: 100},
	}})
	if err != nil || r.Payout != 700 ||
		r.Verdicts[0].Reason != ReasonPaid || r.Verdicts[1].Reason != ReasonWaiting {
		t.Fatalf("意外等待期内仍赔、普通病等待: %+v err=%v", r, err)
	}
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 100, Start: 100, End: 200, WaitDays: 30,
		Amount: 1000, Declared: []string{"ACC"}})
	r, err = e.SubmitClaim(ClaimInput{ID: "m2", Person: "u", Day: 101,
		Diagnoses: []Diagnosis{{Code: "ACC2", Fee: 1}}})
	if err != nil || r.Verdicts[0].Reason != ReasonExcluded || r.Payout != 0 {
		t.Fatalf("既往症除外应优先于意外: %v %v", r, err)
	}
}

// 合计赔付不超保额。
func TestPayoutCappedAtAmount(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 0, Amount: 500})
	r, err := e.SubmitClaim(ClaimInput{ID: "cap", Person: "u", Day: 10, Diagnoses: []Diagnosis{
		{Code: "A", Fee: 300}, {Code: "OTHER", Fee: 300},
	}})
	if err != nil || r.Payout != 500 ||
		r.Verdicts[0].Reason != ReasonPaid || r.Verdicts[1].Reason != ReasonPaid {
		t.Fatalf("合计赔付应以保额为上限: %+v %v", r, err)
	}
}

// 目录维护错误与环检测。
func TestCatalogMaintenance(t *testing.T) {
	c := NewCatalog()
	if err := c.AddCode(CodeInput{Code: ""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空编码应参数非法: %v", err)
	}
	if err := c.AddCode(CodeInput{Code: "X", Parent: "GHOST"}); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("上级不存在: %v", err)
	}
	if err := c.AddCode(CodeInput{Code: "X"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddCode(CodeInput{Code: "X"}); !errors.Is(err, ErrCodeDuplicate) {
		t.Fatalf("编码重复: %v", err)
	}
	if err := c.AddCode(CodeInput{Code: "Y", Parent: "X"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ChangeParent("GHOST", ""); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("变更目标不存在: %v", err)
	}
	if err := c.ChangeParent("X", "GHOST"); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("新上级不存在: %v", err)
	}
	if err := c.ChangeParent("X", "X"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("自环应非法: %v", err)
	}
	if err := c.ChangeParent("Y", "X"); err != nil {
		t.Fatal(err)
	}
	// X->Y, 把 X 的上级改为 Y 会使 X 成为自身祖先。
	if err := c.ChangeParent("X", "Y"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("成环应非法: %v", err)
	}
}

// 登记路径拒绝次序：参数非法 > 编码不存在 > 区间重叠。
func TestRejectionOrderingRegister(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 10, End: 20, WaitDays: 0, Amount: 100})
	cases := []struct {
		name string
		in   PolicyInput
		want error
	}{
		{"参数非法优先", PolicyInput{Person: "u", Start: 20, End: 10, Amount: 100, Declared: []string{"GHOST"}}, ErrInvalidArgument},
		{"编码不存在优先于重叠", PolicyInput{Person: "u", Start: 15, End: 25, Amount: 100, Declared: []string{"GHOST"}}, ErrCodeNotFound},
		{"区间重叠", PolicyInput{Person: "u", RegisteredAt: 0, Start: 15, End: 25, Amount: 100}, ErrIntervalOverlap},
	}
	for _, tc := range cases {
		err := e.RegisterPolicy(tc.in)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v got %v", tc.name, tc.want, err)
		}
	}
}

// 理赔路径拒绝次序逐对验证。
func TestRejectionOrderingClaims(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 0, Amount: 1000})
	if _, err := e.SubmitClaim(ClaimInput{ID: "dup", Person: "u", Day: 1,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}}); err != nil {
		t.Fatal(err)
	}
	ok := func(name string, in ClaimInput, want error) {
		_, err := e.SubmitClaim(in)
		if !errors.Is(err, want) {
			t.Fatalf("%s: want %v got %v", name, want, err)
		}
	}
	ok("参数非法>被保人不存在", ClaimInput{ID: "", Person: "nobody", Day: 1, Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}}, ErrInvalidArgument)
	ok("参数非法>编码不存在", ClaimInput{ID: "a", Person: "u", Day: 1, Diagnoses: []Diagnosis{{Code: "", Fee: 1}}}, ErrInvalidArgument)
	ok("参数非法>理赔已存在", ClaimInput{ID: "dup", Person: "u", Day: 1, Diagnoses: nil}, ErrInvalidArgument)
	ok("被保人不存在>编码不存在", ClaimInput{ID: "b", Person: "nobody", Day: 1, Diagnoses: []Diagnosis{{Code: "GHOST", Fee: 1}}}, ErrInsuredNotFound)
	ok("被保人不存在>理赔已存在", ClaimInput{ID: "dup", Person: "nobody", Day: 1, Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}}, ErrInsuredNotFound)
	ok("编码不存在>理赔已存在", ClaimInput{ID: "dup", Person: "u", Day: 1, Diagnoses: []Diagnosis{{Code: "GHOST", Fee: 1}}}, ErrCodeNotFound)
	ok("编码不存在>出险日未承保", ClaimInput{ID: "c", Person: "u", Day: 500, Diagnoses: []Diagnosis{{Code: "GHOST", Fee: 1}}}, ErrCodeNotFound)
	ok("理赔已存在>出险日未承保", ClaimInput{ID: "dup", Person: "u", Day: 500, Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}}, ErrClaimExists)
	if _, err := e.SubmitClaim(ClaimInput{ID: "d", Person: "u", Day: 500,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}}); !errors.Is(err, ErrUninsuredDate) {
		t.Fatalf("出险日未承保: %v", err)
	}
}

// 被拒绝的理赔不写档案、不占用理赔号。
func TestRejectedClaimLeavesNoTrace(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 30, Amount: 1000})
	bad := []ClaimInput{
		{ID: "", Person: "u", Day: 1, Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}},
		{ID: "n", Person: "u", Day: 1, Diagnoses: []Diagnosis{{Code: "A", Fee: 0}}},
		{ID: "e", Person: "u", Day: 1, Diagnoses: nil},
		{ID: "g", Person: "u", Day: 1, Diagnoses: []Diagnosis{{Code: "GHOST", Fee: 1}}},
		{ID: "x", Person: "u", Day: 200, Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}},
		{ID: "g2", Person: "ghost", Day: 1, Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}},
	}
	for i, in := range bad {
		if _, err := e.SubmitClaim(in); err == nil {
			t.Fatalf("用例 %d 应被拒绝", i)
		}
	}
	if got := e.ArchiveSnapshot("u"); len(got) != 0 {
		t.Fatalf("被拒绝理赔不得写档案: %v", got)
	}
	// 被拒绝的理赔号可再次使用。
	r, err := e.SubmitClaim(ClaimInput{ID: "x", Person: "u", Day: 1,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}})
	if err != nil || r.Verdicts[0].Reason != ReasonWaiting {
		t.Fatalf("拒绝不应占用理赔号: %v %v", r, err)
	}
}

// 档案按受理次序对后续理赔可见。
func TestArchiveVisibleToLaterClaims(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "u", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 30, Amount: 1000})
	r1, _ := e.SubmitClaim(ClaimInput{ID: "t1", Person: "u", Day: 1,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}})
	if r1.Verdicts[0].Reason != ReasonWaiting {
		t.Fatalf("首笔应等待: %v", r1.Verdicts)
	}
	// 等待期结束后，A 已入档 -> 除外（即使当时诊断的是下级 A，上级 CARD 不受影响）。
	r2, _ := e.SubmitClaim(ClaimInput{ID: "t2", Person: "u", Day: 40, Diagnoses: []Diagnosis{
		{Code: "A", Fee: 1}, {Code: "CARD", Fee: 1},
	}})
	if got := reasons(r2); got[0] != ReasonExcluded || got[1] != ReasonPaid {
		t.Fatalf("等待期入档对后续可见且不波及其上级: %v", got)
	}
}

// 不同被保人档案相互隔离。
func TestPersonsIsolated(t *testing.T) {
	e := NewEngine()
	testCodes(t, e)
	mustPolicy(t, e, PolicyInput{Person: "a", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 0,
		Amount: 100, Declared: []string{"A"}})
	mustPolicy(t, e, PolicyInput{Person: "b", RegisteredAt: 0, Start: 0, End: 100, WaitDays: 0, Amount: 100})
	ra, _ := e.SubmitClaim(ClaimInput{ID: "pa", Person: "a", Day: 1,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}})
	rb, _ := e.SubmitClaim(ClaimInput{ID: "pb", Person: "b", Day: 1,
		Diagnoses: []Diagnosis{{Code: "A", Fee: 1}}})
	if ra.Verdicts[0].Reason != ReasonExcluded || rb.Verdicts[0].Reason != ReasonPaid {
		t.Fatalf("被保人档案应隔离: %v %v", ra.Verdicts, rb.Verdicts)
	}
}
