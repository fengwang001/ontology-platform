package exam

import "fmt"

// Category 错误类别。数值越小优先级越高，被拒绝的操作按优先级报出首个命中的类别。
type Category int

const (
	CatInvalidParam    Category = iota + 1 // 参数非法
	CatNotFound                            // 题目或试卷不存在
	CatStateNotAllowed                     // 状态不允许
	CatNotSelectable                       // 题目不可选用（停用或下架）
	CatMutexConflict                       // 互斥冲突
	CatTotalScore                          // 总分不符
	CatCoverage                            // 知识点覆盖不足
	CatDifficulty                          // 难度分布越界
)

func (c Category) String() string {
	switch c {
	case CatInvalidParam:
		return "invalid-param"
	case CatNotFound:
		return "not-found"
	case CatStateNotAllowed:
		return "state-not-allowed"
	case CatNotSelectable:
		return "not-selectable"
	case CatMutexConflict:
		return "mutex-conflict"
	case CatTotalScore:
		return "total-score-mismatch"
	case CatCoverage:
		return "knowledge-coverage-insufficient"
	case CatDifficulty:
		return "difficulty-out-of-range"
	}
	return "unknown"
}

// Error 是引擎返回的唯一错误类型，携带类别、可区分的理由与涉及题目/试卷。
type Error struct {
	Category  Category
	Op        string   // 触发操作
	Reason    string   // 可区分的判定依据
	Questions []string // 涉及题目
	Papers    []string // 涉及试卷
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s (questions=%v papers=%v)", e.Op, e.Category, e.Reason, e.Questions, e.Papers)
}

func newErr(cat Category, op, reason string, questions ...string) *Error {
	return &Error{Category: cat, Op: op, Reason: reason, Questions: questions}
}

// IsCategory 判断 err 是否属于指定类别。
func IsCategory(err error, cat Category) bool {
	e, ok := err.(*Error)
	return ok && e.Category == cat
}
