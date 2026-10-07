package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// OpKind 是串行化操作日志的记录类别。
type OpKind string

const (
	OpAddType    OpKind = "add_type"
	OpSetParent  OpKind = "set_parent"
	OpUpsertRule OpKind = "upsert_rule"
	OpDeleteRule OpKind = "delete_rule"
	OpExport     OpKind = "export"
)

// OpRecord 是全局串行顺序中一条已生效操作的记录。
// 测试可据此重放，验证并发交错等价于该串行顺序。
type OpRecord struct {
	Seq      uint64
	Kind     OpKind
	TypeName string
	Parent   string
	Subject  string
	Property string
	Effect   Effect
	Content  string
	ExportID string
	Props    []string
}

// Platform 是权限固化导出与审计追溯的门面。
// 所有变更与导出在单一互斥锁下串行化，等价于某个全局串行顺序；
// 每次导出固化的排除依据对应该顺序下发起导出那一刻的规则状态。
type Platform struct {
	mu         sync.RWMutex
	types      *typeRegistry
	rules      *ruleStore
	principals map[string]bool
	exports    map[string]ExportResult
	opLog      []OpRecord
	seq        uint64
	probes     uint64 // 审计解析实际检查的版本记录数（性能可验证性）
}

func NewPlatform() *Platform {
	return &Platform{
		types:      newTypeRegistry(),
		rules:      newRuleStore(),
		principals: map[string]bool{},
		exports:    map[string]ExportResult{},
	}
}

// AddPrincipal 注册执行主体。
func (p *Platform) AddPrincipal(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.principals[id] = true
}

// AddType 注册对象类型。
func (p *Platform) AddType(name, parent string, properties []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.types.addType(name, parent, properties); err != nil {
		return err
	}
	p.log(OpRecord{Kind: OpAddType, TypeName: name, Parent: parent, Props: append([]string(nil), properties...)})
	return nil
}

// SetParent 重组继承关系。
func (p *Platform) SetParent(name, parent string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.types.setParent(name, parent); err != nil {
		return err
	}
	p.log(OpRecord{Kind: OpSetParent, TypeName: name, Parent: parent})
	return nil
}

// UpsertRule 在指定类型上声明/覆盖一条规则，返回新规则版本 ID。
// 每次调用都产生新的不可变版本，旧版本永久保留。
func (p *Platform) UpsertRule(typeName, subject, property string, effect Effect, content string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.types.types[typeName]; !ok {
		return "", newError(CategoryObjectTypeNotFound, "type not found: "+typeName)
	}
	if effect != EffectAllow && effect != EffectDeny {
		return "", newError(CategoryInvalidArgument, "unknown effect: "+string(effect))
	}
	p.log(OpRecord{Kind: OpUpsertRule, TypeName: typeName, Subject: subject, Property: property, Effect: effect, Content: content})
	id := p.rules.upsert(typeName, subject, property, effect, content, p.seq)
	return id, nil
}

// DeleteRule 删除指定类型上的生效规则（历史版本保留，继承查找回落到祖先规则）。
func (p *Platform) DeleteRule(typeName, subject, property string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.types.types[typeName]; !ok {
		return newError(CategoryObjectTypeNotFound, "type not found: "+typeName)
	}
	p.rules.delete(typeName, subject, property)
	p.log(OpRecord{Kind: OpDeleteRule, TypeName: typeName, Subject: subject, Property: property})
	return nil
}

// Export 对指定类型执行快照导出，固化每个被排除属性的排除依据。
// 拒绝优先级：对象类型不存在 > 执行主体不存在；被拒绝时不产生任何副作用。
// 默认允许：只有命中 deny 规则的属性才被排除，因此每个排除必有命中规则。
func (p *Platform) Export(typeName, principal string) (ExportResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.types.types[typeName]; !ok {
		return ExportResult{}, newError(CategoryObjectTypeNotFound, "type not found: "+typeName)
	}
	if !p.principals[principal] {
		return ExportResult{}, newError(CategoryPrincipalNotFound, "principal not found: "+principal)
	}
	props, _ := p.types.allProperties(typeName)
	chain, _ := p.types.chain(typeName)
	res := ExportResult{
		TypeName:   typeName,
		Principal:  principal,
		Exclusions: map[string]string{},
	}
	for _, prop := range props {
		if v, hit := p.rules.resolve(chain, principal, prop); hit && v.Effect == EffectDeny {
			// 固化：写入命中规则版本 ID，此后永不改变。
			res.Exclusions[prop] = v.ID
		} else {
			res.Included = append(res.Included, prop)
		}
	}
	p.log(OpRecord{Kind: OpExport, TypeName: typeName, Subject: principal})
	res.Seq = p.seq
	res.ID = fmt.Sprintf("exp-%d", p.seq)
	p.exports[res.ID] = res
	p.opLog[len(p.opLog)-1].ExportID = res.ID
	return cloneExport(res), nil
}

// AuditTrace 查询某次历史导出中某属性被排除的依据。
// 拒绝优先级：导出记录不存在 > 属性未被排除（明确报告而非报记录缺失）。
// 只读、幂等、结果可重复；解析不受规则后续任何变更影响。
func (p *Platform) AuditTrace(exportID, property string) (AuditRecord, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	exp, ok := p.exports[exportID]
	if !ok {
		return AuditRecord{}, newError(CategoryExportNotFound, "export not found: "+exportID)
	}
	ruleID, excluded := exp.Exclusions[property]
	if !excluded {
		return AuditRecord{}, newError(CategoryPropertyNotExcluded,
			fmt.Sprintf("property %q was not excluded in export %s", property, exportID))
	}
	p.probes++ // 一次哈希查找，检查记录数恒为 1
	v, ok := p.rules.version(ruleID)
	if !ok {
		return AuditRecord{}, newError(CategoryInvalidArgument, "invariant violated: missing rule version "+ruleID)
	}
	return AuditRecord{
		ExportID:   exportID,
		Property:   property,
		RuleID:     ruleID,
		Rule:       v,
		DeclaredOn: v.TypeName,
	}, nil
}

// GetExport 返回导出结果副本（只读）。
func (p *Platform) GetExport(exportID string) (ExportResult, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok := p.exports[exportID]
	if !ok {
		return ExportResult{}, false
	}
	return cloneExport(r), true
}

// OpLog 返回全局串行顺序的操作日志副本（测试用于重放验证）。
func (p *Platform) OpLog() []OpRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]OpRecord, len(p.opLog))
	copy(out, p.opLog)
	return out
}

// ProbeCount 返回审计解析累计检查的规则版本记录数。
func (p *Platform) ProbeCount() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.probes
}

// log 追加一条串行化操作记录（调用方须已持有写锁）。
func (p *Platform) log(rec OpRecord) {
	p.seq++
	rec.Seq = p.seq
	p.opLog = append(p.opLog, rec)
}

func cloneExport(r ExportResult) ExportResult {
	out := r
	out.Included = append([]string(nil), r.Included...)
	out.Exclusions = map[string]string{}
	for k, v := range r.Exclusions {
		out.Exclusions[k] = v
	}
	return out
}

// SortedExclusions 返回有序的排除列表，便于日志与比较。
func SortedExclusions(r ExportResult) []string {
	out := make([]string, 0, len(r.Exclusions))
	for prop, id := range r.Exclusions {
		out = append(out, prop+"->"+id)
	}
	sort.Strings(out)
	return out
}
