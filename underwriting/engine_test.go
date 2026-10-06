package underwriting

import (
	"fmt"
	"sync"
	"testing"
)

func intPtr(v int) *int { return &v }

func testConfig() Config {
	var limits [7]int64
	for i := 1; i <= 6; i++ {
		limits[i] = 1_000_000 // 各职业免体检限额（分）
	}
	return Config{LoadingCap: 100, ExamFreeLimit: limits}
}

func loadingRule(id string, pct int, cond Condition) Rule {
	return Rule{ID: id, Cond: cond, Act: Action{Kind: ActionLoading, Percent: pct}, Start: 0, End: 1 << 40}
}

func baseApp(id string) Application {
	return Application{ID: id, Age: 30, Occupation: 1, Amount: 500_000, ApplyTime: 100}
}

func mustDecision(t *testing.T, d Decision, err error) Decision {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到 %v", err)
	}
	return d
}

func reg(t *testing.T, e *Engine, a Application) Decision {
	t.Helper()
	d, err := e.Register(a)
	return mustDecision(t, d, err)
}

func sup(t *testing.T, e *Engine, id string, codes []string, now int64) Decision {
	t.Helper()
	d, err := e.Supplement(id, codes, now)
	return mustDecision(t, d, err)
}

func red(t *testing.T, e *Engine, id string, now int64) Decision {
	t.Helper()
	d, err := e.Redecide(id, now)
	return mustDecision(t, d, err)
}

func exam(t *testing.T, e *Engine, id string, pass bool, now int64) Decision {
	t.Helper()
	d, err := e.RegisterExam(id, pass, now)
	return mustDecision(t, d, err)
}

func expectKind(t *testing.T, err error, want ErrKind) {
	t.Helper()
	if KindOf(err) != want {
		t.Fatalf("期望错误 %v，得到 %v（err=%v）", want, KindOf(err), err)
	}
}

// 年龄区间两端取等。
func TestAgeRangeInclusive(t *testing.T) {
	e := NewEngine(testConfig())
	if err := e.AddRule(loadingRule("r1", 10, Condition{AgeLo: intPtr(30), AgeHi: intPtr(40)})); err != nil {
		t.Fatal(err)
	}
	for _, age := range []int{30, 40} {
		app := baseApp(fmt.Sprintf("in%d", age))
		app.Age = age
		d := reg(t, e, app)
		if d.Kind != DecAccepted || d.LoadingPercent != 10 {
			t.Fatalf("年龄 %d 应命中加费 10，得到 %v", age, d)
		}
	}
	for _, age := range []int{29, 41} {
		app := baseApp(fmt.Sprintf("out%d", age))
		app.Age = age
		d := reg(t, e, app)
		if d.Kind != DecAccepted || d.LoadingPercent != 0 {
			t.Fatalf("年龄 %d 不应命中，得到 %v", age, d)
		}
	}
}

// 生效区间左含右不含：申请时刻恰等于右端不命中，恰等于左端命中。
func TestEffectiveIntervalRightExclusive(t *testing.T) {
	e := NewEngine(testConfig())
	r := loadingRule("r1", 20, Condition{})
	r.Start, r.End = 100, 200
	if err := e.AddRule(r); err != nil {
		t.Fatal(err)
	}
	at := func(id string, ts int64) Decision {
		app := baseApp(id)
		app.ApplyTime = ts
		return reg(t, e, app)
	}
	if d := at("left", 100); d.LoadingPercent != 20 {
		t.Fatalf("左端应命中，得到 %v", d)
	}
	if d := at("mid", 199); d.LoadingPercent != 20 {
		t.Fatalf("区间内应命中，得到 %v", d)
	}
	if d := at("right", 200); d.LoadingPercent != 0 {
		t.Fatalf("右端不应命中，得到 %v", d)
	}
}

// 加费合计恰等于上限承保，大 1 则加费超限拒保。
func TestLoadingCapBoundary(t *testing.T) {
	// 上限 100：60 + 40 = 100，恰等于，承保且加费 100。
	e1 := NewEngine(testConfig())
	_ = e1.AddRule(loadingRule("r1", 60, Condition{}))
	_ = e1.AddRule(loadingRule("r2", 40, Condition{}))
	d := reg(t, e1, baseApp("eq"))
	if d.Kind != DecAccepted || d.LoadingPercent != 100 {
		t.Fatalf("合计恰等于上限应承保，得到 %v", d)
	}
	// 60 + 41 = 101 > 100，加费超限拒保。
	e2 := NewEngine(testConfig())
	_ = e2.AddRule(loadingRule("r1", 60, Condition{}))
	_ = e2.AddRule(loadingRule("r2", 41, Condition{}))
	d = reg(t, e2, baseApp("over"))
	if d.Kind != DecOverloadRejected {
		t.Fatalf("合计大 1 应加费超限拒保，得到 %v", d)
	}
}

// 保额恰等于免体检限额不需体检，大 1 则需体检。
func TestExamFreeLimitBoundary(t *testing.T) {
	e := NewEngine(testConfig())
	eq := baseApp("eq")
	eq.Amount = 1_000_000
	if d := reg(t, e, eq); d.Kind != DecAccepted {
		t.Fatalf("恰等于免体检限额应承保，得到 %v", d)
	}
	over := baseApp("over")
	over.Amount = 1_000_001
	if d := reg(t, e, over); d.Kind != DecNeedExam {
		t.Fatalf("超过免体检限额 1 分应需体检，得到 %v", d)
	}
}

// 规则拒保与加费超限拒保并存时，结论为规则拒保且两者可区分。
func TestRejectBeatsOverload(t *testing.T) {
	e := NewEngine(testConfig())
	_ = e.AddRule(loadingRule("r1", 150, Condition{})) // 单条即超上限
	_ = e.AddRule(Rule{ID: "r2", Cond: Condition{}, Act: Action{Kind: ActionReject}, Start: 0, End: 1 << 40})
	d := reg(t, e, baseApp("a"))
	if d.Kind != DecRuleRejected {
		t.Fatalf("拒保应压过加费超限，得到 %v", d)
	}
	e2 := NewEngine(testConfig())
	_ = e2.AddRule(loadingRule("r1", 150, Condition{}))
	d2 := reg(t, e2, baseApp("b"))
	if d2.Kind != DecOverloadRejected {
		t.Fatalf("仅超限时应为加费超限拒保，得到 %v", d2)
	}
	if d.Kind == d2.Kind {
		t.Fatal("规则拒保与加费超限拒保必须可区分")
	}
}

// 多条延期规则命中时取最晚时刻；延期时刻之前再裁定报延期中，恰等于可以。
func TestPostponeLatestAndBoundary(t *testing.T) {
	e := NewEngine(testConfig())
	postpone := func(id string, until int64) Rule {
		return Rule{ID: id, Cond: Condition{}, Act: Action{Kind: ActionPostpone, PostponeUntil: until}, Start: 0, End: 1 << 40}
	}
	_ = e.AddRule(postpone("p1", 500))
	_ = e.AddRule(postpone("p2", 900))
	_ = e.AddRule(postpone("p3", 700))
	d := reg(t, e, baseApp("a"))
	if d.Kind != DecPostponed || d.PostponeUntil != 900 {
		t.Fatalf("延期时刻应取最晚 900，得到 %v", d)
	}
	_, err := e.Redecide("a", 899)
	expectKind(t, err, ErrDeferred)
	// 恰等于延期时刻允许再裁定；快照未变，结论仍为延期（至同一时刻）。
	d = red(t, e, "a", 900)
	if d.Kind != DecPostponed || d.PostponeUntil != 900 {
		t.Fatalf("恰等于延期时刻应可再裁定，得到 %v", d)
	}
	// 到期后补充告知同样被允许。
	_ = sup(t, e, "a", []string{"HX"}, 900)
}

// 裁定后修改规则库不影响既有结论与后续再裁定（快照语义）。
func TestSnapshotFrozenAfterDecision(t *testing.T) {
	e := NewEngine(testConfig())
	_ = e.AddRule(loadingRule("r1", 10, Condition{}))
	d0 := reg(t, e, baseApp("a"))
	if d0.LoadingPercent != 10 {
		t.Fatalf("首次裁定加费应为 10，得到 %v", d0)
	}
	// 修改既有规则、新增更严规则、删除规则，均不影响该投保单。
	_ = e.UpdateRule(loadingRule("r1", 99, Condition{}))
	_ = e.AddRule(Rule{ID: "r2", Cond: Condition{}, Act: Action{Kind: ActionReject}, Start: 0, End: 1 << 40})
	_ = e.DeleteRule("r2")
	d1, err := e.DecisionOf("a")
	if err != nil {
		t.Fatal(err)
	}
	if !d0.equal(d1) {
		t.Fatalf("规则库变更后结论被污染: %v -> %v", d0, d1)
	}
	d2 := sup(t, e, "a", []string{"H1"}, 1000)
	if !d0.equal(d2) {
		t.Fatalf("补充告知后再裁定应仍按快照，得到 %v", d2)
	}
}

// 补充告知后结论变严（命中新除外与新拒保）与不变两种情形。
func TestSupplementStricterAndUnchanged(t *testing.T) {
	e := NewEngine(testConfig())
	_ = e.AddRule(Rule{ID: "ex", Cond: Condition{HealthCodes: map[string]bool{"H1": true}},
		Act: Action{Kind: ActionExclusion, ExclusionCode: "EX1"}, Start: 0, End: 1 << 40})
	_ = e.AddRule(Rule{ID: "rj", Cond: Condition{HealthCodes: map[string]bool{"H2": true}},
		Act: Action{Kind: ActionReject}, Start: 0, End: 1 << 40})
	d0 := reg(t, e, baseApp("a"))
	if d0.Kind != DecAccepted || len(d0.Exclusions) != 0 {
		t.Fatalf("初始应无除外承保，得到 %v", d0)
	}
	// 补充无关告知：结论不变。
	d1 := sup(t, e, "a", []string{"HX"}, 1000)
	if !d0.equal(d1) {
		t.Fatalf("补充无关告知结论不应变化，得到 %v", d1)
	}
	// 补充 H1：变严，附加除外 EX1。
	d2 := sup(t, e, "a", []string{"H1"}, 1001)
	if d2.Kind != DecAccepted || len(d2.Exclusions) != 1 || d2.Exclusions[0] != "EX1" {
		t.Fatalf("补充 H1 应附加 EX1，得到 %v", d2)
	}
	// 补充 H2：变严为规则拒保终态。
	d3 := sup(t, e, "a", []string{"H2"}, 1002)
	if d3.Kind != DecRuleRejected {
		t.Fatalf("补充 H2 应规则拒保，得到 %v", d3)
	}
	// 空集合补充为参数非法。
	_, err := e.Supplement("a", nil, 1003)
	expectKind(t, err, ErrInvalidParam)
}

// 加费与除外责任可同时存在于承保结论中，且除外取并集。
func TestLoadingAndExclusionCoexist(t *testing.T) {
	e := NewEngine(testConfig())
	_ = e.AddRule(loadingRule("r1", 15, Condition{}))
	_ = e.AddRule(Rule{ID: "e1", Cond: Condition{}, Act: Action{Kind: ActionExclusion, ExclusionCode: "EXB"}, Start: 0, End: 1 << 40})
	_ = e.AddRule(Rule{ID: "e2", Cond: Condition{}, Act: Action{Kind: ActionExclusion, ExclusionCode: "EXA"}, Start: 0, End: 1 << 40})
	_ = e.AddRule(Rule{ID: "e3", Cond: Condition{}, Act: Action{Kind: ActionExclusion, ExclusionCode: "EXB"}, Start: 0, End: 1 << 40})
	d := reg(t, e, baseApp("a"))
	if d.Kind != DecAccepted || d.LoadingPercent != 15 {
		t.Fatalf("应承保且加费 15，得到 %v", d)
	}
	if len(d.Exclusions) != 2 || d.Exclusions[0] != "EXA" || d.Exclusions[1] != "EXB" {
		t.Fatalf("除外应取并集并排序，得到 %v", d.Exclusions)
	}
}

// 需体检状态：不通过转规则拒保终态；通过后以免体检限额视同无限制重新裁定；
// 重复登记报已登记体检。
func TestExamFlow(t *testing.T) {
	// 不通过 -> 规则拒保终态。
	e1 := NewEngine(testConfig())
	app := baseApp("fail")
	app.Amount = 2_000_000
	if d := reg(t, e1, app); d.Kind != DecNeedExam {
		t.Fatalf("应需体检，得到 %v", d)
	}
	d := exam(t, e1, "fail", false, 1000)
	if d.Kind != DecRuleRejected {
		t.Fatalf("体检不通过应转规则拒保，得到 %v", d)
	}
	if _, err := e1.Supplement("fail", []string{"H1"}, 1001); KindOf(err) != ErrTerminal {
		t.Fatalf("终态后补充告知应报已终态，得到 %v", err)
	}
	// 通过 -> 以无限制限额重新裁定为承保；重复登记报已登记体检。
	e2 := NewEngine(testConfig())
	app2 := baseApp("pass")
	app2.Amount = 2_000_000
	_ = e2.AddRule(loadingRule("r1", 25, Condition{}))
	reg(t, e2, app2)
	d = exam(t, e2, "pass", true, 1000)
	if d.Kind != DecAccepted || d.LoadingPercent != 25 {
		t.Fatalf("体检通过后应承保并保留加费，得到 %v", d)
	}
	_, err := e2.RegisterExam("pass", true, 1001)
	expectKind(t, err, ErrExamRegistered)
	// 承保状态（未登记过体检）不可登记体检。
	e3 := NewEngine(testConfig())
	reg(t, e3, baseApp("ok"))
	_, err = e3.RegisterExam("ok", true, 1000)
	expectKind(t, err, ErrStateConflict)
}

// 拒绝次序逐对验证：参数非法 > 投保单不存在 > 已终态 > 延期中 > 已登记体检 > 其余。
func TestRejectionOrder(t *testing.T) {
	e := NewEngine(testConfig())
	// 终态投保单。
	_ = e.AddRule(Rule{ID: "rj", Cond: Condition{}, Act: Action{Kind: ActionReject}, Start: 0, End: 1 << 40})
	reg(t, e, baseApp("term"))
	// 延期投保单。
	e2 := NewEngine(testConfig())
	_ = e2.AddRule(Rule{ID: "pp", Cond: Condition{}, Act: Action{Kind: ActionPostpone, PostponeUntil: 500}, Start: 0, End: 1 << 40})
	reg(t, e2, baseApp("def"))
	// 已登记体检的投保单。
	e3 := NewEngine(testConfig())
	examApp := baseApp("exam")
	examApp.Amount = 2_000_000
	reg(t, e3, examApp)
	exam(t, e3, "exam", true, 100)

	// 参数非法 > 投保单不存在：空告知集合 + 不存在的投保单。
	_, err := e.Supplement("ghost", nil, 100)
	expectKind(t, err, ErrInvalidParam)
	// 参数非法 > 已终态：空告知集合 + 终态投保单。
	_, err = e.Supplement("term", nil, 100)
	expectKind(t, err, ErrInvalidParam)
	// 投保单不存在 > 已终态语义：合法参数 + 不存在的投保单。
	_, err = e.Supplement("ghost", []string{"H1"}, 100)
	expectKind(t, err, ErrAppNotFound)
	// 已终态 > 其余：终态投保单的补充告知与再裁定。
	_, err = e.Supplement("term", []string{"H1"}, 100)
	expectKind(t, err, ErrTerminal)
	_, err = e.Redecide("term", 100)
	expectKind(t, err, ErrTerminal)
	_, err = e.RegisterExam("term", true, 100)
	expectKind(t, err, ErrTerminal)
	// 延期中 > 其余：延期时刻之前的补充、再裁定与体检登记。
	_, err = e2.Supplement("def", []string{"H1"}, 499)
	expectKind(t, err, ErrDeferred)
	_, err = e2.Redecide("def", 499)
	expectKind(t, err, ErrDeferred)
	_, err = e2.RegisterExam("def", true, 499)
	expectKind(t, err, ErrDeferred)
	// 已登记体检 > 其余业务错误（状态冲突）：已登记且已承保，再登记报已登记体检。
	_, err = e3.RegisterExam("exam", true, 200)
	expectKind(t, err, ErrExamRegistered)
	// 规则库入口：参数非法 > 规则重复 / 规则不存在。
	bad := loadingRule("bad", 0, Condition{})
	expectKind(t, e.AddRule(bad), ErrInvalidParam)
	expectKind(t, e.UpdateRule(bad), ErrInvalidParam)
	_ = e.AddRule(loadingRule("dup", 5, Condition{}))
	expectKind(t, e.AddRule(loadingRule("dup", 6, Condition{})), ErrRuleDuplicate)
	expectKind(t, e.UpdateRule(loadingRule("ghost", 6, Condition{})), ErrRuleNotFound)
	expectKind(t, e.DeleteRule("ghost"), ErrRuleNotFound)
	// 参数非法：生效区间左端不小于右端、职业类别越界。
	badInterval := loadingRule("bi", 5, Condition{})
	badInterval.Start, badInterval.End = 200, 200
	expectKind(t, e.AddRule(badInterval), ErrInvalidParam)
	badOcc := loadingRule("bo", 5, Condition{Occupations: map[int]bool{7: true}})
	expectKind(t, e.AddRule(badOcc), ErrInvalidParam)
}

// 被拒绝的操作不得改变投保单状态、结论、告知集合与任何时刻。
func TestRejectedOpLeavesNoTrace(t *testing.T) {
	e := NewEngine(testConfig())
	_ = e.AddRule(Rule{ID: "pp", Cond: Condition{}, Act: Action{Kind: ActionPostpone, PostponeUntil: 500}, Start: 0, End: 1 << 40})
	d0 := reg(t, e, baseApp("a"))
	disc0, _ := e.DisclosuresOf("a")
	// 延期中补充告知被拒。
	if _, err := e.Supplement("a", []string{"H1"}, 499); KindOf(err) != ErrDeferred {
		t.Fatalf("应报延期中，得到 %v", err)
	}
	d1, _ := e.DecisionOf("a")
	disc1, _ := e.DisclosuresOf("a")
	if !d0.equal(d1) {
		t.Fatalf("被拒操作改变了结论: %v -> %v", d0, d1)
	}
	if len(disc1) != len(disc0) {
		t.Fatalf("被拒操作改变了告知集合: %v -> %v", disc0, disc1)
	}
	// 到期后再裁定被允许（快照未变，结论仍为延期），告知集合不含曾被拒的 H1。
	d2 := red(t, e, "a", 500)
	if d2.Kind != DecPostponed || d2.PostponeUntil != 500 {
		t.Fatalf("到期后应可再裁定，得到 %v", d2)
	}
	_ = sup(t, e, "a", []string{"H1"}, 501)
	disc2, _ := e.DisclosuresOf("a")
	if len(disc2) != 1 || disc2[0] != "H1" {
		t.Fatalf("告知集合应只含 H1，得到 %v", disc2)
	}
}

// 同一投保单的并发补充与再裁定：结果等价于某个串行顺序，
// 不得出现只写入一半的告知集合或结论（配合 -race 运行）。
func TestConcurrentSupplementAndRedecide(t *testing.T) {
	e := NewEngine(testConfig())
	_ = e.AddRule(Rule{ID: "ex1", Cond: Condition{HealthCodes: map[string]bool{"H1": true}},
		Act: Action{Kind: ActionExclusion, ExclusionCode: "EX1"}, Start: 0, End: 1 << 40})
	_ = e.AddRule(Rule{ID: "ex2", Cond: Condition{HealthCodes: map[string]bool{"H2": true}},
		Act: Action{Kind: ActionExclusion, ExclusionCode: "EX2"}, Start: 0, End: 1 << 40})
	reg(t, e, baseApp("a"))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			code := fmt.Sprintf("H%d", i%2+1)
			_, _ = e.Supplement("a", []string{code}, int64(1000+i))
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = e.Redecide("a", int64(1000+i))
		}(i)
	}
	wg.Wait()
	// 最终告知集合应含全部补充项，结论应与按完整集合重放一致。
	disc, _ := e.DisclosuresOf("a")
	if len(disc) != 2 {
		t.Fatalf("并发后告知集合不完整: %v", disc)
	}
	final, _ := e.DecisionOf("a")
	replay := red(t, e, "a", 2000)
	if !final.equal(replay) {
		t.Fatalf("并发结果与串行重放不一致: %v vs %v", final, replay)
	}
	if len(final.Exclusions) != 2 {
		t.Fatalf("并发后除外集合不完整: %v", final.Exclusions)
	}
}
