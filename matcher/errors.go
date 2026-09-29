package matcher

import "errors"

// 查询被拒绝时返回下列哨兵错误，调用方可用 errors.Is 区分原因。
// 被拒绝的查询不会返回任何部分结果。
var (
	// ErrEmptyPattern：模式为空，匹配会退化为对整张图的无约束全搜索。
	ErrEmptyPattern = errors.New("matcher: empty pattern rejected (would degenerate into a full scan)")
	// ErrUnconstrainedVariable：存在与其它变量无边相连且无类型/属性约束的变量，
	// 匹配会退化为对整张图的笛卡尔积式全搜索。
	ErrUnconstrainedVariable = errors.New("matcher: unconstrained variable rejected (would degenerate into a full scan)")
	// ErrPatternDefinition：模式自身定义非法（空变量名、重复变量、
	// 边引用未定义变量、非法算子等）。
	ErrPatternDefinition = errors.New("matcher: invalid pattern definition")
	// ErrInconsistentBinding：同一变量在一次匹配中被要求绑定不同对象。
	ErrInconsistentBinding = errors.New("matcher: inconsistent variable binding rejected")
	// ErrDuplicateMatch：同一组对象映射在去重后仍重复出现。
	ErrDuplicateMatch = errors.New("matcher: duplicate isomorphic match rejected")
	// ErrFullScanDegeneration：搜索过程中出现无类型/无边约束的候选枚举，
	// 即匹配退化为全搜索。
	ErrFullScanDegeneration = errors.New("matcher: search degenerated into a full scan")
)
