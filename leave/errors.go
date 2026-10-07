// Package leave 实现企业年休假额度账户服务。
//
// 时间以整数日序号计，年度 y 覆盖日序号 [365y, 365y+364]，不考虑闰年。
// 所有会改变状态的操作都携带 now，且 now 不得小于上一次被接受操作的 now。
package leave

import "fmt"

// Category 是可区分的错误类别。
//
// 一个操作同时违反多条规则时，按以下优先级只报告第一个：
// 参数非法 > 时钟回退 > 员工不存在 > 状态不允许 > 区间重叠 > 额度不足。
type Category int

const (
	// CatInvalidParam 参数非法（含不依赖状态的入参校验，以及依赖员工数据的
	// 入参校验，如请假起始日早于入职日；后者在员工存在性检查之后报告）。
	CatInvalidParam Category = iota
	// CatClockRegression 时钟回退：now 小于上一次被接受操作的 now。
	CatClockRegression
	// CatEmployeeNotFound 员工不存在（或在被查询的历史时刻尚未登记）。
	CatEmployeeNotFound
	// CatInvalidState 状态不允许：假单不存在、状态机不允许的迁移、重复登记等。
	CatInvalidState
	// CatOverlap 与同一员工已有未终结请假的日期区间重叠。
	CatOverlap
	// CatInsufficientQuota 额度不足，整单拒绝。
	CatInsufficientQuota
)

func (c Category) String() string {
	switch c {
	case CatInvalidParam:
		return "invalid_param"
	case CatClockRegression:
		return "clock_regression"
	case CatEmployeeNotFound:
		return "employee_not_found"
	case CatInvalidState:
		return "invalid_state"
	case CatOverlap:
		return "overlap"
	case CatInsufficientQuota:
		return "insufficient_quota"
	}
	return "unknown"
}

// Error 是服务返回的唯一错误类型，携带可区分的类别。
type Error struct {
	Cat Category
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Cat, e.Msg) }

func errf(cat Category, format string, args ...any) *Error {
	return &Error{Cat: cat, Msg: fmt.Sprintf(format, args...)}
}

// CategoryOf 提取错误的类别；非本服务错误返回 ok=false。
func CategoryOf(err error) (Category, bool) {
	if e, ok := err.(*Error); ok {
		return e.Cat, true
	}
	return 0, false
}
