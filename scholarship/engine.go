package scholarship

import (
	"fmt"
	"sync"
	"time"
)

// Engine 奖学金评定引擎。所有公开方法可并发调用,
// 内部以单一互斥锁串行化, 效果等价于某个串行顺序。
// 引擎不保存任何历史评定快照, 重评开销与历史评定次数无关。
type Engine struct {
	mu        sync.Mutex
	cfg       Config
	levelIdx  map[string]int
	depts     map[string]bool
	students  map[string]Student
	confirmed map[string]Award
	current   *Result
	stats     Stats
}

// NewEngine 校验配置并创建引擎; 配置非法时返回 ErrInvalidParam。
func NewEngine(cfg Config) (*Engine, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	cpy := Config{PoolSize: cfg.PoolSize, Levels: make([]LevelConfig, 0, len(cfg.Levels))}
	levelIdx := map[string]int{}
	depts := map[string]bool{}
	for i, lv := range cfg.Levels {
		q := make(map[string]int, len(lv.Quotas))
		for d, n := range lv.Quotas {
			q[d] = n
			depts[d] = true
		}
		lv.Quotas = q
		cpy.Levels = append(cpy.Levels, lv)
		levelIdx[lv.ID] = i
	}
	return &Engine{
		cfg:       cpy,
		levelIdx:  levelIdx,
		depts:     depts,
		students:  map[string]Student{},
		confirmed: map[string]Award{},
	}, nil
}

// AddStudent 登记学生。被拒绝时不改动任何状态。
func (e *Engine) AddStudent(s Student) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.ID == "" || s.DeptID == "" {
		return fmt.Errorf("%w: empty student or department id", ErrInvalidParam)
	}
	if err := validateGrade(s.AvgGrade); err != nil {
		return err
	}
	if s.Credits < 0 {
		return fmt.Errorf("%w: negative credits for student %q", ErrInvalidParam, s.ID)
	}
	if _, ok := e.students[s.ID]; ok {
		return fmt.Errorf("%w: duplicate student %q", ErrInvalidParam, s.ID)
	}
	if !e.depts[s.DeptID] {
		return fmt.Errorf("%w: department %q", ErrNotFound, s.DeptID)
	}
	seen := map[string]bool{}
	for _, d := range s.Disciplines {
		if d.ID == "" || seen[d.ID] {
			return fmt.Errorf("%w: invalid discipline id %q", ErrInvalidParam, d.ID)
		}
		seen[d.ID] = true
	}
	e.students[s.ID] = s
	return nil
}

// CorrectGrade 成绩更正: 重写平均成绩与本周期不及格标记。
func (e *Engine) CorrectGrade(studentID string, avg float64, hasFailRecord bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if studentID == "" {
		return fmt.Errorf("%w: empty student id", ErrInvalidParam)
	}
	if err := validateGrade(avg); err != nil {
		return err
	}
	s, ok := e.students[studentID]
	if !ok {
		return fmt.Errorf("%w: student %q", ErrNotFound, studentID)
	}
	s.AvgGrade = avg
	s.HasFailRecord = hasFailRecord
	e.students[studentID] = s
	return nil
}

// RegisterDiscipline 处分登记。liftedAt 为零值表示无限期生效。
func (e *Engine) RegisterDiscipline(studentID, disciplineID string, liftedAt time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if studentID == "" || disciplineID == "" {
		return fmt.Errorf("%w: empty student or discipline id", ErrInvalidParam)
	}
	s, ok := e.students[studentID]
	if !ok {
		return fmt.Errorf("%w: student %q", ErrNotFound, studentID)
	}
	for _, d := range s.Disciplines {
		if d.ID == disciplineID {
			return fmt.Errorf("%w: duplicate discipline %q", ErrInvalidParam, disciplineID)
		}
	}
	s.Disciplines = append(s.Disciplines, Discipline{ID: disciplineID, LiftedAt: liftedAt})
	e.students[studentID] = s
	return nil
}

// LiftDiscipline 处分解除: 将解除时刻设定为 liftedAt。
func (e *Engine) LiftDiscipline(studentID, disciplineID string, liftedAt time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if studentID == "" || disciplineID == "" {
		return fmt.Errorf("%w: empty student or discipline id", ErrInvalidParam)
	}
	s, ok := e.students[studentID]
	if !ok {
		return fmt.Errorf("%w: student %q", ErrNotFound, studentID)
	}
	for i, d := range s.Disciplines {
		if d.ID == disciplineID {
			s.Disciplines[i].LiftedAt = liftedAt
			e.students[studentID] = s
			return nil
		}
	}
	return fmt.Errorf("%w: discipline %q", ErrNotFound, disciplineID)
}

// CheckEligibility 判定一名学生是否具备某等级资格。
// 只做一次学生记录读取(Stats.StudentReads == 1), 开销与学生总数无关。
func (e *Engine) CheckEligibility(studentID, levelID string, at time.Time) (FailReason, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if studentID == "" || levelID == "" {
		return ReasonNone, fmt.Errorf("%w: empty student or level id", ErrInvalidParam)
	}
	s, ok := e.students[studentID]
	if !ok {
		return ReasonNone, fmt.Errorf("%w: student %q", ErrNotFound, studentID)
	}
	li, ok := e.levelIdx[levelID]
	if !ok {
		return ReasonNone, fmt.Errorf("%w: level %q", ErrNotFound, levelID)
	}
	e.stats = Stats{StudentReads: 1}
	return checkEligibility(&s, &e.cfg.Levels[li], at), nil
}

// Evaluate 以 at 为评定时刻重新评定, 结果与用当前数据从零评定完全相同,
// 但已确认奖励被钉住并优先占用名额。评定结果成为新的当前结果。
func (e *Engine) Evaluate(at time.Time) Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stats = Stats{}
	res := runAllocation(&e.cfg, e.students, e.confirmed, at, &e.stats)
	e.current = res
	return cloneResult(res)
}

// Current 返回当前评定结果; 尚未评定时 ok 为 false。
func (e *Engine) Current() (res Result, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current == nil {
		return Result{}, false
	}
	return cloneResult(e.current), true
}

// Confirm 确认当前评定结果中某学生的奖励。
// 拒绝优先级: 参数非法 > 不存在 > 未评定 > 奖励不在当前结果 > 已确认。
// 被拒绝时不改动任何状态。
func (e *Engine) Confirm(studentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if studentID == "" {
		return fmt.Errorf("%w: empty student id", ErrInvalidParam)
	}
	if _, ok := e.students[studentID]; !ok {
		return fmt.Errorf("%w: student %q", ErrNotFound, studentID)
	}
	if e.current == nil {
		return ErrNotEvaluated
	}
	var award *Award
	for i := range e.current.Awards {
		if e.current.Awards[i].StudentID == studentID {
			award = &e.current.Awards[i]
			break
		}
	}
	if award == nil {
		return fmt.Errorf("%w: student %q", ErrAwardNotInResult, studentID)
	}
	if _, ok := e.confirmed[studentID]; ok {
		return fmt.Errorf("%w: student %q", ErrAlreadyConfirmed, studentID)
	}
	e.confirmed[studentID] = *award
	return nil
}

// LastStats 返回最近一次 Evaluate 或 CheckEligibility 的计数器, 用于验证复杂度约束。
func (e *Engine) LastStats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

func cloneResult(r *Result) Result {
	c := *r
	c.Awards = append([]Award(nil), r.Awards...)
	c.Disqualified = append([]Disqualification(nil), r.Disqualified...)
	c.Rankings = append([]DeptRanking(nil), r.Rankings...)
	for i := range c.Rankings {
		c.Rankings[i].Entries = append([]RankEntry(nil), r.Rankings[i].Entries...)
	}
	return c
}
