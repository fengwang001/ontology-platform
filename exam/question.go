package exam

// Lifecycle 题目生命周期三态。
type Lifecycle int

const (
	Available Lifecycle = iota // 可用
	Suspended                  // 停用
	Withdrawn                  // 下架（不可恢复）
)

func (l Lifecycle) String() string {
	switch l {
	case Available:
		return "available"
	case Suspended:
		return "suspended"
	case Withdrawn:
		return "withdrawn"
	}
	return "unknown"
}

// Version 题目的一个不可变版本。
type Version struct {
	Number          int
	Score           int
	Difficulty      int
	KnowledgePoints []string
}

// Question 题目及其全部版本。版本号在题内从 1 起单调递增。
type Question struct {
	ID       string
	Life     Lifecycle
	Versions []Version
}

func newQuestion(id string, score, difficulty int, kps []string) *Question {
	q := &Question{ID: id, Life: Available}
	q.Versions = append(q.Versions, Version{
		Number:          1,
		Score:           score,
		Difficulty:      difficulty,
		KnowledgePoints: sortedCopy(kps),
	})
	return q
}

func (q *Question) latest() Version { return q.Versions[len(q.Versions)-1] }

// revise 追加一个新版本。调用方须先完成全部校验，保证被拒绝的修订不消耗版本号。
func (q *Question) revise(score, difficulty int, kps []string) int {
	n := len(q.Versions) + 1
	q.Versions = append(q.Versions, Version{
		Number:          n,
		Score:           score,
		Difficulty:      difficulty,
		KnowledgePoints: sortedCopy(kps),
	})
	return n
}

// canTransition 报告生命周期迁移 from->to 是否合法。
// 合法迁移：可用->停用、停用->可用、可用->下架、停用->下架。
func canTransition(from, to Lifecycle) bool {
	switch from {
	case Available:
		return to == Suspended || to == Withdrawn
	case Suspended:
		return to == Available || to == Withdrawn
	}
	return false
}
