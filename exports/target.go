package exports

import "strings"

// targetKind 区分目标的三种形态。
type targetKind int

const (
	targetString targetKind = iota
	targetForbidden
	targetConditions
)

// CondPair 是条件映射中的一项：条件名 + 命中时继续解析的目标。
type CondPair struct {
	Condition string
	Target    Target
}

// Cond 构造一个条件项，便于以字面量方式组装条件映射。
func Cond(condition string, target Target) CondPair {
	return CondPair{Condition: condition, Target: target}
}

// Target 表示一个键对应的解析目标：相对目标字符串、显式禁止或条件映射。
// 条件映射是有序的，目标可以再嵌套条件映射。
type Target struct {
	kind  targetKind
	value string     // targetString 使用
	conds []CondPair // targetConditions 使用
}

// StringTarget 构造相对目标字符串，例如 "./dist/index.js"。
func StringTarget(s string) Target {
	return Target{kind: targetString, value: s}
}

// ForbiddenTarget 构造显式禁止目标（对应 exports 中的 null）。
func ForbiddenTarget() Target {
	return Target{kind: targetForbidden}
}

// ConditionsTarget 构造有序条件映射。
func ConditionsTarget(pairs ...CondPair) Target {
	return Target{kind: targetConditions, conds: pairs}
}

// validate 在构造期递归校验目标。
// 字符串目标的合法性依赖通配符匹配段，留到解析期检查，此处不校验。
func (t Target) validate(path string) error {
	if t.kind != targetConditions {
		return nil
	}
	if len(t.conds) == 0 {
		return errf(KindInvalidTable, "%s: 条件映射为空", path)
	}
	seen := make(map[string]struct{}, len(t.conds))
	for i, pair := range t.conds {
		name := pair.Condition
		if name == "" {
			return errf(KindInvalidTable, "%s: 条件名为空串", path)
		}
		if isAllDigits(name) {
			return errf(KindInvalidTable, "%s: 条件名 %q 全由数字组成", path, name)
		}
		if _, dup := seen[name]; dup {
			return errf(KindInvalidTable, "%s: 重复的条件名 %q", path, name)
		}
		seen[name] = struct{}{}
		if name == "default" && i != len(t.conds)-1 {
			return errf(KindInvalidTable, "%s: 条件 default 不在所在条件映射的最后", path)
		}
		if err := pair.Target.validate(path + "/" + name); err != nil {
			return err
		}
	}
	return nil
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// substitute 把目标字符串中的每个星号替换为通配符匹配段。
func (t Target) substitute(matched string) string {
	return strings.ReplaceAll(t.value, "*", matched)
}
