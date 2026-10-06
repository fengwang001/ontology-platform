package underwriting

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func intPtr(v int) *int { return &v }

func testLimits() [7]int64 {
	var l [7]int64
	for i := 1; i <= 6; i++ {
		l[i] = 1_000_000 // 1 万元，以分计
	}
	return l
}

func newService(t *testing.T, rules ...Rule) *Service {
	t.Helper()
	store := NewRuleStore()
	for _, r := range rules {
		if err := store.Add(r); err != nil {
			t.Fatalf("Add(%s): %v", r.ID, err)
		}
	}
	return NewService(store, testLimits())
}

func regApp(t *testing.T, s *Service, app Application) Decision {
	t.Helper()
	d, err := s.RegisterApplication(app)
	if err != nil {
		t.Fatalf("RegisterApplication(%s): %v", app.ID, err)
	}
	return d
}

func baseApp(id string) Application {
	return Application{ID: id, Age: 35, Occupation: 2, SumAssured: 500_000, AppliedAt: 1_000, PremiumCap: 50}
}

func TestAgeRangeInclusive(t *testing.T) {
	rule := Rule{ID: "r1", Start: 0, End: 10_000,
		Cond:   Condition{MinAge: intPtr(30), MaxAge: intPtr(40)},
		Action: Action{Kind: ActionDecline}}
	s := newService(t, rule)
	for _, age := range []int{30, 40} {
		app := baseApp(fmt.Sprintf("hit%d", age))
		app.Age = age
		if d := regApp(t, s, app); d.Kind != DecRuleDecline {
			t.Fatalf("age=%d 边界应命中拒保, got %v", age, d.Kind)
		}
	}
	for _, age := range []int{29, 41} {
		app := baseApp(fmt.Sprintf("miss%d", age))
		app.Age = age
		if d := regApp(t, s, app); d.Kind != DecAccept {
			t.Fatalf("age=%d 不应命中, got %v", age, d.Kind)
		}
	}
}

func TestEffectiveIntervalRightExclusive(t *testing.T) {
	rule := Rule{ID: "r1", Start: 100, End: 200,
		Action: Action{Kind: ActionDecline}}
	s := newService(t, rule)
	for i, at := range []int64{99, 100, 199, 200, 201} {
		app := baseApp(string(rune('a' + i)))
		app.AppliedAt = at
		d := regApp(t, s, app)
		want := DecAccept
		if at >= 100 && at < 200 {
			want = DecRuleDecline
		}
		if d.Kind != want {
			t.Fatalf("AppliedAt=%d: got %v want %v", at, d.Kind, want)
		}
	}
}

func premiumRule(id string, pct int) Rule {
	return Rule{ID: id, Start: 0, End: 10_000, Action: Action{Kind: ActionExtraPremium, Percent: pct}}
}

func TestPremiumCapExactAndOverByOne(t *testing.T) {
	// 合计恰等于上限：承保且加费 50。
	s := newService(t, premiumRule("p1", 30), premiumRule("p2", 20))
	d := regApp(t, s, baseApp("eq"))
	if d.Kind != DecAccept || d.ExtraPremium != 50 {
		t.Fatalf("恰等于上限应承保加费50, got %+v", d)
	}
	// 合计大 1：加费超限拒保，且与规则拒保可区分。
	s2 := newService(t, premiumRule("p1", 30), premiumRule("p2", 21))
	d2 := regApp(t, s2, baseApp("over"))
	if d2.Kind != DecCapExceeded {
		t.Fatalf("大1应加费超限拒保, got %+v", d2)
	}
	if d2.Kind == DecRuleDecline {
		t.Fatal("加费超限拒保不得与规则拒保混淆")
	}
}

func TestSumEqualToExamLimit(t *testing.T) {
	s := newService(t)
	app := baseApp("eq")
	app.SumAssured = 1_000_000 // 恰等于免体检限额
	if d := regApp(t, s, app); d.Kind != DecAccept {
		t.Fatalf("恰等于限额不需体检, got %v", d.Kind)
	}
	app2 := baseApp("over")
	app2.SumAssured = 1_000_001
	if d := regApp(t, s, app2); d.Kind != DecNeedExam {
		t.Fatalf("超限额 1 分需体检, got %v", d.Kind)
	}
}

func TestDeclineBeatsCapExceeded(t *testing.T) {
	decline := Rule{ID: "d1", Start: 0, End: 10_000, Action: Action{Kind: ActionDecline}}
	s := newService(t, decline, premiumRule("p1", 30), premiumRule("p2", 40))
	if d := regApp(t, s, baseApp("a")); d.Kind != DecRuleDecline {
		t.Fatalf("规则拒保应压过加费超限拒保, got %v", d.Kind)
	}
}

func TestPostponeTakesLatest(t *testing.T) {
	p1 := Rule{ID: "p1", Start: 0, End: 10_000, Action: Action{Kind: ActionPostpone, PostponeUntil: 5_000}}
	p2 := Rule{ID: "p2", Start: 0, End: 10_000, Action: Action{Kind: ActionPostpone, PostponeUntil: 8_000}}
	s := newService(t, p1, p2)
	d := regApp(t, s, baseApp("a"))
	if d.Kind != DecPostpone || d.PostponeUntil != 8_000 {
		t.Fatalf("延期时刻应取最晚 8000, got %+v", d)
	}
}

func TestPostponeSupplementTiming(t *testing.T) {
	p1 := Rule{ID: "p1", Start: 0, End: 10_000, Action: Action{Kind: ActionPostpone, PostponeUntil: 5_000}}
	s := newService(t, p1)
	regApp(t, s, baseApp("a"))
	if _, err := s.Supplement("a", []string{"H1"}, 4_999); !errors.Is(err, ErrPostponing) {
		t.Fatalf("延期时刻前应报延期中, got %v", err)
	}
	// 恰等于延期时刻可以再裁定。
	if _, err := s.Supplement("a", []string{"H1"}, 5_000); err != nil {
		t.Fatalf("恰等于延期时刻应可再裁定, got %v", err)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	excl := Rule{ID: "e1", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H1": true}},
		Action: Action{Kind: ActionExclusion, ExclusionCode: "X1"}}
	s := newService(t, excl)
	d := regApp(t, s, baseApp("a"))
	if d.Kind != DecAccept {
		t.Fatalf("got %v", d.Kind)
	}
	// 裁定后删除旧规则、新增拒保规则，不应影响该投保单的快照。
	if err := s.Rules().Delete("e1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rules().Add(Rule{ID: "d9", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H1": true}},
		Action: Action{Kind: ActionDecline}}); err != nil {
		t.Fatal(err)
	}
	d2, err := s.Supplement("a", []string{"H1"}, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	want := Decision{Kind: DecAccept, Exclusions: []string{"X1"}}
	if !reflect.DeepEqual(d2, want) {
		t.Fatalf("快照应不受规则库变更影响, got %+v want %+v", d2, want)
	}
}

func TestSupplementStricterAndUnchanged(t *testing.T) {
	decline := Rule{ID: "d1", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H9": true}},
		Action: Action{Kind: ActionDecline}}
	s := newService(t, decline)
	regApp(t, s, baseApp("strict"))
	regApp(t, s, baseApp("same"))
	// 补充后结论变严：命中拒保规则。
	d, err := s.Supplement("strict", []string{"H9"}, 2_000)
	if err != nil || d.Kind != DecRuleDecline {
		t.Fatalf("补充后应变严为规则拒保, got %+v err=%v", d, err)
	}
	// 补充后结论不变：新告知项不命中任何规则。
	d2, err := s.Supplement("same", []string{"H1"}, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Decision{Kind: DecAccept}); !reflect.DeepEqual(d2, want) {
		t.Fatalf("补充后结论应不变, got %+v want %+v", d2, want)
	}
}

func TestExamFlow(t *testing.T) {
	s := newService(t)
	big := baseApp("fail")
	big.SumAssured = 2_000_000 // 超免体检限额
	if d := regApp(t, s, big); d.Kind != DecNeedExam {
		t.Fatalf("got %v", d.Kind)
	}
	// 体检不通过 → 规则拒保终态。
	d, err := s.RegisterExamResult("fail", false, 2_000)
	if err != nil || d.Kind != DecRuleDecline {
		t.Fatalf("体检不通过应转规则拒保, got %+v err=%v", d, err)
	}
	if _, err := s.RegisterExamResult("fail", true, 3_000); !errors.Is(err, ErrTerminal) {
		t.Fatalf("终态后再登记应报已终态, got %v", err)
	}
	// 体检通过 → 免体检限额视同无限制，重新裁定为承保。
	big2 := baseApp("pass")
	big2.SumAssured = 2_000_000
	regApp(t, s, big2)
	d2, err := s.RegisterExamResult("pass", true, 2_000)
	if err != nil || d2.Kind != DecAccept {
		t.Fatalf("体检通过后应承保, got %+v err=%v", d2, err)
	}
	if _, err := s.RegisterExamResult("pass", true, 3_000); !errors.Is(err, ErrExamRegistered) {
		t.Fatalf("重复登记体检应报已登记体检, got %v", err)
	}
}

func TestRuleStoreErrors(t *testing.T) {
	s := newService(t, premiumRule("p1", 10))
	if err := s.Rules().Add(premiumRule("p1", 20)); !errors.Is(err, ErrRuleDuplicate) {
		t.Fatalf("重复编号应报规则重复, got %v", err)
	}
	if err := s.Rules().Update(premiumRule("ghost", 10)); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("修改不存在编号应报规则不存在, got %v", err)
	}
	if err := s.Rules().Delete("ghost"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("删除不存在编号应报规则不存在, got %v", err)
	}
	bad := []Rule{
		{ID: "b1", Start: 200, End: 200, Action: Action{Kind: ActionDecline}}, // 左端不小于右端
		{ID: "b2", Start: 300, End: 200, Action: Action{Kind: ActionDecline}},
		{ID: "b3", Start: 0, End: 10, Action: Action{Kind: ActionExtraPremium, Percent: 0}},  // 百分比非正
		{ID: "b4", Start: 0, End: 10, Action: Action{Kind: ActionExtraPremium, Percent: -5}}, // 百分比非正
		{ID: "b5", Start: 0, End: 10, Cond: Condition{Occupations: map[int]bool{7: true}},
			Action: Action{Kind: ActionDecline}}, // 职业类别越界
		{ID: "b6", Start: 0, End: 10, Cond: Condition{Occupations: map[int]bool{0: true}},
			Action: Action{Kind: ActionDecline}},
	}
	for _, r := range bad {
		if err := s.Rules().Add(r); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("规则 %s 应报参数非法, got %v", r.ID, err)
		}
	}
}

// 拒绝次序：参数非法 > 投保单不存在 > 已终态 > 延期中 > 已登记体检 > 其余业务错误。
func TestRejectionOrder(t *testing.T) {
	postpone := Rule{ID: "p1", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"HP": true}},
		Action: Action{Kind: ActionPostpone, PostponeUntil: 5_000}}
	decline := Rule{ID: "d1", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H9": true}},
		Action: Action{Kind: ActionDecline}}
	s := newService(t, postpone, decline)

	// 终态投保单（补充 H9 后拒保）。
	regApp(t, s, baseApp("term"))
	if _, err := s.Supplement("term", []string{"H9"}, 100); err != nil {
		t.Fatal(err)
	}
	// 延期投保单。
	postApp := baseApp("post")
	postApp.Disclosures = []string{"HP"}
	regApp(t, s, postApp)
	// 已登记体检投保单：需体检 → 通过 → 承保；再补充 HP 进入延期。
	needExam := baseApp("exam")
	needExam.SumAssured = 2_000_000
	regApp(t, s, needExam)
	if _, err := s.RegisterExamResult("exam", true, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Supplement("exam", []string{"HP"}, 200); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		call func() error
		want error
	}{
		// 参数非法 > 投保单不存在：空告知集合 + 不存在的投保单。
		{"非法优先于不存在", func() error { _, e := s.Supplement("ghost", nil, 100); return e }, ErrInvalidParam},
		// 参数非法 > 已终态。
		{"非法优先于终态", func() error { _, e := s.Supplement("term", nil, 100); return e }, ErrInvalidParam},
		// 参数非法 > 延期中。
		{"非法优先于延期中", func() error { _, e := s.Supplement("post", nil, 100); return e }, ErrInvalidParam},
		// 参数非法 > 已登记体检。
		{"非法优先于已登记体检", func() error { _, e := s.RegisterExamResult("exam", true, -1); return e }, ErrInvalidParam},
		// 投保单不存在 > 其余。
		{"不存在优先于其余", func() error { _, e := s.Supplement("ghost", []string{"H1"}, 100); return e }, ErrApplicationNotFound},
		// 已终态 > 延期中：终态投保单带任何时刻都报已终态。
		{"终态优先于延期中", func() error { _, e := s.Supplement("term", []string{"H1"}, 100); return e }, ErrTerminal},
		// 已终态 > 已登记体检：体检不通过转终态后再次登记。
		{"终态优先于已登记体检", func() error {
			big := baseApp("examfail")
			big.SumAssured = 2_000_000
			if _, e := s.RegisterApplication(big); e != nil {
				return e
			}
			if _, e := s.RegisterExamResult("examfail", false, 100); e != nil {
				return e
			}
			_, e := s.RegisterExamResult("examfail", true, 200)
			return e
		}, ErrTerminal},
		// 延期中 > 已登记体检：exam 单再裁定后为延期，延期时刻前再登记体检。
		{"延期中优先于已登记体检", func() error { _, e := s.RegisterExamResult("exam", true, 100); return e }, ErrPostponing},
		// 已登记体检 > 其余业务错误：延期结束后重复登记，状态已非需体检。
		{"已登记体检优先于状态错误", func() error { _, e := s.RegisterExamResult("exam", true, 6_000); return e }, ErrExamRegistered},
		// 其余业务错误：非需体检状态登记体检。
		{"状态错误兜底", func() error { _, e := s.RegisterExamResult("post", true, 6_000); return e }, ErrInvalidState},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestRejectedOpLeavesNoTrace(t *testing.T) {
	excl := Rule{ID: "e1", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H1": true}},
		Action: Action{Kind: ActionExclusion, ExclusionCode: "X1"}}
	s := newService(t, excl)
	regApp(t, s, baseApp("a"))
	before, err := s.DecisionOf("a")
	if err != nil {
		t.Fatal(err)
	}
	// 各类被拒操作。
	_, _ = s.Supplement("a", nil, 100)                // 参数非法
	_, _ = s.Supplement("ghost", []string{"H1"}, 100) // 投保单不存在
	_, _ = s.RegisterExamResult("a", true, 100)       // 状态错误
	_, _ = s.RegisterApplication(baseApp("a"))        // 重复登记
	after, err := s.DecisionOf("a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒操作不得改变结论: before=%+v after=%+v", before, after)
	}
	// 被拒的补充不得写入告知集合：随后合法补充 H1 应只按 {H1} 命中。
	d, err := s.Supplement("a", []string{"H1"}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Decision{Kind: DecAccept, Exclusions: []string{"X1"}}); !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v want %+v", d, want)
	}
}

func TestConcurrentSupplements(t *testing.T) {
	excl1 := Rule{ID: "e1", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H1": true}},
		Action: Action{Kind: ActionExclusion, ExclusionCode: "X1"}}
	excl2 := Rule{ID: "e2", Start: 0, End: 10_000,
		Cond:   Condition{Disclosures: map[string]bool{"H2": true}},
		Action: Action{Kind: ActionExclusion, ExclusionCode: "X2"}}
	s := newService(t, excl1, excl2)
	regApp(t, s, baseApp("a"))

	var wg sync.WaitGroup
	for _, codes := range [][]string{{"H1"}, {"H2"}} {
		wg.Add(1)
		go func(c []string) {
			defer wg.Done()
			if _, err := s.Supplement("a", c, 100); err != nil {
				t.Errorf("Supplement(%v): %v", c, err)
			}
		}(codes)
	}
	wg.Wait()
	// 无论哪种串行顺序，最终告知集合为 {H1,H2}，结论一致。
	d, err := s.DecisionOf("a")
	if err != nil {
		t.Fatal(err)
	}
	want := Decision{Kind: DecAccept, Exclusions: []string{"X1", "X2"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("并发再裁定应等价于串行, got %+v want %+v", d, want)
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() []Decision {
		s := newService(t,
			premiumRule("p1", 15),
			Rule{ID: "e1", Start: 0, End: 10_000,
				Cond:   Condition{Disclosures: map[string]bool{"H1": true}},
				Action: Action{Kind: ActionExclusion, ExclusionCode: "X1"}},
			Rule{ID: "e2", Start: 0, End: 10_000,
				Cond:   Condition{Disclosures: map[string]bool{"H2": true}},
				Action: Action{Kind: ActionExclusion, ExclusionCode: "X2"}},
		)
		var out []Decision
		d, _ := s.RegisterApplication(baseApp("a"))
		out = append(out, d)
		d, _ = s.Supplement("a", []string{"H2", "H1"}, 100)
		out = append(out, d)
		d, _ = s.Supplement("a", []string{"H1"}, 200)
		out = append(out, d)
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放应得到完全相同结论: %+v vs %+v", first, second)
	}
	want := Decision{Kind: DecAccept, ExtraPremium: 15, Exclusions: []string{"X1", "X2"}}
	if !reflect.DeepEqual(first[len(first)-1], want) {
		t.Fatalf("got %+v want %+v", first[len(first)-1], want)
	}
}
