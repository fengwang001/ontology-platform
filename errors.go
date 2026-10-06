package ontology

import "fmt"

// ErrorKind 区分各类分析失败。错误判定与报告优先级为：
// 参数非法 > 不可访问属性 > 缺少判别属性 > 不可赋值。
type ErrorKind int

const (
	ErrInvalidArgument       ErrorKind = iota // 参数非法
	ErrPropertyNotAccessible                  // 不可访问属性
	ErrMissingDiscriminant                    // 缺少判别属性
	ErrNotAssignable                          // 不可赋值
)

// AnalysisError 描述导致整次分析失败的那一处错误。
type AnalysisError struct {
	Kind   ErrorKind
	StmtID string // 出错语句的唯一标识；声明级参数非法时为空
	Var    string // 相关变量（可能为空）
	Detail string // 人类可读的判定依据
	order  int    // 程序次序（仅内部用于同类错误排序）
}

func (e *AnalysisError) Error() string {
	return fmt.Sprintf("%s: 语句 %q 变量 %q: %s", e.Kind.String(), e.StmtID, e.Var, e.Detail)
}

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "参数非法"
	case ErrPropertyNotAccessible:
		return "不可访问属性"
	case ErrMissingDiscriminant:
		return "缺少判别属性"
	case ErrNotAssignable:
		return "不可赋值"
	}
	return "未知错误"
}

// pendingError 在一次分析中暂存候选错误；最终按优先级与程序次序取唯一一处。
type pendingError struct {
	err *AnalysisError
}

func (p *pendingError) record(kind ErrorKind, order int, stmtID, variable, detail string) {
	cand := &AnalysisError{Kind: kind, StmtID: stmtID, Var: variable, Detail: detail}
	if p.err == nil {
		p.err = cand
		p.err.order = order
		return
	}
	if kind < p.err.Kind || (kind == p.err.Kind && order < p.err.order) {
		p.err = cand
		p.err.order = order
	}
}

// QueryError 表示查询本身不合法（区别于分析失败与程序点不可达）。
type QueryError struct{ msg string }

func (e *QueryError) Error() string { return e.msg }
