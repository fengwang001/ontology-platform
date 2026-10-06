package grading

// Role 标识任务在一份答卷中的角色。
type Role int

const (
	RoleFirst Role = iota + 1
	RoleSecond
	RoleArbiter
)

// TaskStatus 是任务状态。
type TaskStatus int

const (
	StatusOpen TaskStatus = iota + 1
	StatusSubmitted
	StatusWithdrawn
)

// Task 是一次评卷任务。
type Task struct {
	ID      string
	PaperID string
	Grader  string
	Group   string
	Role    Role
	Status  TaskStatus
	Score   int
}

// FinalScore 是答卷终分及判定依据。
type FinalScore struct {
	Score  int
	Reason string
}

// Paper 是答卷的不可变描述。
type Paper struct {
	ID       string
	Question string
	MaxScore int
	Student  string
}

// paperState 保存答卷的可变状态。
type paperState struct {
	desc       Paper
	tasks      map[string]*Task
	byRole     map[Role]*Task
	final      *FinalScore
	needArb    bool
	unassigned bool
}
