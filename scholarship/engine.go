package scholarship

import (
	"sync"
	"time"
)

// Engine 是奖学金评定引擎，所有方法均可并发调用。
type Engine struct {
	mu        sync.Mutex
	levels    []LevelConfig
	deptQuota map[string]map[string]int // level -> dept -> quota
	students  map[string]*Student
	result    *Result
	confirmed map[string]Award // studentID -> 已确认奖励（跨评定保留）
	tracer    Tracer
}

// NewEngine 构造引擎。levels 必须按全局次序从高到低给出，
// 高等级成绩下限不得低于低等级；deptQuotas 为 level->dept->名额。
func NewEngine(levels []LevelConfig, deptQuotas map[string]map[string]int, tracer Tracer) (*Engine, error) {
	if len(levels) == 0 {
		return nil, errf(ErrInvalidArgument, "levels must not be empty")
	}
	seen := map[string]bool{}
	prev := 1e308
	for _, lv := range levels {
		if lv.ID == "" || seen[lv.ID] {
			return nil, errf(ErrInvalidArgument, "empty or duplicate level id %q", lv.ID)
		}
		seen[lv.ID] = true
		if lv.MinAverage < 0 || lv.MinCredits < 0 || lv.PoolQuota < 0 {
			return nil, errf(ErrInvalidArgument, "level %q has negative threshold or quota", lv.ID)
		}
		if lv.MinAverage > prev {
			return nil, errf(ErrInvalidArgument, "level %q min average must be non-increasing in level order", lv.ID)
		}
		prev = lv.MinAverage
	}
	for lv, depts := range deptQuotas {
		if !seen[lv] {
			return nil, errf(ErrInvalidArgument, "quota references unknown level %q", lv)
		}
		for dept, q := range depts {
			if dept == "" {
				return nil, errf(ErrInvalidArgument, "empty department id in quota for %q", lv)
			}
			if q < 0 {
				return nil, errf(ErrInvalidArgument, "negative quota for %q/%q", lv, dept)
			}
		}
	}
	if tracer == nil {
		tracer = nopTracer{}
	}
	e := &Engine{
		levels:    append([]LevelConfig(nil), levels...),
		deptQuota: cloneQuotas(deptQuotas),
		students:  map[string]*Student{},
		confirmed: map[string]Award{},
		tracer:    tracer,
	}
	return e, nil
}

// AddStudent 加入或整体替换一名学生数据。
func (e *Engine) AddStudent(s Student) error {
	if s.ID == "" || s.Dept == "" || s.Average < 0 || s.Average > 100 ||
		s.Credits < 0 || s.Honor < 0 {
		return errf(ErrInvalidArgument, "invalid student record for %q", s.ID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s.Sanctions = append([]Sanction(nil), s.Sanctions...)
	e.students[s.ID] = &s
	e.tracer.Log("add-student", s.ID)
	return nil
}

// Evaluate 以 at 为评定时刻从零重评，返回不可变结果快照。
func (e *Engine) Evaluate(at time.Time) (*Result, error) {
	if at.IsZero() {
		return nil, errf(ErrInvalidArgument, "evaluation time must not be zero")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	res := e.reevaluate(at)
	return cloneResult(res), nil
}

// Confirm 确认当前评定结果中 studentID 的奖励。
func (e *Engine) Confirm(studentID string) (*Award, error) {
	if studentID == "" {
		return nil, errf(ErrInvalidArgument, "student id must not be empty")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.students[studentID]; !ok {
		return nil, errf(ErrNotFound, "student %q not found", studentID)
	}
	res, err := e.currentResultLocked() // 固定优先级：不存在 > 未评定 > 不在结果 > 已确认
	if err != nil {
		return nil, err
	}
	for i := range res.Awards {
		if res.Awards[i].StudentID == studentID {
			if res.Awards[i].Confirmed {
				return nil, errf(ErrAlreadyConfirmed, "student %q already confirmed", studentID)
			}
			cw := res.Awards[i]
			cw.Confirmed = true
			e.confirmed[studentID] = cw
			e.result.Awards[i].Confirmed = true
			e.tracer.Log("confirm", studentID)
			out := cw
			return &out, nil
		}
	}
	return nil, errf(ErrAwardNotInResult, "student %q has no award in current result", studentID)
}

// CorrectGrade 更正成绩数据。
func (e *Engine) CorrectGrade(studentID string, average, credits float64, honor int, hasFail bool) error {
	if studentID == "" {
		return errf(ErrInvalidArgument, "student id must not be empty")
	}
	if average < 0 || average > 100 || credits < 0 || honor < 0 {
		return errf(ErrInvalidArgument, "grade fields out of range for %q", studentID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.students[studentID]
	if !ok {
		return errf(ErrNotFound, "student %q not found", studentID)
	}
	s.Average, s.Credits, s.Honor, s.HasFail = average, credits, honor, hasFail
	e.tracer.Log("correct-grade", studentID)
	return nil
}

// RegisterSanction 登记一条生效中的处分。
func (e *Engine) RegisterSanction(studentID, sanctionID string) error {
	if studentID == "" || sanctionID == "" {
		return errf(ErrInvalidArgument, "student and sanction id must not be empty")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.students[studentID]
	if !ok {
		return errf(ErrNotFound, "student %q not found", studentID)
	}
	for i := range s.Sanctions {
		if s.Sanctions[i].ID == sanctionID {
			s.Sanctions[i].ReleasedAt = time.Time{} // 重复登记视为重新生效
			e.tracer.Log("register-sanction", studentID+"/"+sanctionID)
			return nil
		}
	}
	s.Sanctions = append(s.Sanctions, Sanction{ID: sanctionID})
	e.tracer.Log("register-sanction", studentID+"/"+sanctionID)
	return nil
}

// ReleaseSanction 解除处分；releaseAt 不晚于评定时刻即视为已解除。
func (e *Engine) ReleaseSanction(studentID, sanctionID string, releaseAt time.Time) error {
	if studentID == "" || sanctionID == "" {
		return errf(ErrInvalidArgument, "student and sanction id must not be empty")
	}
	if releaseAt.IsZero() {
		return errf(ErrInvalidArgument, "release time must not be zero")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.students[studentID]
	if !ok {
		return errf(ErrNotFound, "student %q not found", studentID)
	}
	for i := range s.Sanctions {
		if s.Sanctions[i].ID == sanctionID {
			s.Sanctions[i].ReleasedAt = releaseAt
			e.tracer.Log("release-sanction", studentID+"/"+sanctionID)
			return nil
		}
	}
	return errf(ErrNotFound, "sanction %q not found for student %q", sanctionID, studentID)
}

// SetTracer 替换日志记录器（nil 表示静默）。
func (e *Engine) SetTracer(t Tracer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t == nil {
		t = nopTracer{}
	}
	e.tracer = t
}

// Eligibility 以 O(学生自身数据) 时间判定某学生在某等级、某时刻的资格。
func (e *Engine) Eligibility(studentID, levelID string, at time.Time) (Eligibility, error) {
	if studentID == "" || levelID == "" || at.IsZero() {
		return Eligibility{}, errf(ErrInvalidArgument, "student, level and time are required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.students[studentID]
	if !ok {
		return Eligibility{}, errf(ErrNotFound, "student %q not found", studentID)
	}
	for i := range e.levels {
		if e.levels[i].ID == levelID {
			return judge(s, e.levels[i], at), nil
		}
	}
	return Eligibility{}, errf(ErrNotFound, "level %q not found", levelID)
}

func cloneQuotas(q map[string]map[string]int) map[string]map[string]int {
	out := make(map[string]map[string]int, len(q))
	for lv, depts := range q {
		out[lv] = make(map[string]int, len(depts))
		for d, n := range depts {
			out[lv][d] = n
		}
	}
	return out
}

func cloneResult(r *Result) *Result {
	out := &Result{EvaluatedAt: r.EvaluatedAt, Ranking: map[string][]RankedStudent{}}
	out.Levels = append([]string(nil), r.Levels...)
	out.Awards = append([]Award(nil), r.Awards...)
	for lv, rk := range r.Ranking {
		out.Ranking[lv] = append([]RankedStudent(nil), rk...)
	}
	return out
}
