package ontology

import (
	"context"
	"sync"
)

// ChangeEvent 是继承链/规则变更的只增审计事件（每次成功变更一条）。
// 它让外部可以重建任意序号时刻的规则状态，用于验证并发交错下导出固化的发起时刻语义。
type ChangeEvent struct {
	Seq           int64
	Kind          string // "put_rule" | "delete_rule" | "set_parents" | "create_type" | "create_subject"
	TypeID        string
	ParentIDs     []string
	DeclaringType string
	Subject       string
	Attribute     string
	VersionID     string
	Effect        Effect
}

// Platform 是本体平台的权限固化导出与审计追溯入口。
// 所有方法在单一互斥锁下串行化，因此并发交错严格等价于某个全局串行顺序。
type Platform struct {
	mu       sync.Mutex
	seq      int64
	types    map[string]*typeNode
	subjects map[string]struct{}
	rules    map[ruleKey]string
	archive  *ruleArchive
	exports  map[string]*ExportRecord
	events   []ChangeEvent
	logger   DecisionLogger
	exportN  int64
}

type ruleKey struct {
	declaringType string
	subject       string
	attribute     string
}

type typeNode struct {
	id      string
	parents []string
}

// New 创建平台实例。
func New() *Platform {
	return &Platform{
		types:    map[string]*typeNode{},
		subjects: map[string]struct{}{},
		rules:    map[ruleKey]string{},
		archive:  newRuleArchive(),
		exports:  map[string]*ExportRecord{},
		logger:   nopLogger{},
	}
}

// WithLogger 返回配置了判定日志器的平台。
func (p *Platform) WithLogger(l DecisionLogger) *Platform {
	if l == nil {
		l = nopLogger{}
	}
	p.logger = l
	return p
}

func (p *Platform) nextSeqLocked() int64 {
	p.seq++
	return p.seq
}

func (p *Platform) appendEventLocked(e ChangeEvent) {
	p.events = append(p.events, e)
}

// Events 返回变更事件日志的只读副本（序号单调）。
func (p *Platform) Events() []ChangeEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ChangeEvent, len(p.events))
	copy(out, p.events)
	return out
}

// Seq 返回平台当前全局版本序号（只读观测点，测试可用于发起时刻断言）。
func (p *Platform) Seq() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seq
}

// ensureParentsLocked 校验父类型全部存在且不存在继承环。
func (p *Platform) ensureParentsLocked(id string, parentIDs []string) error {
	saved, hadSaved := p.types[id]
	probe := &typeNode{id: id, parents: append([]string(nil), parentIDs...)}
	p.types[id] = probe
	chain, ok := p.linearizeLocked(id)
	if hadSaved {
		p.types[id] = saved
	} else {
		delete(p.types, id)
	}
	if !ok {
		return newError(KindObjectTypeNotFound, "unknown parent or inheritance cycle while setting parents for type: "+id)
	}
	_ = chain
	return nil
}

// dedupPreserveOrder 返回去重后的属性列表，保留首次出现顺序。
func dedupPreserveOrder(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// CreateType 注册对象类型并声明其直接父类型（有序、可空）。
func (p *Platform) CreateType(ctx context.Context, id string, parentIDs []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.types[id]; ok {
		return newError(KindObjectTypeNotFound, "type already exists: "+id)
	}
	if err := p.ensureParentsLocked(id, parentIDs); err != nil {
		return err
	}
	p.types[id] = &typeNode{id: id, parents: append([]string(nil), parentIDs...)}
	seq := p.nextSeqLocked()
	p.appendEventLocked(ChangeEvent{Seq: seq, Kind: "create_type", TypeID: id, ParentIDs: append([]string(nil), parentIDs...)})
	return nil
}

// CreateSubject 注册执行主体。
func (p *Platform) CreateSubject(ctx context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.subjects[id]; ok {
		return newError(KindSubjectNotFound, "subject already exists: "+id)
	}
	p.subjects[id] = struct{}{}
	seq := p.nextSeqLocked()
	p.appendEventLocked(ChangeEvent{Seq: seq, Kind: "create_subject", Subject: id})
	return nil
}

// PutRule 在 declaringType 上声明（新增或覆盖）一条权限规则，返回新版本标识。
func (p *Platform) PutRule(ctx context.Context, declaringType, subject, attribute string, effect Effect) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.types[declaringType]; !ok {
		return "", newError(KindObjectTypeNotFound, "declaring type does not exist: "+declaringType)
	}
	if _, ok := p.subjects[subject]; !ok {
		return "", newError(KindSubjectNotFound, "subject does not exist: "+subject)
	}
	if effect != EffectAllow && effect != EffectDeny {
		return "", newError(KindObjectTypeNotFound, "unknown effect: "+string(effect))
	}
	seq := p.nextSeqLocked()
	ruleID := ruleIdentity(declaringType, subject, attribute)
	version := p.archive.seal(ruleID, declaringType, subject, attribute, effect, seq)
	p.rules[ruleKey{declaringType: declaringType, subject: subject, attribute: attribute}] = version.VersionID
	p.appendEventLocked(ChangeEvent{
		Seq: seq, Kind: "put_rule", DeclaringType: declaringType, Subject: subject,
		Attribute: attribute, VersionID: version.VersionID, Effect: effect,
	})
	return version.VersionID, nil
}

// DeleteRule 删除 declaringType 上直接声明的规则（覆盖删除回落）。
func (p *Platform) DeleteRule(ctx context.Context, declaringType, subject, attribute string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.types[declaringType]; !ok {
		return false, newError(KindObjectTypeNotFound, "declaring type does not exist: "+declaringType)
	}
	key := ruleKey{declaringType: declaringType, subject: subject, attribute: attribute}
	if _, ok := p.rules[key]; !ok {
		return false, nil
	}
	delete(p.rules, key)
	seq := p.nextSeqLocked()
	p.appendEventLocked(ChangeEvent{
		Seq: seq, Kind: "delete_rule", DeclaringType: declaringType, Subject: subject, Attribute: attribute,
	})
	return true, nil
}

// SetParents 重组类型的继承关系。
func (p *Platform) SetParents(ctx context.Context, id string, parentIDs []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	node, ok := p.types[id]
	if !ok {
		return newError(KindObjectTypeNotFound, "type does not exist: "+id)
	}
	if err := p.ensureParentsLocked(id, parentIDs); err != nil {
		return err
	}
	node.parents = append([]string(nil), parentIDs...)
	seq := p.nextSeqLocked()
	p.appendEventLocked(ChangeEvent{Seq: seq, Kind: "set_parents", TypeID: id, ParentIDs: append([]string(nil), parentIDs...)})
	return nil
}

// ExportRequest 是快照导出请求。
type ExportRequest struct {
	TypeID     string
	ObjectID   string
	SubjectID  string
	Attributes []string
	Values     map[string]any
	ExportID   string
}

// ExclusionRecord 是被排除属性的不可变排除依据记录。
type ExclusionRecord struct {
	Attribute     string
	VersionID     string
	RuleID        string
	DeclaringType string
	Effect        Effect
}

// ExportRecord 是一次已完成导出的不可变快照。
type ExportRecord struct {
	ExportID     string
	ObjectID     string
	TypeID       string
	SubjectID    string
	Seq          int64
	Included     []string
	Values       map[string]any
	Excluded     []*ExclusionRecord
	excludedByID map[string]*ExclusionRecord
}

// Export 发起一次快照导出。
// 拒绝优先级：对象类型不存在 > 执行主体不存在；其余属性逐一判定，不视为错误。
// 导出全程持锁，其固化依据严格对应全局串行顺序下“发起导出那一刻”的规则状态。
func (p *Platform) Export(ctx context.Context, req ExportRequest) (*ExportRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.types[req.TypeID]; !ok {
		return nil, newError(KindObjectTypeNotFound, "object type does not exist: "+req.TypeID)
	}
	if _, ok := p.subjects[req.SubjectID]; !ok {
		return nil, newError(KindSubjectNotFound, "subject does not exist: "+req.SubjectID)
	}
	chain, ok := p.linearizeLocked(req.TypeID)
	if !ok {
		return nil, newError(KindObjectTypeNotFound, "inheritance chain unresolved (cycle or dangling parent): "+req.TypeID)
	}

	seq := p.nextSeqLocked()
	p.exportN++
	exportID := req.ExportID
	if exportID == "" {
		exportID = "export-" + itoa(p.exportN)
	}
	if _, dup := p.exports[exportID]; dup {
		return nil, newError(KindExportNotFound, "export id already exists: "+exportID)
	}

	attrs := dedupPreserveOrder(req.Attributes)
	record := &ExportRecord{
		ExportID:     exportID,
		ObjectID:     req.ObjectID,
		TypeID:       req.TypeID,
		SubjectID:    req.SubjectID,
		Seq:          seq,
		Values:       map[string]any{},
		Excluded:     []*ExclusionRecord{},
		excludedByID: map[string]*ExclusionRecord{},
	}

	for _, attr := range attrs {
		var hit *RuleVersion
		for _, typeID := range chain {
			if versionID, ok := p.rules[ruleKey{declaringType: typeID, subject: req.SubjectID, attribute: attr}]; ok {
				v, resolved := p.archive.resolve(versionID)
				if !resolved {
					// 档案只增不改，理论不可达；失败时安全拒绝并保持可审计。
					return nil, newError(KindExportNotFound, "internal: sealed rule version missing: "+versionID)
				}
				hit = v
				break // 最近声明优先，唯一命中点
			}
		}

		if hit != nil && hit.Effect == EffectDeny {
			excl := &ExclusionRecord{
				Attribute:     attr,
				VersionID:     hit.VersionID,
				RuleID:        hit.RuleID,
				DeclaringType: hit.DeclaringType,
				Effect:        hit.Effect,
			}
			record.Excluded = append(record.Excluded, excl)
			record.excludedByID[attr] = excl
			p.logger.LogDecision(ctx, DecisionLogRecord{
				ExportID:      exportID,
				Seq:           seq,
				Subject:       req.SubjectID,
				TypeID:        req.TypeID,
				Attribute:     attr,
				Decision:      "EXCLUDE",
				HitVersionID:  hit.VersionID,
				HitRuleID:     hit.RuleID,
				DeclaringType: hit.DeclaringType,
				Effect:        string(hit.Effect),
			})
			continue
		}

		// 默认策略 ALLOW：无规则或命中 ALLOW 均纳入导出。
		record.Included = append(record.Included, attr)
		if value, has := req.Values[attr]; has {
			record.Values[attr] = value
		}
		decision := "INCLUDE"
		var versionID, ruleID, declaringType, effect string
		if hit != nil {
			versionID, ruleID, declaringType, effect = hit.VersionID, hit.RuleID, hit.DeclaringType, string(hit.Effect)
		}
		p.logger.LogDecision(ctx, DecisionLogRecord{
			ExportID:      exportID,
			Seq:           seq,
			Subject:       req.SubjectID,
			TypeID:        req.TypeID,
			Attribute:     attr,
			Decision:      decision,
			HitVersionID:  versionID,
			HitRuleID:     ruleID,
			DeclaringType: declaringType,
			Effect:        effect,
		})
	}

	// 不可变化：保存 Included 副本并清空可变请求别名。
	record.Included = append([]string(nil), record.Included...)
	p.exports[exportID] = record
	return p.cloneExportLocked(record), nil
}

func (p *Platform) cloneExportLocked(r *ExportRecord) *ExportRecord {
	cp := *r
	cp.Included = append([]string(nil), r.Included...)
	cp.Excluded = append([]*ExclusionRecord(nil), r.Excluded...)
	if r.Values != nil {
		cp.Values = make(map[string]any, len(r.Values))
		for k, v := range r.Values {
			cp.Values[k] = v
		}
	}
	return &cp
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TraceResult 是审计溯源结果：唯一确定命中点及其当时内容快照。
type TraceResult struct {
	ExportID  string
	Attribute string
	VersionID string
	RuleID    string
	Rule      *RuleVersion
}

// Trace 回答某次历史导出当时为何排除了某属性。
// 拒绝优先级：历史导出记录不存在 > 属性在该次导出中未被排除。
// Trace 为只读查询：不加写序号、不修改任何状态，天然幂等且结果可重复。
func (p *Platform) Trace(_ context.Context, exportID, attribute string) (*TraceResult, error) {
	p.mu.Lock()
	record, ok := p.exports[exportID]
	if !ok {
		p.mu.Unlock()
		return nil, newError(KindExportNotFound, "historical export record does not exist: "+exportID)
	}
	excl, excluded := record.excludedByID[attribute]
	p.mu.Unlock()
	if !excluded {
		return nil, newError(KindAttributeNotExcluded,
			"attribute was not excluded in export "+exportID+": "+attribute)
	}
	rule, resolved := p.archive.resolve(excl.VersionID)
	if !resolved {
		return nil, newError(KindExportNotFound, "internal: sealed rule version cannot be resolved: "+excl.VersionID)
	}
	return &TraceResult{
		ExportID:  exportID,
		Attribute: attribute,
		VersionID: excl.VersionID,
		RuleID:    excl.RuleID,
		Rule:      rule,
	}, nil
}

// GetExport 只读返回某次历史导出的不可变快照副本（不存在返回 KindExportNotFound）。
func (p *Platform) GetExport(_ context.Context, exportID string) (*ExportRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.exports[exportID]
	if !ok {
		return nil, newError(KindExportNotFound, "historical export record does not exist: "+exportID)
	}
	return p.cloneExportLocked(record), nil
}
