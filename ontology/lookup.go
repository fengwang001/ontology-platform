package ontology

import "fmt"

// LookupResult 描述一次生效规则查找的结果。
type LookupResult struct {
	TypeID     string   // 发起查找的具体类型
	Property   string   // 查找的属性
	Path       []string // 实际遍历的继承链条（从具体类型到命中类型）
	SourceType string   // 命中声明所在的类型
	Rule       Rule     // 最终生效规则
}

// LookupRecord 是一次生效规则查找的审计记录。
type LookupRecord struct {
	Seq        uint64
	TypeID     string
	Property   string
	Path       []string
	SourceType string
	Rule       Rule
}

// MutationKind 区分注册表上发生的变更类别。
type MutationKind int

const (
	MutationDeclareRule MutationKind = iota
	MutationWriteValue
)

// MutationRecord 记录一次被接受的状态变更及其全局序号，
// 供并发场景下与朴素串行实现对照。
type MutationRecord struct {
	Seq      uint64
	Kind     MutationKind
	TypeID   string
	Property string
	Rule     Rule   // Kind 为 MutationDeclareRule 时有效
	Instance string // Kind 为 MutationWriteValue 时有效
	Value    string // Kind 为 MutationWriteValue 时有效
	RuleUsed Rule   // Kind 为 MutationWriteValue 时，写入校验实际使用的生效规则
}

// effectiveRuleLocked 从具体类型开始沿继承链向上查找，
// 返回链条上第一个对该属性有显式声明的类型所声明的规则。
// 调用方必须持有锁（读锁或写锁）。
// 遍历步数恰为具体类型到命中类型之间的实际深度，
// 与继承体系中其它分支的规模无关。
func (r *Registry) effectiveRuleLocked(typeID, property string) (LookupResult, bool) {
	res := LookupResult{TypeID: typeID, Property: property}
	for cur := typeID; cur != ""; {
		t, ok := r.types[cur]
		if !ok {
			return LookupResult{}, false
		}
		res.Path = append(res.Path, cur)
		if rule, declared := t.declared[property]; declared {
			res.SourceType = cur
			res.Rule = rule.clone()
			return res, true
		}
		cur = t.parentID
	}
	return LookupResult{}, false
}

// EffectiveRule 查询具体类型上某属性的最终生效规则，
// 并追加一条审计记录。类型或属性组合不存在时返回 ErrNotFound。
func (r *Registry) EffectiveRule(typeID, property string) (LookupResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.types[typeID]; !ok {
		return LookupResult{}, fmt.Errorf("%w: type %q", ErrNotFound, typeID)
	}
	res, found := r.effectiveRuleLocked(typeID, property)
	if !found {
		return LookupResult{}, fmt.Errorf("%w: type %q property %q", ErrNotFound, typeID, property)
	}
	r.seq++
	r.audit = append(r.audit, LookupRecord{
		Seq:        r.seq,
		TypeID:     res.TypeID,
		Property:   res.Property,
		Path:       res.Path,
		SourceType: res.SourceType,
		Rule:       res.Rule.clone(),
	})
	return res, nil
}

// AuditLog 返回全部生效规则查找的审计记录副本。
func (r *Registry) AuditLog() []LookupRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]LookupRecord(nil), r.audit...)
}

// MutationLog 返回全部被接受的状态变更记录副本（按全局序号递增）。
func (r *Registry) MutationLog() []MutationRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]MutationRecord(nil), r.mutations...)
}
