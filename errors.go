package ontology

import "fmt"

// RuleError 定位规则集中的某一条规则。index 从 1 开始，
// kind 为 "allow" 或 "deny"。
type RuleError struct {
	Kind  string
	Index int
	Raw   string
	Msg   string
}

func (e *RuleError) Error() string {
	return fmt.Sprintf("%s rule #%d %q: %s", e.Kind, e.Index, e.Raw, e.Msg)
}

// ConflictError 表示完全相同的模式同时出现在允许集与拒绝集中。
type ConflictError struct {
	Raw        string
	AllowIndex int
	DenyIndex  int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflicting rules: pattern %q is both allowed (#%d) and denied (#%d)",
		e.Raw, e.AllowIndex, e.DenyIndex)
}

// RequiredHiddenError 表示某个必填属性被规则裁掉。
type RequiredHiddenError struct {
	Path   []string
	Reason Reason
}

func (e *RequiredHiddenError) Error() string {
	return fmt.Sprintf("required field %q is hidden by %s rule #%d %q",
		joinPath(e.Path), e.Reason.Decision.Kind, e.Reason.Decision.Index, e.Reason.Decision.Raw)
}

// DependencyError 表示依赖来源不可见而结果可见，且策略为报错。
type DependencyError struct {
	Source []string
	Target []string
	Reason Reason
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("field %q is visible but its dependency source %q is hidden by %s rule #%d %q",
		joinPath(e.Target), joinPath(e.Source),
		e.Reason.Decision.Kind, e.Reason.Decision.Index, e.Reason.Decision.Raw)
}

func joinPath(path []string) string {
	out := ""
	for i, seg := range path {
		if i > 0 {
			out += "."
		}
		out += seg
	}
	return out
}
