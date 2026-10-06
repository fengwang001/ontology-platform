package underwriting

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是按需求规则独立写成的朴素参照实现：
// 线性扫描全部规则、直白的状态机，不依赖索引与内部实现。
type naiveModel struct {
	rules  map[string]Rule
	apps   map[string]*naiveApp
	limits [7]int64
}

type naiveApp struct {
	age            int
	occupation     int
	disclosures    map[string]bool
	sumAssured     int64
	appliedAt      int64
	premiumCap     int
	snapshot       map[string]Rule
	decision       Decision
	terminal       bool
	examRegistered bool
	liftExamLimit  bool
}

func newNaiveModel(limits [7]int64) *naiveModel {
	return &naiveModel{rules: map[string]Rule{}, apps: map[string]*naiveApp{}, limits: limits}
}

func cloneRules(src map[string]Rule) map[string]Rule {
	dst := make(map[string]Rule, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func naiveValidateRule(r Rule) error {
	if r.ID == "" || r.Start < 0 || r.Start >= r.End {
		return ErrInvalidParam
	}
	if r.Cond.MinAge != nil && *r.Cond.MinAge < 0 {
		return ErrInvalidParam
	}
	if r.Cond.MaxAge != nil && *r.Cond.MaxAge < 0 {
		return ErrInvalidParam
	}
	if r.Cond.MinAge != nil && r.Cond.MaxAge != nil && *r.Cond.MinAge > *r.Cond.MaxAge {
		return ErrInvalidParam
	}
	for occ := range r.Cond.Occupations {
		if occ < 1 || occ > 6 {
			return ErrInvalidParam
		}
	}
	switch r.Action.Kind {
	case ActionStandard, ActionDecline:
	case ActionExtraPremium:
		if r.Action.Percent <= 0 {
			return ErrInvalidParam
		}
	case ActionExclusion:
		if r.Action.ExclusionCode == "" {
			return ErrInvalidParam
		}
	case ActionPostpone:
		if r.Action.PostponeUntil < 0 {
			return ErrInvalidParam
		}
	default:
		return ErrInvalidParam
	}
	return nil
}

func (m *naiveModel) addRule(r Rule) error {
	if err := naiveValidateRule(r); err != nil {
		return err
	}
	if _, ok := m.rules[r.ID]; ok {
		return ErrRuleDuplicate
	}
	m.rules[r.ID] = r
	return nil
}

func (m *naiveModel) updateRule(r Rule) error {
	if err := naiveValidateRule(r); err != nil {
		return err
	}
	if _, ok := m.rules[r.ID]; !ok {
		return ErrRuleNotFound
	}
	m.rules[r.ID] = r
	return nil
}

func (m *naiveModel) deleteRule(id string) error {
	if _, ok := m.rules[id]; !ok {
		return ErrRuleNotFound
	}
	delete(m.rules, id)
	return nil
}

// naiveDecide 线性扫描并合并，返回结论与判定依据（命中规则与合并轨迹）。
func naiveDecide(rules map[string]Rule, age, occupation int, disclosures map[string]bool,
	sumAssured, appliedAt int64, premiumCap int, examLimit int64, lift bool) (Decision, string) {

	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var hits []string
	total := 0
	excl := map[string]bool{}
	postponed := false
	var until int64
	for _, id := range ids {
		r := rules[id]
		if r.Cond.MinAge != nil && age < *r.Cond.MinAge {
			continue
		}
		if r.Cond.MaxAge != nil && age > *r.Cond.MaxAge {
			continue
		}
		if len(r.Cond.Occupations) > 0 && !r.Cond.Occupations[occupation] {
			continue
		}
		if len(r.Cond.Disclosures) > 0 {
			any := false
			for c := range r.Cond.Disclosures {
				if disclosures[c] {
					any = true
					break
				}
			}
			if !any {
				continue
			}
		}
		if appliedAt < r.Start || appliedAt >= r.End {
			continue
		}
		hits = append(hits, id)
		switch r.Action.Kind {
		case ActionDecline:
			return Decision{Kind: DecRuleDecline}, fmt.Sprintf("hits=%v 命中拒保规则%s", hits, id)
		case ActionExtraPremium:
			total += r.Action.Percent
		case ActionExclusion:
			excl[r.Action.ExclusionCode] = true
		case ActionPostpone:
			if !postponed || r.Action.PostponeUntil > until {
				until = r.Action.PostponeUntil
			}
			postponed = true
		case ActionStandard:
		}
	}
	why := fmt.Sprintf("hits=%v 加费合计=%d 上限=%d", hits, total, premiumCap)
	if total > premiumCap {
		return Decision{Kind: DecCapExceeded}, why + " 合计超上限"
	}
	if postponed {
		return Decision{Kind: DecPostpone, PostponeUntil: until}, why + fmt.Sprintf(" 延期至%d", until)
	}
	if !lift && sumAssured > examLimit {
		return Decision{Kind: DecNeedExam}, why + fmt.Sprintf(" 保额%d超限额%d", sumAssured, examLimit)
	}
	codes := make([]string, 0, len(excl))
	for c := range excl {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		codes = nil
	}
	return Decision{Kind: DecAccept, ExtraPremium: total, Exclusions: codes}, why + fmt.Sprintf(" 承保 除外=%v", codes)
}

func (m *naiveModel) register(app Application) (Decision, error) {
	if app.ID == "" || app.Age < 0 || app.Occupation < 1 || app.Occupation > 6 ||
		app.SumAssured <= 0 || app.AppliedAt < 0 || app.PremiumCap < 0 {
		return Decision{}, ErrInvalidParam
	}
	if _, ok := m.apps[app.ID]; ok {
		return Decision{}, ErrApplicationExists
	}
	a := &naiveApp{
		age:         app.Age,
		occupation:  app.Occupation,
		disclosures: map[string]bool{},
		sumAssured:  app.SumAssured,
		appliedAt:   app.AppliedAt,
		premiumCap:  app.PremiumCap,
		snapshot:    cloneRules(m.rules),
	}
	for _, c := range app.Disclosures {
		a.disclosures[c] = true
	}
	d, _ := naiveDecide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, m.limits[a.occupation], false)
	a.decision = d
	a.terminal = d.Kind == DecRuleDecline || d.Kind == DecCapExceeded
	m.apps[app.ID] = a
	return d, nil
}

func (m *naiveModel) supplement(id string, codes []string, now int64) (Decision, error) {
	if len(codes) == 0 || now < 0 {
		return Decision{}, ErrInvalidParam
	}
	a, ok := m.apps[id]
	if !ok {
		return Decision{}, ErrApplicationNotFound
	}
	if a.terminal {
		return Decision{}, ErrTerminal
	}
	if a.decision.Kind == DecPostpone && now < a.decision.PostponeUntil {
		return Decision{}, ErrPostponing
	}
	for _, c := range codes {
		a.disclosures[c] = true
	}
	d, _ := naiveDecide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, m.limits[a.occupation], a.liftExamLimit)
	a.decision = d
	a.terminal = d.Kind == DecRuleDecline || d.Kind == DecCapExceeded
	return d, nil
}

func (m *naiveModel) exam(id string, pass bool, now int64) (Decision, error) {
	if now < 0 {
		return Decision{}, ErrInvalidParam
	}
	a, ok := m.apps[id]
	if !ok {
		return Decision{}, ErrApplicationNotFound
	}
	if a.terminal {
		return Decision{}, ErrTerminal
	}
	if a.decision.Kind == DecPostpone && now < a.decision.PostponeUntil {
		return Decision{}, ErrPostponing
	}
	if a.examRegistered {
		return Decision{}, ErrExamRegistered
	}
	if a.decision.Kind != DecNeedExam {
		return Decision{}, ErrInvalidState
	}
	a.examRegistered = true
	if !pass {
		a.decision = Decision{Kind: DecRuleDecline}
		a.terminal = true
		return a.decision, nil
	}
	a.liftExamLimit = true
	d, _ := naiveDecide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, m.limits[a.occupation], true)
	a.decision = d
	a.terminal = d.Kind == DecRuleDecline || d.Kind == DecCapExceeded
	return d, nil
}

func (m *naiveModel) decisionOf(id string) (Decision, error) {
	a, ok := m.apps[id]
	if !ok {
		return Decision{}, ErrApplicationNotFound
	}
	return a.decision, nil
}

// rationale 重建某投保单当前判定的依据，用于日志。
func (m *naiveModel) rationale(id string) string {
	a, ok := m.apps[id]
	if !ok {
		return "无此投保单"
	}
	_, why := naiveDecide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, m.limits[a.occupation], a.liftExamLimit)
	return why
}

// TestRandomizedAgainstNaiveModel 用随机投保单与随机规则库操作序列，
// 对照引擎与朴素模型的每一次输出，并打印输入、输出与判定依据。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	limits := testLimits()
	svc := NewService(NewRuleStore(), limits)
	model := newNaiveModel(limits)

	disclosurePool := []string{"H1", "H2", "H3", "H4"}
	exclusionPool := []string{"X1", "X2", "X3"}

	randRule := func(id string) Rule {
		r := Rule{ID: id}
		if rng.Intn(2) == 0 {
			lo := rng.Intn(40)
			r.Cond.MinAge = intPtr(lo)
			if rng.Intn(2) == 0 {
				r.Cond.MaxAge = intPtr(lo + rng.Intn(40))
			}
		} else if rng.Intn(2) == 0 {
			r.Cond.MaxAge = intPtr(rng.Intn(80))
		}
		if rng.Intn(2) == 0 {
			r.Cond.Occupations = map[int]bool{}
			for _, occ := range rng.Perm(6)[:rng.Intn(3)+1] {
				r.Cond.Occupations[occ+1] = true
			}
			if rng.Intn(20) == 0 { // 偶发非法职业类别
				r.Cond.Occupations[rng.Intn(2)*7] = true
			}
		}
		if rng.Intn(2) == 0 {
			r.Cond.Disclosures = map[string]bool{}
			for _, c := range disclosurePool[:rng.Intn(len(disclosurePool))+1] {
				r.Cond.Disclosures[c] = true
			}
		}
		switch rng.Intn(5) {
		case 0:
			r.Action = Action{Kind: ActionStandard}
		case 1:
			pct := rng.Intn(30) + 1
			if rng.Intn(20) == 0 { // 偶发非法百分比
				pct = -rng.Intn(3)
			}
			r.Action = Action{Kind: ActionExtraPremium, Percent: pct}
		case 2:
			r.Action = Action{Kind: ActionExclusion, ExclusionCode: exclusionPool[rng.Intn(len(exclusionPool))]}
		case 3:
			r.Action = Action{Kind: ActionPostpone, PostponeUntil: int64(rng.Intn(60))}
		default:
			r.Action = Action{Kind: ActionDecline}
		}
		r.Start = int64(rng.Intn(30))
		r.End = r.Start + int64(rng.Intn(30))
		if rng.Intn(20) != 0 {
			r.End++ // 大多数情况区间合法
		}
		return r
	}

	randApp := func(id string) Application {
		var discs []string
		for _, c := range disclosurePool {
			if rng.Intn(3) == 0 {
				discs = append(discs, c)
			}
		}
		occ := rng.Intn(6) + 1
		if rng.Intn(30) == 0 { // 偶发非法职业类别
			occ = 0
		}
		return Application{
			ID:          id,
			Age:         rng.Intn(80),
			Occupation:  occ,
			Disclosures: discs,
			SumAssured:  int64(rng.Intn(4) * 500_000),
			AppliedAt:   int64(rng.Intn(40)),
			PremiumCap:  rng.Intn(61),
		}
	}

	check := func(step int, op string, gotD Decision, gotErr error, wantD Decision, wantErr error, rationale string) {
		t.Helper()
		ok := reflect.DeepEqual(gotD, wantD) &&
			(gotErr == wantErr || (gotErr != nil && wantErr != nil && errorsIs(gotErr, wantErr)))
		t.Logf("step=%d op=%s 引擎=(%+v,%v) 模型=(%+v,%v) 依据=%s", step, op, gotD, gotErr, wantD, wantErr, rationale)
		if !ok {
			t.Fatalf("step=%d op=%s 不一致: 引擎=(%+v,%v) 模型=(%+v,%v)", step, op, gotD, gotErr, wantD, wantErr)
		}
	}

	const steps = 3000
	for i := 0; i < steps; i++ {
		ruleID := fmt.Sprintf("R%d", rng.Intn(12))
		appID := fmt.Sprintf("A%d", rng.Intn(8))
		switch rng.Intn(8) {
		case 0:
			r := randRule(ruleID)
			ge := svc.Rules().Add(r)
			me := model.addRule(r)
			check(i, fmt.Sprintf("AddRule(%+v)", r), Decision{}, ge, Decision{}, me, "规则库变更")
		case 1:
			r := randRule(ruleID)
			ge := svc.Rules().Update(r)
			me := model.updateRule(r)
			check(i, fmt.Sprintf("UpdateRule(%+v)", r), Decision{}, ge, Decision{}, me, "规则库变更")
		case 2:
			ge := svc.Rules().Delete(ruleID)
			me := model.deleteRule(ruleID)
			check(i, "DeleteRule("+ruleID+")", Decision{}, ge, Decision{}, me, "规则库变更")
		case 3, 4:
			app := randApp(appID)
			gd, ge := svc.RegisterApplication(app)
			md, me := model.register(app)
			check(i, fmt.Sprintf("Register(%+v)", app), gd, ge, md, me, model.rationale(appID))
		case 5, 6:
			var codes []string
			if rng.Intn(10) != 0 { // 偶发空集合（参数非法）
				for _, c := range disclosurePool {
					if rng.Intn(2) == 0 {
						codes = append(codes, c)
					}
				}
				if len(codes) == 0 {
					codes = []string{disclosurePool[rng.Intn(len(disclosurePool))]}
				}
			}
			now := int64(rng.Intn(70))
			gd, ge := svc.Supplement(appID, codes, now)
			md, me := model.supplement(appID, codes, now)
			check(i, fmt.Sprintf("Supplement(%s,%v,%d)", appID, codes, now), gd, ge, md, me, model.rationale(appID))
		default:
			pass := rng.Intn(2) == 0
			now := int64(rng.Intn(70))
			gd, ge := svc.RegisterExamResult(appID, pass, now)
			md, me := model.exam(appID, pass, now)
			check(i, fmt.Sprintf("Exam(%s,pass=%v,%d)", appID, pass, now), gd, ge, md, me, model.rationale(appID))
		}
	}

	// 终态一致性：逐单比对最终结论。
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("A%d", i)
		gd, ge := svc.DecisionOf(id)
		md, me := model.decisionOf(id)
		if (ge == nil) != (me == nil) || !reflect.DeepEqual(gd, md) {
			t.Fatalf("最终结论不一致 %s: 引擎=(%+v,%v) 模型=(%+v,%v)", id, gd, ge, md, me)
		}
	}
	t.Logf("随机对照完成: %d 步, 终态结论=%s", steps, finalSummary(svc))
}

func errorsIs(got, want error) bool {
	return strings.Contains(got.Error(), want.Error())
}

func finalSummary(s *Service) string {
	var parts []string
	for i := 0; i < 8; i++ {
		d, err := s.DecisionOf(fmt.Sprintf("A%d", i))
		if err == nil {
			parts = append(parts, fmt.Sprintf("A%d=%v", i, d.Kind))
		}
	}
	return strings.Join(parts, ",")
}
