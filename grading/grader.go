package grading

// Grader 是评卷人的不可变描述。
type Grader struct {
	ID         string
	Group      string
	DailyQuota int
	Students   []string
}

// graderState 保存评卷人的可变状态。
type graderState struct {
	desc             Grader
	active           bool
	students         map[string]struct{}
	load             int
	assigned         map[string]struct{}
	heapIdx          int
	openTasks        []*Task
	studentOpenCount map[string]int
}
