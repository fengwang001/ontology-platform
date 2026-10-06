package medclaim

import "testing"

func basePolicy() *Policy {
	return &Policy{
		PolicyID:           "P1",
		InceptionDay:       10,
		YearLength:         365,
		PerClaimDeductible: 100,
		AnnualDeductCap:    500,
		InpatientRatio:     80,
		OutpatientRatio:    50,
		AnnualOOPCap:       1000,
		ExcludedCodes:      map[string]struct{}{"X1": {}},
	}
}

func claim(id string, day int, items ...Item) *Claim {
	return &Claim{PolicyID: "P1", ClaimID: id, EventDay: day, Items: items}
}

func inp(code string, amt int) Item {
	return Item{Code: code, Category: CategoryInpatient, Amount: amt}
}
func outp(code string, amt int) Item {
	return Item{Code: code, Category: CategoryOutpatient, Amount: amt}
}

func TestPolicyYearBoundaries(t *testing.T) {
	p := basePolicy()
	cases := []struct {
		day int
		y   int
	}{
		{9, -1},  // 早于承保起始日
		{10, 0},  // 左端包含
		{374, 0}, // 年度最后一天
		{375, 1}, // 右端取等：落入下一年度
		{739, 1}, // 第二年度最后一天
		{740, 2}, // 10+2*365
	}
	for _, c := range cases {
		if got := policyYear(p, c.day); got != c.y {
			t.Errorf("policyYear(%d)=%d want %d", c.day, got, c.y)
		}
	}
}

// 可赔基数恰等于每次事故免赔额。
func TestSettleBaseEqualsPerClaimDeductible(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{yearIndex: 0}
	r := settle(p, claim("c", 10, inp("A", 100)), acc)
	if r.InsurerPaid != 0 || r.OOP != 100 || r.DeductibleUsed != 100 {
		t.Fatalf("got 赔%d 付%d 免%d", r.InsurerPaid, r.OOP, r.DeductibleUsed)
	}
	if r.Items[0].OOP != 100 || r.Items[0].Deductible != 100 {
		t.Fatalf("明细级自付应含免赔: %+v", r.Items[0])
	}
	if acc.deductibleUsed != 100 || acc.oopTotal != 100 {
		t.Fatalf("acc 免%d 付%d", acc.deductibleUsed, acc.oopTotal)
	}
}

// 可赔基数小于每次事故免赔额：免赔不超过可赔基数本身。
func TestSettleBaseLessThanPerClaimDeductible(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{yearIndex: 0}
	r := settle(p, claim("c", 10, inp("A", 60), outp("B", 30)), acc)
	if r.DeductibleUsed != 90 || r.InsurerPaid != 0 || r.OOP != 90 {
		t.Fatalf("got 赔%d 付%d 免%d", r.InsurerPaid, r.OOP, r.DeductibleUsed)
	}
}

// 年度免赔累计恰到上限时，本次扣除额为 0。
func TestSettleAnnualDeductibleCapReached(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{yearIndex: 0, deductibleUsed: 500}
	r := settle(p, claim("c", 10, inp("A", 1000)), acc)
	if r.DeductibleUsed != 0 {
		t.Fatalf("本次免赔=%d want 0", r.DeductibleUsed)
	}
	if r.InsurerPaid != 800 || r.OOP != 200 {
		t.Fatalf("赔%d 付%d", r.InsurerPaid, r.OOP)
	}
}

// 年度免赔剩余额度小于每次免赔：按剩余额度扣。
func TestSettleAnnualDeductibleRemaining(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{yearIndex: 0, deductibleUsed: 450}
	r := settle(p, claim("c", 10, inp("A", 1000)), acc)
	if r.DeductibleUsed != 50 {
		t.Fatalf("本次免赔=%d want 50", r.DeductibleUsed)
	}
	// 剩余 950，80% -> 赔760，比例自付190；自付合计 50+190=240
	if r.InsurerPaid != 760 || r.OOP != 240 {
		t.Fatalf("赔%d 付%d", r.InsurerPaid, r.OOP)
	}
}

// 免赔逐条消耗跨越明细边界。
func TestDeductibleConsumesAcrossItems(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{}
	r := settle(p, claim("c", 10,
		inp("A", 40), inp("B", 30), outp("C", 200)), acc)
	want := []struct{ ded, ins, oop int }{
		{40, 0, 40},   // A 扣尽
		{30, 0, 30},   // B 扣尽
		{30, 85, 115}, // C：免赔30+比例自付85
	}
	for i, w := range want {
		ir := r.Items[i]
		if ir.Deductible != w.ded || ir.InsurerPaid != w.ins || ir.OOP != w.oop {
			t.Fatalf("明细%d 免%d 赔%d 付%d, want 免%d 赔%d 付%d",
				i, ir.Deductible, ir.InsurerPaid, ir.OOP, w.ded, w.ins, w.oop)
		}
	}
	if r.DeductibleUsed != 100 || r.InsurerPaid != 85 || r.OOP != 185 {
		t.Fatalf("合计 免%d 赔%d 付%d", r.DeductibleUsed, r.InsurerPaid, r.OOP)
	}
}

// 向上取整造成的尾差。
func TestCeilTailDifference(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{}
	// 住院 80%：免赔100后剩1，ceil(0.8)=1
	r := settle(p, claim("c", 10, inp("A", 101)), acc)
	if r.Items[0].InsurerPaid != 1 || r.Items[0].OOP != 100 {
		t.Fatalf("住院 赔%d 付%d want 1,100", r.Items[0].InsurerPaid, r.Items[0].OOP)
	}
	// 门诊 50%：免赔100后剩3，ceil(1.5)=2，比例自付1
	acc2 := &yearAcc{}
	r2 := settle(p, claim("c2", 10, outp("B", 103)), acc2)
	if r2.Items[0].InsurerPaid != 2 || r2.Items[0].OOP != 101 {
		t.Fatalf("门诊 赔%d 付%d want 2,101", r2.Items[0].InsurerPaid, r2.Items[0].OOP)
	}
}

// 自付累计恰等于封顶：本笔无追加赔付；后续理赔全额赔、不扣免赔。
func TestOOPCapExactlyThenFullPay(t *testing.T) {
	p := basePolicy()
	p.AnnualOOPCap = 280
	acc := &yearAcc{}
	r := settle(p, claim("c1", 10, inp("A", 1000)), acc)
	if acc.oopTotal != 280 {
		t.Fatalf("累计自付=%d want 280", acc.oopTotal)
	}
	if r.Items[0].CapTruncated {
		t.Fatal("恰等于封顶不应截断")
	}
	if r.Items[0].InsurerPaid != 720 || r.Items[0].OOP != 280 {
		t.Fatalf("A 赔%d 付%d want 720,280", r.Items[0].InsurerPaid, r.Items[0].OOP)
	}
	r2 := settle(p, claim("c2", 10, inp("B", 500), outp("C", 300)), acc)
	if r2.DeductibleUsed != 0 {
		t.Fatalf("封顶后免赔=%d want 0", r2.DeductibleUsed)
	}
	if r2.InsurerPaid != 800 || r2.OOP != 0 {
		t.Fatalf("赔%d 付%d want 800,0", r2.InsurerPaid, r2.OOP)
	}
	if acc.oopTotal != 280 {
		t.Fatalf("累计自付=%d want 280", acc.oopTotal)
	}
}

// 超过封顶：追加赔付归属最后一条被截断明细，其后明细全额赔付。
func TestCapOverflowAttribution(t *testing.T) {
	p := basePolicy()
	p.AnnualOOPCap = 150
	acc := &yearAcc{}
	r := settle(p, claim("c", 10,
		inp("A", 1000), inp("B", 1000), outp("C", 1000)), acc)
	if !r.Items[0].CapTruncated || r.Items[1].CapTruncated || r.Items[2].CapTruncated {
		t.Fatalf("截断标记只应在 A: %v %v %v",
			r.Items[0].CapTruncated, r.Items[1].CapTruncated, r.Items[2].CapTruncated)
	}
	if r.Items[0].OOP != 150 || r.Items[0].Deductible != 100 || r.Items[0].InsurerPaid != 850 {
		t.Fatalf("A 免%d 赔%d 付%d want 100,850,150",
			r.Items[0].Deductible, r.Items[0].InsurerPaid, r.Items[0].OOP)
	}
	if r.Items[1].InsurerPaid != 1000 || r.Items[1].OOP != 0 {
		t.Fatalf("B 应全额赔, got 赔%d 付%d", r.Items[1].InsurerPaid, r.Items[1].OOP)
	}
	if r.Items[2].InsurerPaid != 1000 || r.Items[2].OOP != 0 {
		t.Fatalf("C 应全额赔, got 赔%d 付%d", r.Items[2].InsurerPaid, r.Items[2].OOP)
	}
	if acc.oopTotal != 150 {
		t.Fatalf("累计=%d want 150", acc.oopTotal)
	}
	if r.InsurerPaid+r.OOP != 3000 {
		t.Fatalf("守恒: 赔%d+付%d != 3000", r.InsurerPaid, r.OOP)
	}
}

// 免赔阶段本身撞封顶：截断归属该明细，其后全额。
func TestCapHitDuringDeductible(t *testing.T) {
	p := basePolicy()
	p.AnnualOOPCap = 60
	acc := &yearAcc{oopTotal: 30}
	r := settle(p, claim("c", 10, inp("A", 1000), inp("B", 500)), acc)
	// A 免赔消耗100（不改小），被保人实担30，赔付970；B 其后全赔。
	if !r.Items[0].CapTruncated || r.Items[0].OOP != 30 || r.Items[0].Deductible != 100 {
		t.Fatalf("A 免%d 付%d trunc=%v", r.Items[0].Deductible, r.Items[0].OOP, r.Items[0].CapTruncated)
	}
	if r.Items[0].InsurerPaid != 970 || r.Items[1].InsurerPaid != 500 {
		t.Fatalf("A赔%d B赔%d", r.Items[0].InsurerPaid, r.Items[1].InsurerPaid)
	}
	if acc.deductibleUsed != 30 {
		t.Fatalf("免赔累计只记被保人实担=%d want 30", acc.deductibleUsed)
	}
	if acc.oopTotal != 60 {
		t.Fatalf("自付累计=%d want 60", acc.oopTotal)
	}
}

// 不赔项目整条剔除。
func TestExcludedItemIgnored(t *testing.T) {
	p := basePolicy()
	acc := &yearAcc{}
	r := settle(p, claim("c", 10, inp("X1", 9999), inp("A", 100)), acc)
	if !r.Items[0].Excluded {
		t.Fatal("X1 应标记不赔")
	}
	if r.Items[0].InsurerPaid != 0 || r.Items[0].OOP != 0 {
		t.Fatal("不赔明细各分摊应为 0")
	}
	if r.InsurerPaid != 0 || r.OOP != 100 || r.DeductibleUsed != 100 || r.Items[1].OOP != 100 {
		t.Fatalf("赔%d 付%d 免%d", r.InsurerPaid, r.OOP, r.DeductibleUsed)
	}
	if acc.deductibleUsed != 100 || acc.oopTotal != 100 {
		t.Fatalf("累计不应含不赔项目: 免%d 付%d", acc.deductibleUsed, acc.oopTotal)
	}
}

// 跨年度累计互不影响。
func TestYearAccumulatorsIndependent(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(basePolicy()); err != nil {
		t.Fatal(err)
	}
	r1, err := e.Submit(claim("c1", 10, inp("A", 1000)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(claim("c2", 400, inp("A", 1000))); err != nil {
		t.Fatal(err)
	}
	if r1.PolicyYear != 0 {
		t.Fatalf("年度=%d", r1.PolicyYear)
	}
	d0, o0, _ := e.YearTotals("P1", 10)
	d1, o1, _ := e.YearTotals("P1", 400)
	if d0 != 100 || d1 != 100 {
		t.Fatalf("年度免赔 %d/%d", d0, d1)
	}
	if o0 != 280 || o1 != 280 {
		t.Fatalf("年度自付 %d/%d", o0, o1)
	}
}
