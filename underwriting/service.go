package underwriting

import "sync"

// Service 是核保决策引擎的服务入口，所有方法可并发调用，
// 效果等价于某个串行顺序。同一投保单的操作由单投保单锁串行化。
type Service struct {
	rules      *RuleStore
	examLimits [7]int64 // 各职业类别（1..6）的免体检限额，以分计

	mu   sync.RWMutex
	apps map[string]*application
}

// application 是投保单的运行态；裁定以登记时的规则库快照为准。
type application struct {
	mu             sync.Mutex
	age            int
	occupation     int
	disclosures    map[string]bool
	sumAssured     int64
	appliedAt      int64
	premiumCap     int
	snapshot       *Snapshot
	decision       Decision
	terminal       bool
	examRegistered bool
	liftExamLimit  bool
}

// NewService 创建服务；examLimits[i] 为职业类别 i（1..6）的免体检限额。
func NewService(rules *RuleStore, examLimits [7]int64) *Service {
	return &Service{rules: rules, examLimits: examLimits, apps: make(map[string]*application)}
}

// Rules 暴露规则库，供管理员增删改。
func (s *Service) Rules() *RuleStore { return s.rules }

func (s *Service) examLimit(occupation int) int64 { return s.examLimits[occupation] }

// RegisterApplication 登记投保单并作出首次裁定。
func (s *Service) RegisterApplication(app Application) (Decision, error) {
	if app.ID == "" || app.Age < 0 || app.Occupation < 1 || app.Occupation > 6 ||
		app.SumAssured <= 0 || app.AppliedAt < 0 || app.PremiumCap < 0 {
		return Decision{}, ErrInvalidParam
	}
	a := &application{
		age:         app.Age,
		occupation:  app.Occupation,
		disclosures: make(map[string]bool, len(app.Disclosures)),
		sumAssured:  app.SumAssured,
		appliedAt:   app.AppliedAt,
		premiumCap:  app.PremiumCap,
		snapshot:    s.rules.Snapshot(),
	}
	for _, c := range app.Disclosures {
		a.disclosures[c] = true
	}
	a.decision, _ = decide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, s.examLimit(a.occupation), false)
	a.terminal = isTerminal(a.decision.Kind)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[app.ID]; ok {
		return Decision{}, ErrApplicationExists
	}
	s.apps[app.ID] = a
	return a.decision, nil
}

// Supplement 在裁定后补充健康告知项（只增不减），并按原申请时刻的
// 规则库快照以完整告知集合重新裁定。now 为当前时刻（秒）。
func (s *Service) Supplement(id string, codes []string, now int64) (Decision, error) {
	if len(codes) == 0 || now < 0 {
		return Decision{}, ErrInvalidParam
	}
	a, err := s.lookup(id)
	if err != nil {
		return Decision{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return Decision{}, ErrTerminal
	}
	if a.decision.Kind == DecPostpone && now < a.decision.PostponeUntil {
		return Decision{}, ErrPostponing
	}
	for _, c := range codes {
		a.disclosures[c] = true
	}
	a.decision, _ = decide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, s.examLimit(a.occupation), a.liftExamLimit)
	a.terminal = isTerminal(a.decision.Kind)
	return a.decision, nil
}

// RegisterExamResult 在「需体检」状态下登记体检结论。不通过转为
// 「规则拒保」终态；通过后以免体检限额视同无限制重新裁定一次。
func (s *Service) RegisterExamResult(id string, pass bool, now int64) (Decision, error) {
	if now < 0 {
		return Decision{}, ErrInvalidParam
	}
	a, err := s.lookup(id)
	if err != nil {
		return Decision{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
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
	a.decision, _ = decide(a.snapshot, a.age, a.occupation, a.disclosures,
		a.sumAssured, a.appliedAt, a.premiumCap, s.examLimit(a.occupation), true)
	a.terminal = isTerminal(a.decision.Kind)
	return a.decision, nil
}

// DecisionOf 查询投保单当前结论。
func (s *Service) DecisionOf(id string) (Decision, error) {
	a, err := s.lookup(id)
	if err != nil {
		return Decision{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.decision, nil
}

func (s *Service) lookup(id string) (*application, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.apps[id]
	if !ok {
		return nil, ErrApplicationNotFound
	}
	return a, nil
}

func isTerminal(k DecisionKind) bool {
	return k == DecRuleDecline || k == DecCapExceeded
}
