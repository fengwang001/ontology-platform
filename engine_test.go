package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustAddCode(t *testing.T, e *Engine, code, parent string, accidental bool) {
	t.Helper()
	if err := e.AddCode(code, parent, accidental); err != nil {
		t.Fatalf("AddCode(%s,%s) 意外错误: %v", code, parent, err)
	}
}

func mustRegister(t *testing.T, e *Engine, in RegisterPolicyInput) {
	t.Helper()
	if err := e.RegisterPolicy(in); err != nil {
		t.Fatalf("RegisterPolicy(%+v) 意外错误: %v", in, err)
	}
}

func diag(code string, charge int) Diagnosis { return Diagnosis{Code: code, Charge: charge} }

// 等待期最后一天出险不赔、次日零点出险赔付、生效日当天算第 1 天。
func TestWaitingPeriodBoundary(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 10, Expiry: 100,
		WaitDays: 3, Amount: 100000,
	})
	if r, err := e.SubmitClaim("c1", "u1", 10, []Diagnosis{diag("A", 100)}); err != nil {
		t.Fatal(err)
	} else if r.Paid != 0 || r.Diagnoses[0].Verdict != VerdictInWaiting {
		t.Fatalf("生效日当天应在等待期内: %+v", r)
	}
	// 等待期最后一天 = 生效日 + 等待天数 - 1 = 12。
	if r, err := e.SubmitClaim("c2", "u1", 12, []Diagnosis{diag("A", 100)}); err != nil {
		t.Fatal(err)
	} else if r.Paid != 0 {
		t.Fatalf("等待期最后一天不应赔付: %+v", r)
	}
	mustAddCode(t, e, "B", "", false)
	if r, err := e.SubmitClaim("c3", "u1", 13, []Diagnosis{diag("B", 100)}); err != nil {
		t.Fatal(err)
	} else if r.Paid != 100 || r.Diagnoses[0].Verdict != VerdictPaid {
		t.Fatalf("等待期次日应赔付: %+v", r)
	}
}

// 到期日当天不在覆盖区间 [eff, exp)。
func TestExpiryExcludedFromCoverage(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 10, Expiry: 20,
		WaitDays: 0, Amount: 100000,
	})
	if _, err := e.SubmitClaim("c1", "u1", 19, []Diagnosis{diag("A", 10)}); err != nil {
		t.Fatalf("到期前一天应承保: %v", err)
	}
	if _, err := e.SubmitClaim("c2", "u1", 20, []Diagnosis{diag("A", 10)}); !errors.Is(err, ErrNotInsured) {
		t.Fatalf("到期日当天应出险日未承保, got %v", err)
	}
}

// 恰在到期日登记且新生效日等于原到期日：连续续保，无等待期，档案延续。
func TestRenewalAtExpiryIsContinuous(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	mustAddCode(t, e, "B", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 10,
		WaitDays: 30, Amount: 1000, Disclosure: []string{"A"},
	})
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 10, Effective: 10, Expiry: 20,
		WaitDays: 30, Amount: 1000,
	})
	r, err := e.SubmitClaim("c1", "u1", 10, []Diagnosis{diag("B", 500)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Paid != 500 {
		t.Fatalf("连续续保应无等待期: %+v", r)
	}
	r2, err := e.SubmitClaim("c2", "u1", 11, []Diagnosis{diag("A", 100), diag("B", 100)})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Diagnoses[0].Verdict != VerdictExcluded || r2.Paid != 100 {
		t.Fatalf("档案应延续, A 除外: %+v", r2)
	}
}

// 晚一天登记（原到期日之后）：成为新投保，重新适用等待期。
func TestLateRegistrationIsNewBusiness(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 10,
		WaitDays: 0, Amount: 1000,
	})
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 11, Effective: 11, Expiry: 20,
		WaitDays: 5, Amount: 1000,
	})
	r, err := e.SubmitClaim("c1", "u1", 11, []Diagnosis{diag("A", 100)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Paid != 0 || r.Diagnoses[0].Verdict != VerdictInWaiting {
		t.Fatalf("新投保应重新适用等待期: %+v", r)
	}
}

// 续保提高保额后，新等待期内非意外赔付以原保额为上限，意外可用满额。
func TestRenewalIncreaseAmountCap(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "D", "", false)
	mustAddCode(t, e, "D2", "", false)
	mustAddCode(t, e, "I", "", true)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 10,
		WaitDays: 0, Amount: 1000,
	})
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 10, Effective: 10, Expiry: 30,
		WaitDays: 5, Amount: 3000,
	})
	r, err := e.SubmitClaim("c1", "u1", 10, []Diagnosis{diag("D", 4000)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Paid != 1000 {
		t.Fatalf("非意外部分应以原保额 1000 为上限, got %d", r.Paid)
	}
	r2, err := e.SubmitClaim("c2", "u1", 11, []Diagnosis{diag("I", 2500)})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Paid != 2500 {
		t.Fatalf("意外应按新保额内全额赔付, got %d", r2.Paid)
	}
	r3, err := e.SubmitClaim("c3", "u1", 15, []Diagnosis{diag("D2", 2500)})
	if err != nil {
		t.Fatal(err)
	}
	if r3.Paid != 2500 {
		t.Fatalf("等待期结束后应按新保额赔付, got %d", r3.Paid)
	}
}

// 下级编码记入档案不影响上级与其他下级。
func TestChildArchivedDoesNotAffectSibling(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "R", "", false)
	mustAddCode(t, e, "R.C1", "R", false)
	mustAddCode(t, e, "R.C2", "R", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
		WaitDays: 0, Amount: 100000, Disclosure: []string{"R.C1"},
	})
	r, err := e.SubmitClaim("c1", "u1", 5, []Diagnosis{
		diag("R.C1", 10), diag("R.C2", 20), diag("R", 30),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{VerdictExcluded, VerdictPaid, VerdictPaid}
	for i, w := range want {
		if r.Diagnoses[i].Verdict != w {
			t.Fatalf("诊断 %d 判定=%s 期望 %s", i, r.Diagnoses[i].Verdict, w)
		}
	}
	if r.Paid != 50 {
		t.Fatalf("应赔付兄弟与上级合计 50, got %d", r.Paid)
	}
}

// 上级被除外后，之后新登记的下级也被除外。
func TestParentExcludedCoversLaterChild(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "R", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
		WaitDays: 0, Amount: 100000, Disclosure: []string{"R"},
	})
	mustAddCode(t, e, "R.Late", "R", false)
	r, err := e.SubmitClaim("c1", "u1", 5, []Diagnosis{diag("R.Late", 10)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Diagnoses[0].Verdict != VerdictExcluded {
		t.Fatalf("新下级应因祖先 R 在档案中而被除外: %+v", r)
	}
}

// 意外类在等待期内仍赔付；但若已被既往症除外，则除外优先。
func TestAccidentWaivesWaitingButExclusionWins(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "I", "", true)
	mustAddCode(t, e, "I.X", "I", false)
	mustAddCode(t, e, "P", "", true)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
		WaitDays: 30, Amount: 100000, Disclosure: []string{"P"},
	})
	r, err := e.SubmitClaim("c1", "u1", 0, []Diagnosis{
		diag("I", 100), diag("I.X", 200), diag("P", 300),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{VerdictPaid, VerdictPaid, VerdictExcluded}
	for i, w := range want {
		if r.Diagnoses[i].Verdict != w {
			t.Fatalf("诊断 %d 判定=%s 期望 %s (%s)", i, r.Diagnoses[i].Verdict, w, r.Diagnoses[i].Reason)
		}
	}
	if r.Paid != 300 {
		t.Fatalf("意外两笔应赔付 300，P 除外, got %d", r.Paid)
	}
}

// 被拒绝的理赔不得把任何诊断记入档案。
func TestRejectedClaimDoesNotArchive(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
		WaitDays: 0, Amount: 100000,
	})
	before := e.ArchiveSnapshot("u1")
	_, err := e.SubmitClaim("c1", "u1", 0, []Diagnosis{diag("A", 10), diag("Z-missing", 20)})
	if !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("应报编码不存在, got %v", err)
	}
	if got := e.ArchiveSnapshot("u1"); len(got) != len(before) {
		t.Fatalf("被拒绝理赔不得改变档案: before=%v after=%v", before, got)
	}
	if _, err := e.SubmitClaim("c2", "u1", 200, []Diagnosis{diag("A", 10)}); !errors.Is(err, ErrNotInsured) {
		t.Fatalf("应报出险日未承保, got %v", err)
	}
	if got := e.ArchiveSnapshot("u1"); len(got) != len(before) {
		t.Fatalf("出险日未承保也不得改变档案: %v", got)
	}
}

// 目录维护与保单登记的参数类错误。
func TestCatalogAndPolicyErrors(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	if err := e.AddCode("A", "", false); !errors.Is(err, ErrCodeDuplicate) {
		t.Fatalf("编码重复, got %v", err)
	}
	if err := e.AddCode("B", "A-missing", false); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("上级不存在, got %v", err)
	}
	if err := e.AddCode("", "", false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空编码应参数非法, got %v", err)
	}
	if err := e.RegisterPolicy(RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 10, Expiry: 10,
		WaitDays: 0, Amount: 10,
	}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("到期日须严格大于生效日, got %v", err)
	}
	if err := e.RegisterPolicy(RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 10,
		WaitDays: 0, Amount: 0,
	}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("保额须为正, got %v", err)
	}
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 10,
		WaitDays: 0, Amount: 100,
	})
	if err := e.RegisterPolicy(RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 5, Expiry: 15,
		WaitDays: 0, Amount: 100,
	}); !errors.Is(err, ErrOverlap) {
		t.Fatalf("区间重叠, got %v", err)
	}
	if err := e.RegisterPolicy(RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 10,
		WaitDays: 0, Amount: 100, Disclosure: []string{"nope"},
	}); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("告知编码不存在应报编码不存在, got %v", err)
	}
}

// 理赔通用错误：无保单被保人、空诊断、非正费用、理赔号重复。
func TestClaimCommonErrors(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	if _, err := e.SubmitClaim("c1", "nobody", 0, []Diagnosis{diag("A", 10)}); !errors.Is(err, ErrInsuredNotFound) {
		t.Fatalf("被保人不存在, got %v", err)
	}
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
		WaitDays: 0, Amount: 100000,
	})
	if _, err := e.SubmitClaim("c1", "u1", 0, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("诊断为空应参数非法, got %v", err)
	}
	if _, err := e.SubmitClaim("c1", "u1", 0, []Diagnosis{diag("A", 0)}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("费用非正应参数非法, got %v", err)
	}
	if _, err := e.SubmitClaim("c1", "u1", 0, []Diagnosis{diag("ghost", 10)}); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("诊断编码不存在, got %v", err)
	}
	if _, err := e.SubmitClaim("c1", "u1", 0, []Diagnosis{diag("A", 10)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SubmitClaim("c1", "u1", 1, []Diagnosis{diag("A", 10)}); !errors.Is(err, ErrClaimExists) {
		t.Fatalf("理赔号重复, got %v", err)
	}
}

// 拒绝次序逐对验证：理赔入口固定为
// 参数非法 > 被保人不存在 > 编码不存在 > 理赔已存在 > 出险日未承保。
func TestClaimRejectOrderPairwise(t *testing.T) {
	pairs := []struct {
		name string
		call func(e *Engine) error
		want error
	}{
		{"invalid-vs-noinsured", func(e *Engine) error {
			_, err := e.SubmitClaim("c", "ghost", 0, nil)
			return err
		}, ErrInvalidParam},
		{"noinsured-vs-code", func(e *Engine) error {
			_, err := e.SubmitClaim("c", "ghost", 0, []Diagnosis{diag("ghostcode", 10)})
			return err
		}, ErrInsuredNotFound},
		{"code-vs-claimdup", func(e *Engine) error {
			_, err := e.SubmitClaim("dup", "u1", 0, []Diagnosis{diag("ghostcode", 10)})
			return err
		}, ErrCodeNotFound},
		{"claimdup-vs-uninsured", func(e *Engine) error {
			_, err := e.SubmitClaim("dup", "u1", 500, []Diagnosis{diag("A", 10)})
			return err
		}, ErrClaimExists},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			e := NewEngine()
			mustAddCode(t, e, "A", "", false)
			mustRegister(t, e, RegisterPolicyInput{
				Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
				WaitDays: 0, Amount: 100000,
			})
			if _, err := e.SubmitClaim("dup", "u1", 0, []Diagnosis{diag("A", 1)}); err != nil {
				t.Fatal(err)
			}
			if err := p.call(e); !errors.Is(err, p.want) {
				t.Fatalf("%s 应报 %v, got %v", p.name, p.want, err)
			}
		})
	}
}

// 同一被保人理赔按受理次序生效，前笔记档对后笔可见。
func TestClaimOrderArchiveVisibility(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	mustRegister(t, e, RegisterPolicyInput{
		Insured: "u1", RegisterAt: 0, Effective: 0, Expiry: 100,
		WaitDays: 2, Amount: 100000,
	})
	if r, err := e.SubmitClaim("c1", "u1", 0, []Diagnosis{diag("A", 10)}); err != nil || r.Paid != 0 {
		t.Fatalf("首笔等待期不赔: %v %+v", err, r)
	}
	if r, err := e.SubmitClaim("c2", "u1", 50, []Diagnosis{diag("A", 10)}); err != nil {
		t.Fatal(err)
	} else if r.Diagnoses[0].Verdict != VerdictExcluded || r.Paid != 0 {
		t.Fatalf("等待期后 A 已入档应被除外: %+v", r)
	}
}

// 不同被保人之间互不影响；并发结果等价于某串行顺序。
func TestIsolationAndConcurrency(t *testing.T) {
	e := NewEngine()
	mustAddCode(t, e, "A", "", false)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := "u" + string(rune('a'+i%5))
			_ = e.RegisterPolicy(RegisterPolicyInput{
				Insured: u, RegisterAt: i, Effective: i * 1000, Expiry: i*1000 + 500,
				WaitDays: 0, Amount: 100000,
			})
		}(i)
	}
	wg.Wait()
	// 五名被保人各自恰有一张保单。
	for _, u := range []string{"ua", "ub", "uc", "ud", "ue"} {
		if !e.policies.hasPolicy(u) {
			t.Fatalf("被保人 %s 应有保单", u)
		}
	}
}
