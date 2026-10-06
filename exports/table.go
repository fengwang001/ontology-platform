package exports

import (
	"strings"
)

// Entry 是导出映射表的一项：子路径键 + 目标。
type Entry struct {
	Key    string
	Target Target
}

// Result 是一次成功解析的结果与判定依据。
type Result struct {
	// Target 是替换、校验后的最终目标字符串。
	Target string
	// Key 是选中的键（通配键以原始形式给出，含 "*"）。
	Key string
	// Conditions 是依次命中的条件名序列（含 "default"）。
	Conditions []string
}

// Table 是一张构造完成、不可修改的导出映射表。
// 构造时完成全部表级校验；之后可并发安全地用于解析。
type Table struct {
	idx index
}

// NewTable 校验并构造导出映射表。entries 的顺序即条件映射之外的键序，
// 键的先后顺序不影响解析结果（精确优先，通配按长度裁决）。
// 违反任一表规则时返回 KindInvalidTable 错误。
func NewTable(entries []Entry) (*Table, error) {
	ix := newIndex()
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if err := validateKey(e.Key); err != nil {
			return nil, err
		}
		if _, dup := seen[e.Key]; dup {
			return nil, newError(KindInvalidTable, "duplicate key %q", e.Key)
		}
		seen[e.Key] = struct{}{}
		if err := validateTarget(e.Target, e.Key); err != nil {
			return nil, err
		}
		ix.add(e.Key, e.Target)
	}
	return &Table{idx: ix}, nil
}

func validateKey(key string) error {
	if key != "." && !strings.HasPrefix(key, "./") {
		return newError(KindInvalidTable, "key %q must be \".\" or start with \"./\"", key)
	}
	if strings.Count(key, "*") > 1 {
		return newError(KindInvalidTable, "key %q contains more than one \"*\"", key)
	}
	return nil
}

func validateTarget(t Target, ctx string) error {
	switch t.kind {
	case TargetString, TargetForbidden:
		return nil
	case TargetConditions:
		if len(t.conds) == 0 {
			return newError(KindInvalidTable, "empty conditions map under %q", ctx)
		}
		seen := make(map[string]struct{}, len(t.conds))
		for i, c := range t.conds {
			if c.Name == "" {
				return newError(KindInvalidTable, "empty condition name under %q", ctx)
			}
			if isAllDigits(c.Name) {
				return newError(KindInvalidTable, "condition name %q under %q is all digits", c.Name, ctx)
			}
			if _, dup := seen[c.Name]; dup {
				return newError(KindInvalidTable, "duplicate condition name %q under %q", c.Name, ctx)
			}
			seen[c.Name] = struct{}{}
			if c.Name == "default" && i != len(t.conds)-1 {
				return newError(KindInvalidTable, "\"default\" condition under %q is not last", ctx)
			}
			if c.Target == nil {
				return newError(KindInvalidTable, "condition %q under %q has nil target", c.Name, ctx)
			}
			if err := validateTarget(*c.Target, ctx); err != nil {
				return err
			}
		}
		return nil
	default:
		return newError(KindInvalidTable, "target under %q has unknown kind %d", ctx, int(t.kind))
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Resolve 在表上执行一次纯函数式解析。
// subpath 必须是 "." 或以 "./" 开头且不含 "*"，
// conditions 是活动条件名集合（区分大小写，"default" 总是命中）。
func (t *Table) Resolve(subpath string, conditions []string) (Result, error) {
	if err := validateRequest(subpath, conditions); err != nil {
		return Result{}, err
	}
	condSet := make(map[string]bool, len(conditions))
	for _, c := range conditions {
		condSet[c] = true
	}

	key, tgt, _, ok := t.idx.lookup(subpath)
	if !ok {
		return Result{}, newError(KindNotExported, "subpath %q is not exported", subpath)
	}

	// 键一旦选中即不回退。计算通配匹配段（非通配键为空）。
	matched := ""
	hasStar := false
	if star := strings.IndexByte(key, '*'); star >= 0 {
		hasStar = true
		matched = subpath[star : len(subpath)-(len(key)-star-1)]
	}

	var chain []string
	out, err := resolveTarget(tgt, condSet, matched, hasStar, &chain)
	if err != nil {
		return Result{}, err
	}
	return Result{Target: out, Key: key, Conditions: chain}, nil
}

func validateRequest(subpath string, conditions []string) error {
	if subpath != "." && !strings.HasPrefix(subpath, "./") {
		return newError(KindInvalidRequest, "subpath %q must be \".\" or start with \"./\"", subpath)
	}
	if strings.Contains(subpath, "*") {
		return newError(KindInvalidRequest, "subpath %q must not contain \"*\"", subpath)
	}
	for _, c := range conditions {
		if c == "" {
			return newError(KindInvalidRequest, "condition names must be non-empty")
		}
	}
	return nil
}

// resolveTarget 解析选中键对应的目标。
// chain 记录命中的条件名序列；内部无匹配条件时回退并回滚 chain。
func resolveTarget(tgt Target, conds map[string]bool, matched string, hasStar bool, chain *[]string) (string, error) {
	switch tgt.kind {
	case TargetForbidden:
		return "", newError(KindForbidden, "resolution is explicitly forbidden")
	case TargetString:
		s := tgt.value
		if hasStar {
			s = strings.ReplaceAll(s, "*", matched)
		}
		if err := validateTargetString(s); err != nil {
			return "", err
		}
		return s, nil
	case TargetConditions:
		for _, c := range tgt.conds {
			if c.Name != "default" && !conds[c.Name] {
				continue
			}
			mark := len(*chain)
			*chain = append(*chain, c.Name)
			out, err := resolveTarget(*c.Target, conds, matched, hasStar, chain)
			if err != nil {
				if IsKind(err, KindNoMatchingCondition) {
					// 该目标内部没有任何条件命中：回滚并继续试后面的条件。
					*chain = (*chain)[:mark]
					continue
				}
				// 被禁止与非法目标都直接上报，不再回退。
				return "", err
			}
			return out, nil
		}
		return "", newError(KindNoMatchingCondition, "no condition matched")
	default:
		return "", newError(KindInvalidTable, "target has unknown kind %d", int(tgt.kind))
	}
}

// validateTargetString 校验替换后的目标字符串。
// 必须以 "./" 开头；其后的每一段不得是 "."、".." 或 "node_modules"（不区分大小写）。
func validateTargetString(s string) error {
	if !strings.HasPrefix(s, "./") {
		return newError(KindInvalidTarget, "target %q must start with \"./\"", s)
	}
	for _, seg := range strings.Split(s[2:], "/") {
		if seg == "." || seg == ".." || strings.EqualFold(seg, "node_modules") {
			return newError(KindInvalidTarget, "target %q contains forbidden segment %q", s, seg)
		}
	}
	return nil
}
