package exam

import "fmt"

// PaperState 试卷状态。
type PaperState int

const (
	Draft     PaperState = iota // 草稿
	Published                   // 已发布（冻结）
	Invalid                     // 失效（含下架题）
)

func (s PaperState) String() string {
	switch s {
	case Draft:
		return "draft"
	case Published:
		return "published"
	case Invalid:
		return "invalid"
	}
	return "unknown"
}

// DifficultyRange 某难度等级题目数的闭区间约束。
type DifficultyRange struct {
	Min int
	Max int
}

// Constraints 组卷约束配置。
type Constraints struct {
	TargetScore int                     // 总分须恰等于该值
	Coverage    map[string]int          // 必覆盖知识点 -> 最少题目数
	Difficulty  map[int]DifficultyRange // 难度等级 -> 题目数闭区间
}

// Entry 试卷中绑定的一道题及其冻结版本口径。
type Entry struct {
	QuestionID      string
	Version         int
	Score           int
	Difficulty      int
	KnowledgePoints []string
}

// Paper 试卷。草稿期维护题目 ID 列表，发布后冻结为版本快照。
type Paper struct {
	ID      string
	State   PaperState
	Cons    Constraints
	draft   []string
	entries []Entry
}

func (p *Paper) inDraft(qid string) bool {
	for _, id := range p.draft {
		if id == qid {
			return true
		}
	}
	return false
}

func (p *Paper) entryIndex(qid string) int {
	for i, e := range p.entries {
		if e.QuestionID == qid {
			return i
		}
	}
	return -1
}

// validateEntries 按固定优先级校验一组条目：互斥 > 总分 > 覆盖 > 难度。
// 返回首个不满足的约束类别及涉及题目；全部满足返回 nil。
func validateEntries(op string, entries []Entry, c Constraints, mi *MutexIndex) *Error {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.QuestionID
	}
	if a, b, ok := mi.conflictInSet(ids); ok {
		return newErr(CatMutexConflict, op,
			fmt.Sprintf("questions %s and %s are mutually exclusive (transitive closure of shared groups)", a, b), a, b)
	}
	total := 0
	for _, e := range entries {
		total += e.Score
	}
	if total != c.TargetScore {
		return newErr(CatTotalScore, op,
			fmt.Sprintf("total score %d != target %d", total, c.TargetScore), ids...)
	}
	for _, kp := range sortedKeys(c.Coverage) {
		need := c.Coverage[kp]
		got := 0
		for _, e := range entries {
			if contains(e.KnowledgePoints, kp) {
				got++
			}
		}
		if got < need {
			return newErr(CatCoverage, op,
				fmt.Sprintf("knowledge point %q covered by %d questions, need >= %d", kp, got, need), ids...)
		}
	}
	for _, level := range sortedIntKeys(c.Difficulty) {
		r := c.Difficulty[level]
		got := 0
		for _, e := range entries {
			if e.Difficulty == level {
				got++
			}
		}
		if got < r.Min || got > r.Max {
			return newErr(CatDifficulty, op,
				fmt.Sprintf("difficulty %d has %d questions, need in [%d,%d]", level, got, r.Min, r.Max), ids...)
		}
	}
	return nil
}
