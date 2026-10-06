package underwriting

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// 朴素模型：按需求条文独立写成的参照实现。
// 与正式实现的差异：不做任何索引，快照保留全量规则，
// 裁定时线性扫描并逐项过滤，合并分多趟循环完成。

type naiveApp struct {
	app      Application
	snap     []Rule
	disc     map[string]bool
	dec      Decision
	examReg  bool
	examPass bool
}

type naiveEngine struct {
	cfg   Config
	rules map[string]Rule
	apps  map[string]*naiveApp
}

func newNaive(cfg Config) *naiveEngine {
	return &naiveEngine{cfg: cfg, rules: map[string]Rule{}, apps: map[string]*naiveApp{}}
}

func naiveValidateRule(r Rule) error {
	if r.ID == "" || r.Start < 0 || r.Start >= r.End {
		return &Error{Kind: ErrInvalidParam}
	}
	if r.Cond.AgeLo != nil && *r.Cond.AgeLo < 0 {
		return &Error{Kind: ErrInvalidParam}
	}
	if r.Cond.AgeHi != nil && *r.Cond.AgeHi < 0 {
		return &Error{Kind: ErrInvalidParam}
	}
	if r.Cond.AgeLo != nil && r.Cond.AgeHi != nil && *r.Cond.AgeLo > *r.Cond.AgeHi {
		return &Error{Kind: ErrInvalidParam}
	}
	for o := range r.Cond.Occupations {
		if o < 1 || o > 6 {
			return &Error{Kind: ErrInvalidParam}
		}
	}
	switch r.Act.Kind {
	case ActionStandard, ActionReject:
	case ActionLoading:
		if r.Act.Percent <= 0 {
			return &Error{Kind: ErrInvalidParam}
		}
	case ActionExclusion:
		if r.Act.ExclusionCode == "" {
			return &Error{Kind: ErrInvalidParam}
		}
	case ActionPostpone:
		if r.Act.PostponeUntil < 0 {
			return &Error{Kind: ErrInvalidParam}
		}
	default:
		return &Error{Kind: ErrInvalidParam}
	}
	return nil
}

func (n *naiveEngine) addRule(r Rule) error {
	if err := naiveValidateRule(r); err != nil {
		return err
	}
	if _, ok := n.rules[r.ID]; ok {
		return &Error{Kind: ErrRuleDuplicate}
	}
	n.rules[r.ID] = r.clone()
	return nil
}

func (n *naiveEngine) updateRule(r Rule) error {
	if err := naiveValidateRule(r); err != nil {
		return err
	}
	if _, ok := n.rules[r.ID]; !ok {
		return &Error{Kind: ErrRuleNotFound}
	}
	n.rules[r.ID] = r.clone()
	return nil
}

func (n *naiveEngine) deleteRule(id string) error {
	if id == "" {
		return &Error{Kind: ErrInvalidParam}
	}
	if _, ok := n.rules[id]; !ok {
		return &Error{Kind: ErrRuleNotFound}
	}
	delete(n.rules, id)
	return nil
}

// naiveDecide 线性扫描快照，分趟合并：先拒保、再加费、再延期、再体检、最后承保。
func (n *naiveEngine) naiveDecide(a *naiveApp) Decision {
	var hits []Rule
	for _, r := range a.snap {
		if !(r.Start <= a.app.ApplyTime && a.app.ApplyTime < r.End) {
			continue
		}
		if r.Cond.AgeLo != nil && a.app.Age < *r.Cond.AgeLo {
			continue
		}
		if r.Cond.AgeHi != nil && a.app.Age > *r.Cond.AgeHi {
			continue
		}
		if r.Cond.Occupations != nil && !r.Cond.Occupations[a.app.Occupation] {
			continue
		}
		if r.Cond.HealthCodes != nil {
			hit := false
			for c := range r.Cond.HealthCodes {
				if a.disc[c] {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		hits = append(hits, r)
	}
	for _, r := range hits {
		if r.Act.Kind == ActionReject {
			return Decision{Kind: DecRuleRejected}
		}
	}
	sum := 0
	for _, r := range hits {
		if r.Act.Kind == ActionLoading {
			sum += r.Act.Percent
		}
	}
	if sum > n.cfg.LoadingCap {
		return Decision{Kind: DecOverloadRejected}
	}
	hasPostpone := false
	var until int64
	for _, r := range hits {
		if r.Act.Kind == ActionPostpone && (!hasPostpone || r.Act.PostponeUntil > until) {
			hasPostpone = true
			until = r.Act.PostponeUntil
		}
	}
	if hasPostpone {
		return Decision{Kind: DecPostponed, PostponeUntil: until}
	}
	limit := n.cfg.ExamFreeLimit[a.app.Occupation]
	if a.examPass {
		limit = math.MaxInt64
	}
	if a.app.Amount > limit {
		return Decision{Kind: DecNeedExam}
	}
	set := map[string]bool{}
	for _, r := range hits {
		if r.Act.Kind == ActionExclusion {
			set[r.Act.ExclusionCode] = true
		}
	}
	codes := make([]string, 0, len(set))
	for c := range set {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return Decision{Kind: DecAccepted, LoadingPercent: sum, Exclusions: codes}
}

func naiveValidateApp(a Application) error {
	if a.ID == "" || a.Age < 0 || a.Occupation < 1 || a.Occupation > 6 || a.Amount <= 0 || a.ApplyTime < 0 {
		return &Error{Kind: ErrInvalidParam}
	}
	for _, c := range a.HealthCodes {
		if c == "" {
			return &Error{Kind: ErrInvalidParam}
		}
	}
	return nil
}

func (n *naiveEngine) register(app Application) (Decision, error) {
	if err := naiveValidateApp(app); err != nil {
		return Decision{}, err
	}
	if _, ok := n.apps[app.ID]; ok {
		return Decision{}, &Error{Kind: ErrAppDuplicate}
	}
	a := &naiveApp{app: app, disc: map[string]bool{}}
	for _, r := range n.rules {
		a.snap = append(a.snap, r.clone())
	}
	for _, c := range app.HealthCodes {
		a.disc[c] = true
	}
	n.apps[app.ID] = a
	a.dec = n.naiveDecide(a)
	return a.dec, nil
}

func (n *naiveEngine) supplement(appID string, codes []string, now int64) (Decision, error) {
	if appID == "" || len(codes) == 0 || now < 0 {
		return Decision{}, &Error{Kind: ErrInvalidParam}
	}
	for _, c := range codes {
		if c == "" {
			return Decision{}, &Error{Kind: ErrInvalidParam}
		}
	}
	a, ok := n.apps[appID]
	if !ok {
		return Decision{}, &Error{Kind: ErrAppNotFound}
	}
	if a.dec.terminal() {
		return Decision{}, &Error{Kind: ErrTerminal}
	}
	if a.dec.Kind == DecPostponed && now < a.dec.PostponeUntil {
		return Decision{}, &Error{Kind: ErrDeferred}
	}
	for _, c := range codes {
		a.disc[c] = true
	}
	a.dec = n.naiveDecide(a)
	return a.dec, nil
}

func (n *naiveEngine) redecide(appID string, now int64) (Decision, error) {
	if appID == "" || now < 0 {
		return Decision{}, &Error{Kind: ErrInvalidParam}
	}
	a, ok := n.apps[appID]
	if !ok {
		return Decision{}, &Error{Kind: ErrAppNotFound}
	}
	if a.dec.terminal() {
		return Decision{}, &Error{Kind: ErrTerminal}
	}
	if a.dec.Kind == DecPostponed && now < a.dec.PostponeUntil {
		return Decision{}, &Error{Kind: ErrDeferred}
	}
	a.dec = n.naiveDecide(a)
	return a.dec, nil
}

func (n *naiveEngine) registerExam(appID string, pass bool, now int64) (Decision, error) {
	if appID == "" || now < 0 {
		return Decision{}, &Error{Kind: ErrInvalidParam}
	}
	a, ok := n.apps[appID]
	if !ok {
		return Decision{}, &Error{Kind: ErrAppNotFound}
	}
	if a.dec.terminal() {
		return Decision{}, &Error{Kind: ErrTerminal}
	}
	if a.dec.Kind == DecPostponed && now < a.dec.PostponeUntil {
		return Decision{}, &Error{Kind: ErrDeferred}
	}
	if a.examReg {
		return Decision{}, &Error{Kind: ErrExamRegistered}
	}
	if a.dec.Kind != DecNeedExam {
		return Decision{}, &Error{Kind: ErrStateConflict}
	}
	a.examReg = true
	if !pass {
		a.dec = Decision{Kind: DecRuleRejected}
		return a.dec, nil
	}
	a.examPass = true
	a.dec = n.naiveDecide(a)
	return a.dec, nil
}

// 随机操作序列对照：同一操作序列同时驱动正式实现与朴素模型，
// 逐步比较结论与错误类别，日志打印输入、输出与判定依据。

var healthPool = []string{"H0", "H1", "H2", "H3", "H4", "H5"}

func randRule(r *rand.Rand) Rule {
	id := fmt.Sprintf("R%d", r.Intn(12))
	var cond Condition
	if r.Intn(2) == 0 {
		lo, hi := r.Intn(90), r.Intn(90)
		if r.Intn(20) != 0 && lo > hi {
			lo, hi = hi, lo
		}
		cond.AgeLo = &lo
		cond.AgeHi = &hi
	}
	if r.Intn(2) == 0 {
		cond.Occupations = map[int]bool{}
		for i := 0; i < 1+r.Intn(3); i++ {
			occ := 1 + r.Intn(6)
			if r.Intn(30) == 0 {
				occ = r.Intn(9) // 偶尔越界，触发参数非法
			}
			cond.Occupations[occ] = true
		}
	}
	if r.Intn(2) == 0 {
		cond.HealthCodes = map[string]bool{}
		for i := 0; i < 1+r.Intn(3); i++ {
			cond.HealthCodes[healthPool[r.Intn(len(healthPool))]] = true
		}
	}
	var act Action
	switch r.Intn(5) {
	case 0:
		act = Action{Kind: ActionStandard}
	case 1:
		pct := 1 + r.Intn(120)
		if r.Intn(30) == 0 {
			pct = 0 // 偶尔非正，触发参数非法
		}
		act = Action{Kind: ActionLoading, Percent: pct}
	case 2:
		act = Action{Kind: ActionExclusion, ExclusionCode: fmt.Sprintf("EX%d", r.Intn(4))}
	case 3:
		act = Action{Kind: ActionPostpone, PostponeUntil: int64(r.Intn(1600))}
	default:
		act = Action{Kind: ActionReject}
	}
	start := int64(r.Intn(1000))
	end := start + int64(1+r.Intn(800))
	if r.Intn(30) == 0 {
		end = start // 偶尔左端不小于右端，触发参数非法
	}
	return Rule{ID: id, Cond: cond, Act: act, Start: start, End: end}
}

func randApp(r *rand.Rand) Application {
	occ := 1 + r.Intn(6)
	if r.Intn(40) == 0 {
		occ = r.Intn(9) // 偶尔越界
	}
	app := Application{
		ID:         fmt.Sprintf("A%d", r.Intn(8)),
		Age:        r.Intn(90),
		Occupation: occ,
		Amount:     int64(1 + r.Intn(3_000_000)),
		ApplyTime:  int64(r.Intn(1000)),
	}
	for i := 0; i < r.Intn(3); i++ {
		app.HealthCodes = append(app.HealthCodes, healthPool[r.Intn(len(healthPool))])
	}
	return app
}

func modelConfig(r *rand.Rand) Config {
	var limits [7]int64
	for i := 1; i <= 6; i++ {
		limits[i] = int64(1+r.Intn(4)) * 500_000
	}
	return Config{LoadingCap: 40 + r.Intn(80), ExamFreeLimit: limits}
}

func checkStep(t *testing.T, step int, desc string, d1 Decision, e1 error, d2 Decision, e2 error) {
	t.Helper()
	k1, k2 := KindOf(e1), KindOf(e2)
	ok := k1 == k2 && (k1 != 0 || d1.equal(d2))
	t.Logf("step=%d op=%s | 引擎: 结论=%v 错误=%v | 模型: 结论=%v 错误=%v | 判定: %v",
		step, desc, d1, k1, d2, k2, ok)
	if !ok {
		t.Fatalf("第 %d 步 %s 不一致: 引擎(%v, %v) 模型(%v, %v)", step, desc, d1, k1, d2, k2)
	}
}

func TestRandomSequenceAgainstNaiveModel(t *testing.T) {
	const seeds = 30
	const opsPerSeed = 200
	for seed := int64(0); seed < seeds; seed++ {
		r := rand.New(rand.NewSource(seed))
		cfg := modelConfig(r)
		eng := NewEngine(cfg)
		nav := newNaive(cfg)
		t.Logf("seed=%d 加费上限=%d 免体检限额=%v", seed, cfg.LoadingCap, cfg.ExamFreeLimit)
		for step := 0; step < opsPerSeed; step++ {
			switch r.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19:
				rule := randRule(r)
				e1 := eng.AddRule(rule)
				e2 := nav.addRule(rule)
				checkStep(t, step, fmt.Sprintf("AddRule(%+v)", rule), Decision{}, e1, Decision{}, e2)
			case 20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
				rule := randRule(r)
				e1 := eng.UpdateRule(rule)
				e2 := nav.updateRule(rule)
				checkStep(t, step, fmt.Sprintf("UpdateRule(%+v)", rule), Decision{}, e1, Decision{}, e2)
			case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
				id := fmt.Sprintf("R%d", r.Intn(12))
				e1 := eng.DeleteRule(id)
				e2 := nav.deleteRule(id)
				checkStep(t, step, "DeleteRule("+id+")", Decision{}, e1, Decision{}, e2)
			case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55:
				app := randApp(r)
				d1, e1 := eng.Register(app)
				d2, e2 := nav.register(app)
				checkStep(t, step, fmt.Sprintf("Register(%+v)", app), d1, e1, d2, e2)
			case 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71:
				id := fmt.Sprintf("A%d", r.Intn(8))
				var codes []string
				if r.Intn(20) != 0 {
					for i := 0; i < 1+r.Intn(2); i++ {
						codes = append(codes, healthPool[r.Intn(len(healthPool))])
					}
				}
				now := int64(r.Intn(1600))
				d1, e1 := eng.Supplement(id, codes, now)
				d2, e2 := nav.supplement(id, codes, now)
				checkStep(t, step, fmt.Sprintf("Supplement(%s,%v,%d)", id, codes, now), d1, e1, d2, e2)
			case 72, 73, 74, 75, 76, 77, 78, 79, 80, 81:
				id := fmt.Sprintf("A%d", r.Intn(8))
				now := int64(r.Intn(1600))
				d1, e1 := eng.Redecide(id, now)
				d2, e2 := nav.redecide(id, now)
				checkStep(t, step, fmt.Sprintf("Redecide(%s,%d)", id, now), d1, e1, d2, e2)
			default:
				id := fmt.Sprintf("A%d", r.Intn(8))
				pass := r.Intn(2) == 0
				now := int64(r.Intn(1600))
				d1, e1 := eng.RegisterExam(id, pass, now)
				d2, e2 := nav.registerExam(id, pass, now)
				checkStep(t, step, fmt.Sprintf("RegisterExam(%s,%v,%d)", id, pass, now), d1, e1, d2, e2)
			}
		}
		// 序列结束后，逐投保单核对最终结论与告知集合。
		for id := 0; id < 8; id++ {
			appID := fmt.Sprintf("A%d", id)
			d1, e1 := eng.DecisionOf(appID)
			d2 := Decision{}
			var e2 error
			if a, ok := nav.apps[appID]; ok {
				d2 = a.dec
			} else {
				e2 = &Error{Kind: ErrAppNotFound}
			}
			checkStep(t, opsPerSeed+id, "FinalDecisionOf("+appID+")", d1, e1, d2, e2)
		}
	}
}
