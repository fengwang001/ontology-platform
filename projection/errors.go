package projection

import "fmt"

// PatternError 表示编译阶段发现的无效模式，定位到集合与序号。
type PatternError struct {
	Set     string // "allow" 或 "deny"
	Index   int    // 在该集合中的下标（从 0 开始）
	Pattern string // 规则原文
	Reason  string
}

func (e *PatternError) Error() string {
	return fmt.Sprintf("无效规则模式（%s 第 %d 条）%q: %s", e.Set, e.Index+1, e.Pattern, e.Reason)
}

// ConflictError 表示同一模式同时出现在允许与拒绝集合中。
type ConflictError struct {
	Pattern string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("规则冲突: 模式 %q 同时出现在允许与拒绝集合中", e.Pattern)
}

// RequiredFieldError 表示必填属性在投影结果中缺失。
type RequiredFieldError struct {
	Field string // 被裁掉的必填属性
	Rule  string // 裁掉它的规则原文；若非规则所致则为空
}

func (e *RequiredFieldError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("必填属性 %q 被规则 %q 裁剪", e.Field, e.Rule)
	}
	return fmt.Sprintf("必填属性 %q 在投影结果中缺失", e.Field)
}

// DependencyError 表示计算来源不可见而结果可见（FailOnHiddenSource 策略）。
type DependencyError struct {
	Field  string // 计算结果属性
	Source string // 不可见的来源属性
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("属性 %q 的计算来源 %q 不可见", e.Field, e.Source)
}
