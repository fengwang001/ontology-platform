package ontology

import "fmt"

// DependencyPolicy 决定「来源不可见而结果可见」时的处理方式。
type DependencyPolicy int

const (
	// HideDependent 一并隐藏依赖该来源的计算结果字段。
	HideDependent DependencyPolicy = iota
	// FailOnHiddenSource 直接报错，拒绝返回不一致的投影。
	FailOnHiddenSource
)

// Dependency 声明 Result 字段以 Source 字段为计算来源。
type Dependency struct {
	Result string
	Source string
}

// Schema 描述对象的结构约束：必填属性与属性间的计算依赖。
type Schema struct {
	Required     []string
	Dependencies []Dependency
	Policy       DependencyPolicy
}

// RequiredFieldError 表示某个必填属性被规则裁掉，无法返回合法对象。
type RequiredFieldError struct {
	Path   string // 被裁掉的必填属性路径
	Rule   string // 裁掉它的规则原文；默认隐藏时为空
	Reason Reason // 完整的隐藏原因
}

func (e *RequiredFieldError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("required field %q was projected out by rule %q (%s)",
			e.Path, e.Rule, e.Reason)
	}
	return fmt.Sprintf("required field %q was projected out: %s", e.Path, e.Reason)
}

// DependencyError 表示计算结果可见而其来源不可见（FailOnHiddenSource 策略）。
type DependencyError struct {
	Result string
	Source string
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("field %q is visible but its computation source %q is hidden",
		e.Result, e.Source)
}
