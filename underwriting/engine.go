package underwriting

import (
	"math"
	"sort"
	"sync"
)

// application 引擎内部维护的投保单状态。
type application struct {
	app            Application     // 登记时的投保单要素
	snapshot       []Rule          // 申请时刻对应的规则库快照（裁定后冻结）
	disclosures    map[string]bool // 完整健康告知集合（只增不减）
	decision       Decision        // 当前结论
	examRegistered bool            // 是否已登记体检结论
	examPassed     bool            // 体检是否已通过（通过后免体检限额视同无限制）
}

// Engine 核保决策引擎。所有入口可并发调用，内部以互斥锁串行化，
// 结果等价于某个串行顺序。
type Engine struct {
	mu    sync.Mutex
	cfg   Config
	rules *rulebook
	apps  map[string]*application
}

func NewEngine(cfg Config) *Engine {
	return &Engine{cfg: cfg, rules: newRulebook(), apps: map[string]*application{}}
}

// AddRule 登记规则；编号已存在报规则重复。
func (e *Engine) AddRule(r Rule) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rules.add(r)
}

// UpdateRule 按编号整体修改规则；编号不存在报规则不存在。
func (e *Engine) UpdateRule(r Rule) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rules.update(r)
}

// DeleteRule 按编号删除规则；编号不存在报规则不存在。
func (e *Engine) DeleteRule(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rules.remove(id)
}

func validateApplication(a Application) error {
	if a.ID == "" {
		return errf(ErrInvalidParam, "投保单编号为空")
	}
	if a.Age < 0 {
		return errf(ErrInvalidParam, "投保单 %s 年龄为负: %d", a.ID, a.Age)
	}
	if a.Occupation < 1 || a.Occupation > 6 {
		return errf(ErrInvalidParam, "投保单 %s 职业类别 %d 不在 1 到 6", a.ID, a.Occupation)
	}
	if a.Amount <= 0 {
		return errf(ErrInvalidParam, "投保单 %s 申请保额非正: %d", a.ID, a.Amount)
	}
	if a.ApplyTime < 0 {
		return errf(ErrInvalidParam, "投保单 %s 申请时刻为负: %d", a.ID, a.ApplyTime)
	}
	for _, c := range a.HealthCodes {
		if c == "" {
			return errf(ErrInvalidParam, "投保单 %s 含空告知编码", a.ID)
		}
	}
	return nil
}

// examLimit 该投保单当前适用的免体检限额；体检通过后视同无限制。
func (e *Engine) examLimit(a *application) int64 {
	if a.examPassed {
		return math.MaxInt64
	}
	return e.cfg.ExamFreeLimit[a.app.Occupation]
}

// redecide 以快照与完整告知集合重新裁定（调用方须已持锁并完成前置检查）。
func (e *Engine) redecide(a *application) Decision {
	a.decision = mergeDecision(a.snapshot, a.disclosures, a.app, e.cfg, e.examLimit(a))
	return a.decision
}

// Register 登记投保单并作出首次裁定；裁定以申请时刻对应的规则库快照为准。
func (e *Engine) Register(app Application) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := validateApplication(app); err != nil {
		return Decision{}, err
	}
	if _, ok := e.apps[app.ID]; ok {
		return Decision{}, errf(ErrAppDuplicate, "投保单 %s 已存在", app.ID)
	}
	a := &application{
		app:         app,
		snapshot:    e.rules.query(app.Age, app.Occupation, app.ApplyTime),
		disclosures: map[string]bool{},
	}
	for _, c := range app.HealthCodes {
		a.disclosures[c] = true
	}
	e.apps[app.ID] = a
	return e.redecide(a), nil
}

// Supplement 补充健康告知项（只增不减），随后按原申请时刻的快照重新裁定。
func (e *Engine) Supplement(appID string, codes []string, now int64) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if appID == "" || len(codes) == 0 || now < 0 {
		return Decision{}, errf(ErrInvalidParam, "补充告知参数非法: app=%q codes=%d now=%d", appID, len(codes), now)
	}
	for _, c := range codes {
		if c == "" {
			return Decision{}, errf(ErrInvalidParam, "补充告知含空编码")
		}
	}
	a, ok := e.apps[appID]
	if !ok {
		return Decision{}, errf(ErrAppNotFound, "投保单 %s 不存在", appID)
	}
	if a.decision.terminal() {
		return Decision{}, errf(ErrTerminal, "投保单 %s 已终态: %s", appID, a.decision.Kind)
	}
	if a.decision.Kind == DecPostponed && now < a.decision.PostponeUntil {
		return Decision{}, errf(ErrDeferred, "投保单 %s 延期至 %d，当前 %d", appID, a.decision.PostponeUntil, now)
	}
	for _, c := range codes {
		a.disclosures[c] = true
	}
	return e.redecide(a), nil
}

// Redecide 以当前完整告知集合、按原申请时刻的快照重新裁定。
func (e *Engine) Redecide(appID string, now int64) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if appID == "" || now < 0 {
		return Decision{}, errf(ErrInvalidParam, "再裁定参数非法: app=%q now=%d", appID, now)
	}
	a, ok := e.apps[appID]
	if !ok {
		return Decision{}, errf(ErrAppNotFound, "投保单 %s 不存在", appID)
	}
	if a.decision.terminal() {
		return Decision{}, errf(ErrTerminal, "投保单 %s 已终态: %s", appID, a.decision.Kind)
	}
	if a.decision.Kind == DecPostponed && now < a.decision.PostponeUntil {
		return Decision{}, errf(ErrDeferred, "投保单 %s 延期至 %d，当前 %d", appID, a.decision.PostponeUntil, now)
	}
	return e.redecide(a), nil
}

// RegisterExam 在需体检状态下登记体检结论：不通过转为规则拒保终态，
// 通过后以免体检限额视同无限制重新裁定一次。
func (e *Engine) RegisterExam(appID string, pass bool, now int64) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if appID == "" || now < 0 {
		return Decision{}, errf(ErrInvalidParam, "体检登记参数非法: app=%q now=%d", appID, now)
	}
	a, ok := e.apps[appID]
	if !ok {
		return Decision{}, errf(ErrAppNotFound, "投保单 %s 不存在", appID)
	}
	if a.decision.terminal() {
		return Decision{}, errf(ErrTerminal, "投保单 %s 已终态: %s", appID, a.decision.Kind)
	}
	if a.decision.Kind == DecPostponed && now < a.decision.PostponeUntil {
		return Decision{}, errf(ErrDeferred, "投保单 %s 延期至 %d，当前 %d", appID, a.decision.PostponeUntil, now)
	}
	if a.examRegistered {
		return Decision{}, errf(ErrExamRegistered, "投保单 %s 已登记体检", appID)
	}
	if a.decision.Kind != DecNeedExam {
		return Decision{}, errf(ErrStateConflict, "投保单 %s 当前为 %s，不可登记体检", appID, a.decision.Kind)
	}
	a.examRegistered = true
	if !pass {
		a.decision = Decision{Kind: DecRuleRejected}
		return a.decision, nil
	}
	a.examPassed = true
	return e.redecide(a), nil
}

// DecisionOf 查询投保单当前结论（不改变任何状态）。
func (e *Engine) DecisionOf(appID string) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if appID == "" {
		return Decision{}, errf(ErrInvalidParam, "投保单编号为空")
	}
	a, ok := e.apps[appID]
	if !ok {
		return Decision{}, errf(ErrAppNotFound, "投保单 %s 不存在", appID)
	}
	d := a.decision
	d.Exclusions = append([]string(nil), d.Exclusions...)
	return d, nil
}

// DisclosuresOf 查询投保单当前完整告知集合（排序，供核对与测试）。
func (e *Engine) DisclosuresOf(appID string) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if appID == "" {
		return nil, errf(ErrInvalidParam, "投保单编号为空")
	}
	a, ok := e.apps[appID]
	if !ok {
		return nil, errf(ErrAppNotFound, "投保单 %s 不存在", appID)
	}
	out := make([]string, 0, len(a.disclosures))
	for c := range a.disclosures {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}
