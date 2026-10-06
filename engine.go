package audit

import (
	"fmt"
	"sync"
)

// Engine 是学分替换与毕业审核引擎的注册表入口。
// 单一读写互斥锁保证所有操作线性一致：并发调用等价于某个串行顺序。
type Engine struct {
	mu sync.RWMutex

	courses map[string]*Course
	plans   map[string]map[int]*PlanVersion

	students map[string]*Student

	records     map[string]*Record
	studentRecs map[string][]string

	subs map[string]*Substitution
	// subIndex：方案ID -> 版本 -> 源课程 -> 替换（按 ID 排序）。
	// 审核只查该学生绑定版本与所修课程的索引桶，不扫描全部替换。
	subIndex map[string]map[int]map[string][]*Substitution

	transfers   map[string][]*TransferRecord
	transferSeq int

	// auditTouches 统计最近一次审核访问的记录条数；配合 Touches 方法
	// 可验证审核只触及该学生自己的数据（与其他学生数量无关）。
	auditTouches int
}

func NewEngine() *Engine {
	return &Engine{
		courses:     map[string]*Course{},
		plans:       map[string]map[int]*PlanVersion{},
		students:    map[string]*Student{},
		records:     map[string]*Record{},
		studentRecs: map[string][]string{},
		subs:        map[string]*Substitution{},
		subIndex:    map[string]map[int]map[string][]*Substitution{},
		transfers:   map[string][]*TransferRecord{},
	}
}

func errf(code ErrorCode, format string, args ...any) error {
	return &OpError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func (e *Engine) AddCourse(c Course) error {
	if c.ID == "" || c.Credits < 0 {
		return errf(ErrInvalidArgument, "invalid course %q", c.ID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.courses[c.ID]; dup {
		return errf(ErrInvalidArgument, "course %q already exists", c.ID)
	}
	cp := c
	e.courses[c.ID] = &cp
	return nil
}

func (e *Engine) AddPlan(p *PlanVersion) error {
	if err := validatePlan(p); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.plans[p.PlanID] == nil {
		e.plans[p.PlanID] = map[int]*PlanVersion{}
	}
	if _, dup := e.plans[p.PlanID][p.Version]; dup {
		return errf(ErrInvalidArgument, "plan %s@%d already exists", p.PlanID, p.Version)
	}
	e.plans[p.PlanID][p.Version] = clonePlan(p)
	return nil
}

func (e *Engine) Enroll(s Student) error {
	if s.ID == "" || s.PlanID == "" || s.PlanVersion <= 0 {
		return errf(ErrInvalidArgument, "invalid enrollment")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.students[s.ID]; dup {
		return errf(ErrInvalidArgument, "student %q already enrolled", s.ID)
	}
	if e.plans[s.PlanID] == nil || e.plans[s.PlanID][s.PlanVersion] == nil {
		return errf(ErrNotFound, "plan %s@%d not found", s.PlanID, s.PlanVersion)
	}
	cp := s
	e.students[s.ID] = &cp
	e.studentRecs[s.ID] = nil
	e.transfers[s.ID] = nil
	return nil
}

func (e *Engine) RegisterRecord(r Record) error {
	if r.ID == "" || r.Student == "" || r.Course == "" || r.Semester == "" || r.Credits < 0 {
		return errf(ErrInvalidArgument, "invalid record %q", r.ID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.students[r.Student]; !ok {
		return errf(ErrNotFound, "student %q not found", r.Student)
	}
	if _, ok := e.courses[r.Course]; !ok {
		return errf(ErrNotFound, "course %q not found", r.Course)
	}
	if _, dup := e.records[r.ID]; dup {
		return errf(ErrInvalidArgument, "record %q already registered", r.ID)
	}
	cp := r
	e.records[r.ID] = &cp
	e.studentRecs[r.Student] = append(e.studentRecs[r.Student], r.ID)
	return nil
}

func (e *Engine) RevokeRecord(student, recordID string) error {
	if student == "" || recordID == "" {
		return errf(ErrInvalidArgument, "invalid revoke request")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.students[student]; !ok {
		return errf(ErrNotFound, "student %q not found", student)
	}
	rec, ok := e.records[recordID]
	if !ok || rec.Student != student {
		return errf(ErrNotFound, "record %q not found", recordID)
	}
	if rec.Revoked {
		return errf(ErrRecordRevoked, "record %q already revoked", recordID)
	}
	rec.Revoked = true
	return nil
}

func (e *Engine) RegisterSubstitution(s Substitution) error {
	if s.ID == "" || s.From == "" || s.To == "" || s.PlanID == "" || s.PlanVersion <= 0 || s.Effective == "" || s.From == s.To {
		return errf(ErrInvalidArgument, "invalid substitution %q", s.ID)
	}
	if _, err := sscanfSemester(s.Effective, new(int), new(int)); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.courses[s.From]; !ok {
		return errf(ErrNotFound, "course %q not found", s.From)
	}
	if _, ok := e.courses[s.To]; !ok {
		return errf(ErrNotFound, "course %q not found", s.To)
	}
	pv := e.plans[s.PlanID]
	if pv == nil || pv[s.PlanVersion] == nil {
		return errf(ErrNotFound, "plan %s@%d not found", s.PlanID, s.PlanVersion)
	}
	if _, dup := e.subs[s.ID]; dup {
		return errf(ErrInvalidArgument, "substitution %q already registered", s.ID)
	}
	if !planContainsCourse(pv[s.PlanVersion], s.To) {
		return errf(ErrSubNotApplicable, "substitution %q not applicable to plan %s@%d", s.ID, s.PlanID, s.PlanVersion)
	}
	cp := s
	e.subs[s.ID] = &cp
	if e.subIndex[s.PlanID] == nil {
		e.subIndex[s.PlanID] = map[int]map[string][]*Substitution{}
	}
	if e.subIndex[s.PlanID][s.PlanVersion] == nil {
		e.subIndex[s.PlanID][s.PlanVersion] = map[string][]*Substitution{}
	}
	bucket := e.subIndex[s.PlanID][s.PlanVersion][s.From]
	bucket = append(bucket, &cp)
	sortSubSlice(bucket)
	e.subIndex[s.PlanID][s.PlanVersion][s.From] = bucket
	return nil
}

func (e *Engine) RegisterTransfer(tr TransferRecord) error {
	if tr.Student == "" || tr.Course == "" || tr.Credits < 0 || tr.Semester == "" {
		return errf(ErrInvalidArgument, "invalid transfer record")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.students[tr.Student]
	if !ok {
		return errf(ErrNotFound, "student %q not found", tr.Student)
	}
	if _, ok := e.courses[tr.Course]; !ok {
		return errf(ErrNotFound, "course %q not found", tr.Course)
	}
	plan := e.planFor(s)
	var sum float64
	for _, t := range e.transfers[tr.Student] {
		sum += t.Credits
	}
	if sum+tr.Credits > plan.TransferCap+eps {
		return errf(ErrTransferCap, "transfer credits exceed cap %.3f", plan.TransferCap)
	}
	e.transferSeq++
	cp := tr
	cp.Seq = e.transferSeq
	e.transfers[tr.Student] = append(e.transfers[tr.Student], &cp)
	return nil
}

func (e *Engine) SwitchVersion(student, planID string, version int) error {
	if student == "" || planID == "" || version <= 0 {
		return errf(ErrInvalidArgument, "invalid version switch")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.students[student]
	if !ok {
		return errf(ErrNotFound, "student %q not found", student)
	}
	if e.plans[planID] == nil || e.plans[planID][version] == nil {
		return errf(ErrNotFound, "plan %s@%d not found", planID, version)
	}
	if version < s.PlanVersion {
		return errf(ErrVersionTooOld, "version %d is older than current %d", version, s.PlanVersion)
	}
	s.PlanID = planID
	s.PlanVersion = version
	return nil
}

// Audit 对单个学生做毕业审核。审核仅快照并遍历该学生自己的记录，
// 时间复杂度只与该学生的记录/替换数量与方案树大小有关，
// 与其他学生总数及其记录总数无关（可通过 ComplexityProbe 验证）。
func (e *Engine) Audit(student string) (*AuditResult, error) {
	if student == "" {
		return nil, errf(ErrInvalidArgument, "empty student id")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.students[student]
	if !ok {
		return nil, errf(ErrNotFound, "student %q not found", student)
	}
	ctx := buildContext(e, s)
	e.auditTouches = ctx.touches
	return runAudit(ctx), nil
}

// LastAuditTouches 返回最近一次审核访问的记录条数。
// 仅包含被审核学生的本校记录、其转入记录与其版本相关替换桶。
func (e *Engine) LastAuditTouches() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.auditTouches
}

func (e *Engine) planFor(s *Student) *PlanVersion {
	return e.plans[s.PlanID][s.PlanVersion]
}

const eps = 1e-9

func ge0(a, b float64) bool { return a+eps >= b }

func sortSubSlice(subs []*Substitution) {
	for i := 1; i < len(subs); i++ {
		for j := i; j > 0 && subs[j-1].ID > subs[j].ID; j-- {
			subs[j-1], subs[j] = subs[j], subs[j-1]
		}
	}
}
