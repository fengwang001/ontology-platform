package ontology

import "fmt"

// ErrorKind 以稳定的枚举值区分各类拒绝原因，调用方可据此分支处理。
type ErrorKind string

const (
	ErrEmptyName         ErrorKind = "empty_name"          // 名称为空字符串
	ErrDuplicateName     ErrorKind = "duplicate_name"      // 注册时名称已存在
	ErrCycleDetected     ErrorKind = "cycle_detected"      // 依赖关系构成有向环
	ErrUnknownDep        ErrorKind = "unknown_dependency"  // 重算时依赖尚未注册
	ErrNameNotRegistered ErrorKind = "name_not_registered" // 读写未注册名称
	ErrNotBaseView       ErrorKind = "not_base_view"       // 对非基视图调用设值
	ErrBaseViewWithFn    ErrorKind = "base_view_with_fn"   // 基视图（无依赖）不能携带计算函数
	ErrDerivedWithoutFn  ErrorKind = "derived_without_fn"  // 派生视图（有依赖）必须提供计算函数
	ErrNotMaterialized   ErrorKind = "not_materialized"    // 视图已注册但从未完成过重算
	ErrBaseNotSet        ErrorKind = "base_not_set"        // 重算闭包内基视图尚未外部设值
	ErrComputeFailed     ErrorKind = "compute_failed"      // 用户计算函数执行异常
)

// GraphError 携带可区分的错误种类、出错视图名与人类可读信息。
type GraphError struct {
	Kind    ErrorKind
	Name    string
	Message string
}

func (e *GraphError) Error() string {
	if e.Name == "" {
		return fmt.Sprintf("ontology: %s: %s", e.Kind, e.Message)
	}
	return fmt.Sprintf("ontology: %s: %s: %s", e.Kind, e.Name, e.Message)
}

func graphError(kind ErrorKind, name, message string) *GraphError {
	return &GraphError{Kind: kind, Name: name, Message: message}
}
