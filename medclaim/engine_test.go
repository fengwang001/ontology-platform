package medclaim

import (
	"sync"
	"testing"
)

// 校验某操作必返回指定错误码。
func mustErr(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %d，实际成功", code)
	}
	if ErrorCode(err) != code {
		t.Fatalf("错误码=%d want %d (%v)", ErrorCode(err), code, err)
	}
}

func submitErr(e *Engine, c *Claim) error {
	_, err := e.Submit(c)
	return err
}

// 参数非法的各类形态。
func TestInvalidParameters(t *testing.T) {
	e := NewEngine()
	good := basePolicy()
	mustErr(t, e.RegisterPolicy(nil), ErrInvalidParameter)

	badYear := *good
	badYear.YearLength = 0
	mustErr(t, e.RegisterPolicy(&badYear), ErrInvalidParameter)

	badRatio := *good
	badRatio.PolicyID = "P2"
	badRatio.InpatientRatio = 101
	mustErr(t, e.RegisterPolicy(&badRatio), ErrInvalidParameter)

	badNeg := *good
	badNeg.PolicyID = "P3"
	badNeg.InceptionDay = -1
	mustErr(t, e.RegisterPolicy(&badNeg), ErrInvalidParameter)

	if err := e.RegisterPolicy(good); err != nil {
		t.Fatal(err)
	}
	// 明细为空
	mustErr(t, submitErr(e, &Claim{PolicyID: "P1", ClaimID: "z", EventDay: 10}), ErrInvalidParameter)
	// 金额非正
	mustErr(t, submitErr(e, claim("z", 10, inp("A", 0))), ErrInvalidParameter)
	// 类别未知
	mustErr(t, submitErr(e, &Claim{PolicyID: "P1", ClaimID: "z", EventDay: 10,
		Items: []Item{{Code: "A", Category: Category(9), Amount: 10}}}), ErrInvalidParameter)
	// 比例越界在登记时拦
	br := *good
	br.PolicyID = "P4"
	br.OutpatientRatio = -1
	mustErr(t, e.RegisterPolicy(&br), ErrInvalidParameter)
}

// 拒绝次序逐对验证：参数非法 > 保单不存在 > 理赔已存在 > 事故日未承保。
func TestRejectOrderSubmit(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(basePolicy()); err != nil {
		t.Fatal(err)
	}
	ok1, err := e.Submit(claim("dup", 10, inp("A", 1000)))
	if err != nil || ok1 == nil {
		t.Fatal("首笔提交失败", err)
	}

	// 参数非法 优先于 保单不存在
	mustErr(t, submitErr(e, &Claim{PolicyID: "NOPE", ClaimID: "x"}), ErrInvalidParameter)
	// 参数非法 优先于 理赔已存在
	mustErr(t, submitErr(e, &Claim{PolicyID: "P1", ClaimID: "dup"}), ErrInvalidParameter)
	// 保单不存在 优先于 理赔已存在（新号）
	mustErr(t, submitErr(e, &Claim{PolicyID: "NOPE", ClaimID: "new", EventDay: 10,
		Items: []Item{inp("A", 10)}}), ErrPolicyNotFound)
	// 理赔已存在 优先于 事故日未承保
	mustErr(t, submitErr(e, claim("dup", 5, inp("A", 10))), ErrClaimExists)
	// 事故日未承保
	mustErr(t, submitErr(e, claim("early", 9, inp("A", 10))), ErrDateNotCovered)
	// 被拒绝操作不留痕：累计仍只来自 dup
	d, o, _ := e.YearTotals("P1", 10)
	if d != 100 || o != 280 {
		t.Fatalf("拒绝后累计被污染: 免%d 付%d", d, o)
	}
}

// 撤销拒绝次序：保单不存在 > 理赔不存在 > 非末笔。
func TestRejectOrderCancel(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(basePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(claim("a", 10, inp("A", 1000))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(claim("b", 10, inp("A", 1000))); err != nil {
		t.Fatal(err)
	}
	mustErr(t, e.Cancel("NOPE", "a"), ErrPolicyNotFound)
	mustErr(t, e.Cancel("P1", "ghost"), ErrClaimNotFound)
	mustErr(t, e.Cancel("P1", "a"), ErrNotLast) // a 非该年度末笔
	// 非末笔撤销不留痕
	if _, ok := e.policies["P1"].claims["a"]; !ok {
		t.Fatal("非末笔撤销不应删除记录")
	}
}

// 撤销末笔后恢复累计、释放理赔号，可再次同号提交。
func TestCancelLastThenResubmitSameID(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(basePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(claim("a", 10, inp("A", 1000))); err != nil {
		t.Fatal(err)
	}
	r2, err := e.Submit(claim("b", 10, inp("B", 1000)))
	if err != nil {
		t.Fatal(err)
	}
	_ = r2
	if err := e.Cancel("P1", "b"); err != nil {
		t.Fatal(err)
	}
	d, o, _ := e.YearTotals("P1", 10)
	if d != 100 || o != 280 {
		t.Fatalf("撤销后累计: 免%d 付%d want 100,280", d, o)
	}
	// 理赔号 b 已释放
	if _, err := e.Submit(claim("b", 11, outp("C", 200))); err != nil {
		t.Fatalf("同号再提交失败: %v", err)
	}
	d, o, _ = e.YearTotals("P1", 10)
	// 第二笔：门诊200免赔0（年度免赔剩400 > 100？每次免赔仍100）
	// 年度免赔已扣100，剩余额度400，本次免赔 min(100,400,200)=100
	if d != 200 {
		t.Fatalf("免赔累计=%d want 200", d)
	}
	// 门诊200：免赔100后剩100，50%赔50付50；自付=100+50=150
	if o != 280+150 {
		t.Fatalf("自付累计=%d want 430", o)
	}
}

// 跨年度撤销互不影响：撤销年度1末笔不动年度0。
func TestCancelAcrossYears(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(basePolicy()); err != nil {
		t.Fatal(err)
	}
	e.Submit(claim("a", 10, inp("A", 1000)))  // 年度0
	e.Submit(claim("b", 400, inp("A", 1000))) // 年度1
	e.Submit(claim("c", 10, inp("A", 1000)))  // 年度0
	// 年度1末笔是 b，可撤；虽 c 是全局最后受理，但不影响。
	if err := e.Cancel("P1", "b"); err != nil {
		t.Fatalf("撤销年度1末笔: %v", err)
	}
	d0, o0, _ := e.YearTotals("P1", 10)
	if d0 != 200 || o0 != 560 {
		t.Fatalf("年度0 不应受影响: 免%d 付%d", d0, o0)
	}
	d1, o1, _ := e.YearTotals("P1", 400)
	if d1 != 0 || o1 != 0 {
		t.Fatalf("年度1 应清零: 免%d 付%d", d1, o1)
	}
	// 此时 c 是全局末笔且属年度0，撤销年度0的 a 应报非末笔
	mustErr(t, e.Cancel("P1", "a"), ErrNotLast)
}

// 并发提交与撤销：结果等价某串行顺序，累计始终与逐笔计算一致。
func TestConcurrentSubmitAndCancel(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(basePolicy()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				id := "g" + itoaTest(g) + "-" + itoaTest(k)
				r, err := e.Submit(claim(id, 10+(k%300), inp("A", 100+(k%5)*37), outp("B", 50+k)))
				if err != nil {
					t.Errorf("提交失败: %v", err)
					return
				}
				// 金额守恒
				if r.InsurerPaid+r.OOP != 100+(k%5)*37+50+k {
					t.Errorf("守恒破坏")
					return
				}
				if k%3 == 0 {
					_ = e.Cancel("P1", id) // 该笔受理时即其年度末笔（交错时可能已非末笔）
				}
			}
		}(g)
	}
	wg.Wait()

	// 用朴素重放校验最终状态：按 order 现存记录逐年重放。
	st := e.policies["P1"]
	accs := map[int]*yearAcc{}
	for _, id := range st.order {
		rec := st.claims[id]
		acc := accs[rec.yearIndex]
		if acc == nil {
			acc = &yearAcc{yearIndex: rec.yearIndex}
			accs[rec.yearIndex] = acc
		}
		naiveSettle(st.policy, rec.claim, acc)
	}
	for yi, acc := range accs {
		got := st.accs[yi]
		if got == nil || got.deductibleUsed != acc.deductibleUsed || got.oopTotal != acc.oopTotal {
			t.Fatalf("年度%d 并发后累计 免%d/付%d 与重放 免%d/付%d 不一致",
				yi, got.deductibleUsed, got.oopTotal, acc.deductibleUsed, acc.oopTotal)
		}
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
