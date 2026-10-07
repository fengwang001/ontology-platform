package exports

import (
	"strings"
	"sync/atomic"
)

// Resolver 持有当前生效的导出映射表，支持并发解析与原子换表。
//
// 解析与换表可并发进行：每次解析开始时一次性加载表指针，
// 因此每次解析完整地看到旧表或新表之一，绝不混用两张表。
type Resolver struct {
	table atomic.Pointer[ExportTable]
}

// NewResolver 以 entries 构造解析器；表非法时返回错误。
func NewResolver(entries []Entry) (*Resolver, error) {
	t, err := NewTable(entries)
	if err != nil {
		return nil, err
	}
	r := &Resolver{}
	r.table.Store(t)
	return r, nil
}

// Replace 以新的条目原子替换当前表；新表非法时旧表继续生效。
func (r *Resolver) Replace(entries []Entry) error {
	t, err := NewTable(entries)
	if err != nil {
		return err
	}
	r.table.Store(t)
	return nil
}

// Swap 以已构造好的表原子替换当前表；nil 表被拒绝，旧表继续生效。
func (r *Resolver) Swap(t *ExportTable) error {
	if t == nil {
		return errf(KindInvalidTable, "不能替换为 nil 表")
	}
	r.table.Store(t)
	return nil
}

// Resolution 是一次成功解析的结果及其判定依据。
type Resolution struct {
	Target     string   // 最终解析出的目标字符串
	Key        string   // 选中的键
	Matched    string   // 键含星号时的匹配段
	Conditions []string // 依次命中的条件名序列
}

// Resolve 解析一次导入请求。
//
// 错误优先级：请求非法 > 子路径未导出，其后依解析路径上遇到的
// 先后为被禁止、无匹配条件、非法目标。被拒绝的请求不改变解析器状态。
func (r *Resolver) Resolve(subpath string, conditions []string) (Resolution, error) {
	condSet, err := validateRequest(subpath, conditions)
	if err != nil {
		return Resolution{}, err
	}
	t := r.table.Load()
	key, target, matched, ok := t.selectKey(subpath)
	if !ok {
		return Resolution{}, errf(KindSubpathNotExported, "子路径 %q 未导出", subpath)
	}
	hasStar := strings.IndexByte(key, '*') >= 0
	final, condSeq, rerr := resolveTarget(target, condSet, matched, hasStar)
	if rerr != nil {
		return Resolution{}, rerr
	}
	return Resolution{
		Target:     final,
		Key:        key,
		Matched:    matched,
		Conditions: condSeq,
	}, nil
}

// validateRequest 校验请求：子路径恰为一个点或以点斜杠开头且不含星号；
// 活动条件名必须非空。返回去重后的条件集合。
func validateRequest(subpath string, conditions []string) (map[string]struct{}, error) {
	if subpath != "." && !strings.HasPrefix(subpath, "./") {
		return nil, errf(KindInvalidRequest, "子路径 %q 必须恰为 \".\" 或以 \"./\" 开头", subpath)
	}
	if strings.Contains(subpath, "*") {
		return nil, errf(KindInvalidRequest, "子路径 %q 不得含星号", subpath)
	}
	condSet := make(map[string]struct{}, len(conditions))
	for _, c := range conditions {
		if c == "" {
			return nil, errf(KindInvalidRequest, "活动条件含空串")
		}
		condSet[c] = struct{}{}
	}
	return condSet, nil
}

// resolveTarget 递归解析目标。
// 条件映射按顺序找第一个命中的条件解析其目标；若该目标内部
// 没有任何条件命中（KindNoMatchingCondition）则继续试后面的条件；
// 被禁止与非法目标立即向上传播，不再回退。
func resolveTarget(t Target, condSet map[string]struct{}, matched string, hasStar bool) (string, []string, *Error) {
	switch t.kind {
	case targetString:
		s := t.value
		if hasStar {
			s = t.substitute(matched)
		}
		if err := validateTargetString(s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	case targetForbidden:
		return "", nil, errf(KindForbidden, "目标被显式禁止")
	default: // targetConditions
		for _, pair := range t.conds {
			if pair.Condition != "default" {
				if _, active := condSet[pair.Condition]; !active {
					continue
				}
			}
			s, seq, err := resolveTarget(pair.Target, condSet, matched, hasStar)
			if err == nil {
				return s, append([]string{pair.Condition}, seq...), nil
			}
			if err.Kind == KindNoMatchingCondition {
				continue
			}
			return "", nil, err
		}
		return "", nil, errf(KindNoMatchingCondition, "条件映射中没有条件命中")
	}
}

// validateTargetString 校验字符串目标：必须以点斜杠开头；
// 其后的每一段不得是点、两点或不区分大小写的 node_modules。
func validateTargetString(s string) *Error {
	if !strings.HasPrefix(s, "./") {
		return errf(KindInvalidTarget, "目标 %q 必须以 \"./\" 开头", s)
	}
	for _, seg := range strings.Split(s[2:], "/") {
		if seg == "." || seg == ".." || strings.EqualFold(seg, "node_modules") {
			return errf(KindInvalidTarget, "目标 %q 含非法段 %q", s, seg)
		}
	}
	return nil
}
