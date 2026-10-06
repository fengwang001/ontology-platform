package permit

import (
	"sync"
	"sync/atomic"
)

// Logger 记录每步输入、输出与判定依据（测试用于可复现日志）。
type Logger interface {
	Logf(format string, args ...any)
}

// Service 行政许可并联审批办理服务。
//
// 所有变更操作在互斥锁内串行化，因此并发调用结果等价于某个串行顺序；
// 同一操作序列（含其到达顺序）重放得到完全相同的结果。
type Service struct {
	mu sync.Mutex

	cal          *Calendar
	types        map[string]*PermitType
	cases        map[string]*permitCase
	clock        int
	clockSet     bool
	seq          atomic.Int64
	suppWorkdays int
	suppLimit    int
	logger       Logger
}

// permitCase 一件许可的定义快照与不可变事件日志。
type permitCase struct {
	id       string
	typeID   string
	accepted int
	order    []string
	stages   map[string]*StageDef
	events   []*operation
}

// Option 配置服务。
type Option func(*Service)

// WithSupplement 配置补正期限工作日数与每环节补正次数上限。
func WithSupplement(workdays, perStageLimit int) Option {
	return func(s *Service) {
		s.suppWorkdays = workdays
		s.suppLimit = perStageLimit
	}
}

// WithLogger 注入步骤日志。
func WithLogger(l Logger) Option {
	return func(s *Service) {
		s.logger = l
	}
}

// NewService 创建服务，nonWorking 为非工作日日序号集合。
func NewService(nonWorking []int, opts ...Option) *Service {
	s := &Service{
		cal:          NewCalendar(nonWorking),
		types:        map[string]*PermitType{},
		cases:        map[string]*permitCase{},
		suppWorkdays: 5,
		suppLimit:    1,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Logf(format, args...)
	}
}

// checkClock 校验时钟单调。调用方须持锁。
func (s *Service) checkClock(day int) error {
	if !s.clockSet {
		s.clock = day
		s.clockSet = true
		return nil
	}
	if day < s.clock {
		return errf(ErrClockRollback, "day %d before clock %d", day, s.clock)
	}
	s.clock = day
	return nil
}

// RegisterType 注册许可类型（定义须无环、字段合法）。
func (s *Service) RegisterType(t *PermitType) error {
	if t == nil || t.ID == "" || len(t.Stages) == 0 {
		return errf(ErrInvalidParam, "permit type nil/empty id/no stages")
	}
	order, err := topoOrder(t)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.types[t.ID]; exists {
		return errf(ErrStateNotAllowed, "permit type %q already registered", t.ID)
	}
	s.types[t.ID] = &PermitType{ID: t.ID, Stages: append([]StageDef(nil), t.Stages...)}
	s.logf("RegisterType id=%s stages=%d topo=%v", t.ID, len(t.Stages), order)
	return nil
}

// Accept 受理一件许可。无前置的环节在受理当日启动。
func (s *Service) Accept(day int, caseID, typeID string) error {
	if caseID == "" || typeID == "" {
		return errf(ErrInvalidParam, "empty case/type id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(day); err != nil {
		return s.reject("Accept", day, caseID, "", err)
	}
	t, ok := s.types[typeID]
	if !ok {
		return s.reject("Accept", day, caseID, typeID, errf(ErrNotFound, "type %q", typeID))
	}
	if _, dup := s.cases[caseID]; dup {
		return s.reject("Accept", day, caseID, typeID, errf(ErrStateNotAllowed, "case exists"))
	}
	order, _ := topoOrder(t)
	stages := make(map[string]*StageDef, len(t.Stages))
	for i := range t.Stages {
		cp := t.Stages[i]
		stages[cp.ID] = &cp
	}
	c := &permitCase{
		id:       caseID,
		typeID:   typeID,
		accepted: day,
		order:    order,
		stages:   stages,
		events: []*operation{{
			seq: s.seq.Add(1), day: day, kind: opAccept, caseID: caseID,
		}},
	}
	s.cases[caseID] = c
	sn := s.derive(c, day)
	s.logf("Accept day=%d case=%s type=%s -> roots=%v outcome=%d", day, caseID, typeID, startedRoots(sn), sn.outcome)
	return nil
}

func startedRoots(sn *snapshot) []string {
	var roots []string
	for _, id := range sn.c.order {
		if sn.stages[id].started {
			roots = append(roots, id)
		}
	}
	return roots
}

// Decide 部门办理人对某环节作出通过/不通过/要求补正。
func (s *Service) Decide(day int, caseID, stageID, department string, d Decision) error {
	if caseID == "" || stageID == "" || department == "" ||
		(d != DecideApprove && d != DecideReject && d != DecideSupplement) {
		return errf(ErrInvalidParam, "decide bad arguments")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(day); err != nil {
		return s.reject("Decide", day, caseID, stageID, err)
	}
	c, ok := s.cases[caseID]
	if !ok {
		return s.reject("Decide", day, caseID, stageID, errf(ErrNotFound, "case %q", caseID))
	}
	st, ok := c.stages[stageID]
	if !ok {
		return s.reject("Decide", day, caseID, stageID, errf(ErrNotFound, "stage %q", stageID))
	}
	sn := s.derive(c, day)
	cur := sn.stages[stageID]
	// 已办结/已撤回属于“状态不允许”，优先于无权限。
	if cur.status == StageApproved || cur.status == StageRejected || cur.status == StageWithdrawn {
		return s.reject("Decide", day, caseID, stageID, errf(ErrStateNotAllowed, "stage status %d", cur.status))
	}
	if sn.hasFinal {
		return s.reject("Decide", day, caseID, stageID, errf(ErrStateNotAllowed, "case final outcome=%d", sn.outcome))
	}
	// 无权限先于“前置环节未通过”。
	if st.Department != department {
		return s.reject("Decide", day, caseID, stageID, errf(ErrNoPermission, "dept %q != %q", department, st.Department))
	}
	// 尚未启动 / 因他环节终止：前置环节未通过。
	if cur.status == StageNotStarted || cur.status == StageBlocked {
		return s.reject("Decide", day, caseID, stageID, errf(ErrPrereqNotPassed, "stage %q", stageID))
	}
	if d == DecideSupplement && cur.status == StageActive && len(cur.reqs) >= s.suppLimit {
		return s.reject("Decide", day, caseID, stageID, errf(ErrSupplementLimit, "used %d limit %d", len(cur.reqs), s.suppLimit))
	}
	c.events = append(c.events, &operation{
		seq: s.seq.Add(1), day: day, kind: opDecide, caseID: caseID,
		stageID: stageID, department: department, decision: d,
	})
	after := s.derive(c, day)
	s.logf("Decide day=%d case=%s stage=%s dept=%s d=%d -> status=%d remain=%d overtime=%v outcome=%d final=%v",
		day, caseID, stageID, department, d, after.stages[stageID].status, after.stages[stageID].remain,
		after.stages[stageID].overtime, after.outcome, after.hasFinal)
	return nil
}

// SubmitSupplement 申请人在补正期限内提交补正，恢复计时。
func (s *Service) SubmitSupplement(day int, caseID, stageID string) error {
	if caseID == "" || stageID == "" {
		return errf(ErrInvalidParam, "empty case/stage id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(day); err != nil {
		return s.reject("SubmitSupp", day, caseID, stageID, err)
	}
	c, ok := s.cases[caseID]
	if !ok {
		return s.reject("SubmitSupp", day, caseID, stageID, errf(ErrNotFound, "case %q", caseID))
	}
	if _, ok := c.stages[stageID]; !ok {
		return s.reject("SubmitSupp", day, caseID, stageID, errf(ErrNotFound, "stage %q", stageID))
	}
	sn := s.derive(c, day)
	cur := sn.stages[stageID]
	if cur.status != StagePaused {
		return s.reject("SubmitSupp", day, caseID, stageID, errf(ErrStateNotAllowed, "not paused: %d", cur.status))
	}
	if sn.hasFinal {
		return s.reject("SubmitSupp", day, caseID, stageID, errf(ErrStateNotAllowed, "case final"))
	}
	req := cur.reqs[len(cur.reqs)-1]
	if day > req.deadline {
		return s.reject("SubmitSupp", day, caseID, stageID, errf(ErrStateNotAllowed, "supp past deadline %d", req.deadline))
	}
	c.events = append(c.events, &operation{
		seq: s.seq.Add(1), day: day, kind: opSubmitSupp, caseID: caseID, stageID: stageID,
	})
	after := s.derive(c, day)
	s.logf("SubmitSupp day=%d case=%s stage=%s -> status=%d remain=%d due=%d",
		day, caseID, stageID, after.stages[stageID].status, after.stages[stageID].remain, after.stages[stageID].dueDay)
	return nil
}

// Withdraw 申请人在终局前撤回。
func (s *Service) Withdraw(day int, caseID string) error {
	if caseID == "" {
		return errf(ErrInvalidParam, "empty case id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(day); err != nil {
		return s.reject("Withdraw", day, caseID, "", err)
	}
	c, ok := s.cases[caseID]
	if !ok {
		return s.reject("Withdraw", day, caseID, "", errf(ErrNotFound, "case %q", caseID))
	}
	sn := s.derive(c, day)
	// 结论已固定（不予在首个不通过时即锁定，含并行收尾期间）则不允许撤回。
	if sn.outcome != OutcomePending {
		return s.reject("Withdraw", day, caseID, "", errf(ErrStateNotAllowed, "outcome=%d", sn.outcome))
	}
	c.events = append(c.events, &operation{
		seq: s.seq.Add(1), day: day, kind: opWithdraw, caseID: caseID,
	})
	s.logf("Withdraw day=%d case=%s -> withdrawn", day, caseID)
	return nil
}

// reject 记录被拒绝操作（不改变任何状态）并返回错误。
func (s *Service) reject(op string, day int, caseID, stageID string, err error) error {
	s.logf("REJECT op=%s day=%d case=%s stage=%s -> %v", op, day, caseID, stageID, err)
	return err
}

// StageAt 查询某环节在历史时刻 day 的状态、剩余时限与是否超时。
func (s *Service) StageAt(day int, caseID, stageID string) (StageView, error) {
	if caseID == "" || stageID == "" {
		return StageView{}, errf(ErrInvalidParam, "empty id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return StageView{}, errf(ErrNotFound, "case %q", caseID)
	}
	def, ok := c.stages[stageID]
	if !ok {
		return StageView{}, errf(ErrNotFound, "stage %q", stageID)
	}
	if day < c.accepted {
		return StageView{}, errf(ErrInvalidParam, "day %d before acceptance %d", day, c.accepted)
	}
	sn := s.derive(c, day)
	return toStageView(def, sn.stages[stageID]), nil
}

// Progress 查询整件在历史时刻 day 的进度。
func (s *Service) Progress(day int, caseID string) (CaseView, error) {
	if caseID == "" {
		return CaseView{}, errf(ErrInvalidParam, "empty id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return CaseView{}, errf(ErrNotFound, "case %q", caseID)
	}
	if day < c.accepted {
		return CaseView{}, errf(ErrInvalidParam, "day %d before acceptance %d", day, c.accepted)
	}
	sn := s.derive(c, day)
	view := CaseView{
		CaseID: caseID, TypeID: c.typeID, AtDay: day,
		Outcome: sn.outcome, FinalDay: sn.finalDay, HasFinal: sn.hasFinal,
	}
	for _, id := range c.order {
		view.Stages = append(view.Stages, toStageView(c.stages[id], sn.stages[id]))
	}
	return view, nil
}
