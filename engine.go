package ontology

import (
	"fmt"
	"strings"
	"sync"
)

// Engine 成绩复核与改分审计引擎。所有方法并发安全，
// 并发调用的结果等价于某个串行顺序（单一互斥锁串行化）。
type Engine struct {
	mu sync.Mutex

	cfg Config

	// 成绩记录索引：termID -> studentID|courseID -> *Record
	records map[string]map[string]*Record
	// 学生-学期反查索引：termID|studentID -> 该学生该学期的记录
	// 使学期均分查询开销只随该学生自己的课程数增长。
	studentTerm map[string][]*Record

	// (termID, courseID) -> 授课教师 ID
	teachers map[string]map[string]string
	// 用户 ID -> 权限级别
	levels map[string]int
	// 已锁定学期
	locked map[string]bool

	// 已观察到的最大时刻；仅成功的变更操作推进它。
	clock int64

	audit []AuditEntry
}

type recKey struct {
	student string
	course  string
}

// NewEngine 创建引擎。
func NewEngine(cfg Config) *Engine {
	if cfg.ReviewWindow < 0 || cfg.ApproveTimeout < 0 || cfg.FirstValidFor < 0 ||
		cfg.MinScore > cfg.MaxScore {
		panic("invalid engine config")
	}
	return &Engine{
		cfg:         cfg,
		records:     map[string]map[string]*Record{},
		studentTerm: map[string][]*Record{},
		teachers:    map[string]map[string]string{},
		levels:      map[string]int{},
		locked:      map[string]bool{},
	}
}

// RegisterTeacher 登记课程的授课教师。
func (e *Engine) RegisterTeacher(termID, courseID, teacherID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if termID == "" || courseID == "" || teacherID == "" {
		return errf(ErrInvalid, "empty term/course/teacher id")
	}
	courses := e.teachers[termID]
	if courses == nil {
		courses = map[string]string{}
		e.teachers[termID] = courses
	}
	courses[courseID] = teacherID
	return nil
}

// SetUserLevel 设置用户权限级别。
func (e *Engine) SetUserLevel(userID string, level int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if userID == "" {
		return errf(ErrInvalid, "empty user id")
	}
	e.levels[userID] = level
	return nil
}

// Clock 返回当前逻辑时钟（已落地的最大时刻）。
func (e *Engine) Clock() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// Audit 返回审计账本的只读拷贝（按发生次序）。
func (e *Engine) Audit() []AuditEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AuditEntry, len(e.audit))
	copy(out, e.audit)
	return out
}

var errorPriority = map[string]int{
	ErrInvalid:   1,
	ErrClock:     2,
	ErrNotFound:  3,
	ErrLocked:    4,
	ErrForbidden: 5,
	ErrExpired:   6,
	ErrState:     7,
	ErrScore:     8,
}

// ---- 持锁辅助 ----

func recKeyID(studentID, courseID string) string {
	return studentID + "\x00" + courseID
}

func studentTermKey(termID, studentID string) string {
	return termID + "\x00" + studentID
}

func (e *Engine) getRecord(studentID, courseID, termID string) *Record {
	if m := e.records[termID]; m != nil {
		return m[recKeyID(studentID, courseID)]
	}
	return nil
}

func (e *Engine) teacherOf(rec *Record) string {
	if m := e.teachers[rec.TermID]; m != nil {
		return m[rec.CourseID]
	}
	return ""
}

func (e *Engine) userLevel(userID string) int {
	return e.levels[userID]
}

// checkClock 校验时钟单调性（仅变更操作调用）。
func (e *Engine) checkClock(at int64) error {
	if at < e.clock {
		return errf(ErrClock, "at=%d before engine clock %d", at, e.clock)
	}
	return nil
}

func (e *Engine) advance(at int64) {
	if at > e.clock {
		e.clock = at
	}
}

func (e *Engine) appendAudit(kind AuditKind, actor string, at int64, rec *Record, detail string) {
	e.audit = append(e.audit, AuditEntry{
		Seq:       len(e.audit) + 1,
		Kind:      kind,
		Actor:     actor,
		At:        at,
		StudentID: rec.StudentID,
		CourseID:  rec.CourseID,
		TermID:    rec.TermID,
		Detail:    detail,
	})
}

func (e *Engine) appendAuditTerm(kind AuditKind, actor, termID string, at int64, detail string) {
	e.audit = append(e.audit, AuditEntry{
		Seq:    len(e.audit) + 1,
		Kind:   kind,
		Actor:  actor,
		At:     at,
		TermID: termID,
		Detail: detail,
	})
}

func nonEmpty(ids ...string) bool {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return false
		}
	}
	return true
}

func (e *Engine) currentScore(rec *Record) int {
	return rec.Versions[len(rec.Versions)-1].Score
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (e *Engine) addVersion(rec *Record, score int, at int64, src Source) {
	rec.Versions = append(rec.Versions, Version{Score: score, Effective: at, Source: src})
}

func (e *Engine) openReview(rec *Record) *Review {
	if n := len(rec.Reviews); n > 0 && rec.Reviews[n-1].Status == ReviewOpen {
		return rec.Reviews[n-1]
	}
	return nil
}

func describeRecord(rec *Record) string {
	return fmt.Sprintf("student=%s course=%s term=%s", rec.StudentID, rec.CourseID, rec.TermID)
}
