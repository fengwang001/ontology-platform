// Package ontology 实现单继承对象类型体系下属性取值规则的遮蔽与穿透。
//
// 核心语义见 DESIGN.md：实例绑定具体类型，规则自该类型沿单继承链条向上取
// 第一个显式声明者；重新声明在声明时强制允许集合子集约束；密封类型禁止派生；
// 仍有子类型依赖的类型禁止删除；历史取值不被追溯重验；所有操作经统一锁与
// 单调序号保证可线性化；每次查找通过 TraceRecord 暴露遍历链条与命中来源。
package ontology

import (
	"fmt"
	"sync"
)

// Value 是属性取值的原子单位。取值规则以“允许取值集合”表达。
type Value string

// ValueSet 是允许取值的集合。
type ValueSet map[Value]struct{}

// Rule 是一个属性取值规则：允许写入的取值集合。
type Rule struct {
	// RuleID 唯一标识一次具体的声明（原始定义或某次重新声明），
	// 用于并发场景下区分“同一允许集合内容、不同声明时刻”的规则。
	RuleID int64
	// Allowed 为该规则允许的取值集合。
	Allowed ValueSet
}

// TraceNode 记录一次规则查找在继承链条上访问的单个类型节点。
type TraceNode struct {
	// Type 为被访问的具体类型名称（从实例绑定的具体类型开始向上）。
	Type string
	// Hit 表示该类型是否对该属性存在显式声明（原始定义或重新声明）。
	Hit bool
	// Original 表示该命中是否为属性最初定义处的祖先类型。
	Original bool
}

// Resolution 是一次“具体类型 + 属性”的最终生效规则判定结果。
type Resolution struct {
	// Property 为被查询的属性名。
	Property string
	// SourceType 为链条上第一个显式声明该属性规则的类型（命中来源）。
	SourceType string
	// OriginType 为最初定义该属性的祖先类型。
	OriginType string
	// Rule 为最终生效规则。
	Rule Rule
	// Chain 为本次查找实际遍历的链条（自具体类型向上，到命中节点为止）。
	Chain []TraceNode
}

// TraceRecord 是一次查找/读/写的完整审计记录，供事后核对链条、命中来源与最终规则。
type TraceRecord struct {
	// Op 为操作类型：resolve / read / write。
	Op string
	// Instance 为实例名（resolve 时为空）。
	Instance string
	// ConcreteType 为查找起点的具体类型。
	ConcreteType string
	Resolution   Resolution
	// Value 为 write 操作尝试写入的值（其他操作为空）。
	Value Value
	// Accepted 表示 write 的取值是否通过当前生效规则校验。
	Accepted bool
	// Seq 为该操作在全序中的序号（互斥锁内分配，即线性化点）。
	Seq int64
}

// HistoryEntry 记录实例某属性的一次历史写入。
type HistoryEntry struct {
	Value  Value
	RuleID int64 // 该次写入校验时生效的规则
	Seq    int64 // 写入操作的全序序号
}

// Ontology 是单继承对象类型体系与其实例的容器。
// 所有变更与查询经由同一把读写锁串行化，保证并发操作可线性化。
type Ontology struct {
	mu        sync.RWMutex
	types     map[string]*objectType
	instances map[string]*instance
	// auditMu 仅保护 audit/nextSeq：记录追加必须同时对持 RLock 的读操作互斥，
	// 固定加锁顺序为 mu -> auditMu（持 mu 后再获取 auditMu），避免死锁。
	auditMu  sync.Mutex
	audit    []TraceRecord
	nextRule int64
	nextSeq  int64
}

type objectType struct {
	name     string
	parent   *objectType
	sealed   bool
	children map[string]*objectType
	// props 仅记录本类型自身显式声明过的属性（原始定义或重新声明）。
	// 未重新声明的继承属性不在此 map 中，沿链条向上查找。
	props map[string]*propState
}

type propState struct {
	origin string // 最初定义该属性的祖先类型名
	rule   Rule   // 本类型处声明的规则
}

type instance struct {
	name   string
	typ    *objectType
	values map[string][]HistoryEntry // 按属性保存全部历史写入（不追溯、不修改）
}

// New 创建一个空的本体。
func New() *Ontology {
	return &Ontology{
		types:     map[string]*objectType{},
		instances: map[string]*instance{},
	}
}

// CreateRootType 创建一个没有父类型的根类型。
func (o *Ontology) CreateRootType(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.types[name]; ok {
		return fmt.Errorf("ontology: type %q already exists", name)
	}
	o.types[name] = &objectType{
		name:     name,
		children: map[string]*objectType{},
		props:    map[string]*propState{},
	}
	return nil
}

// CreateSubtype 在 parent 下创建名为 name 的子类型；sealed 为 true 时该类型不允许再派生。
func (o *Ontology) CreateSubtype(name, parent string, sealed bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	// 不存在判定优先：父类型必须确实存在。
	p, ok := o.types[parent]
	if !ok {
		return ErrNotFound
	}
	if _, ok := o.types[name]; ok {
		return fmt.Errorf("ontology: type %q already exists", name)
	}
	// 父类型不允许派生时，在创建子类型的当下即拒绝。
	if p.sealed {
		return ErrSealedParent
	}
	t := &objectType{
		name:     name,
		parent:   p,
		sealed:   sealed,
		children: map[string]*objectType{},
		props:    map[string]*propState{},
	}
	p.children[name] = t
	o.types[name] = t
	return nil
}

// MarkSealed 将一个已有类型标记为不允许派生。
func (o *Ontology) MarkSealed(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.types[name]
	if !ok {
		return ErrNotFound
	}
	t.sealed = true
	return nil
}

// DefineProperty 在 typeName 上原始定义一个新属性及其取值规则。
// 该类型的继承链条上不得已存在同名属性。
func (o *Ontology) DefineProperty(typeName, property string, allowed []Value) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.types[typeName]
	if !ok {
		return ErrNotFound
	}
	// 链条上已存在同名属性则不允许“重复定义”。
	if _, _, exists := o.findDeclaration(t, property); exists {
		return fmt.Errorf("ontology: property %q already defined on the type chain of %q", property, typeName)
	}
	t.props[property] = &propState{
		origin: typeName,
		rule:   Rule{RuleID: o.allocRuleIDLocked(), Allowed: toValueSet(allowed)},
	}
	return nil
}

// RedeclareProperty 在 typeName 上对继承属性重新声明取值规则。
// 新允许集合必须是当前生效规则允许集合的子集（允许相等），否则返回 ErrValueSetWidened。
func (o *Ontology) RedeclareProperty(typeName, property string, allowed []Value) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.types[typeName]
	if !ok {
		return ErrNotFound
	}
	// 不存在判定优先：必须先确认链条上确实存在该属性。
	var chain []TraceNode
	res, ok := o.resolveLocked(t, property, &chain)
	if !ok {
		return ErrNotFound
	}
	newSet := toValueSet(allowed)
	// 声明时即做子集检查：不允许扩大允许取值集合。
	if !isSubset(newSet, res.Rule.Allowed) {
		return ErrValueSetWidened
	}
	newRule := Rule{RuleID: o.allocRuleIDLocked(), Allowed: newSet}
	t.props[property] = &propState{
		origin: res.OriginType,
		rule:   newRule,
	}
	// 记录一次成功的重新声明（含全序序号），供并发交织后与朴素串行实现对照。
	newRes := Resolution{
		Property:   property,
		SourceType: typeName,
		OriginType: res.OriginType,
		Rule:       newRule,
		Chain:      []TraceNode{{Type: typeName, Hit: true, Original: false}},
	}
	o.appendRecordLocked(TraceRecord{
		Op:           "redeclare",
		ConcreteType: typeName,
		Resolution:   newRes,
	})
	return nil
}

// DeleteType 删除一个类型；仍存在直接子类型时返回 ErrTypeHasChildren。
func (o *Ontology) DeleteType(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.types[name]
	if !ok {
		return ErrNotFound
	}
	// 中间类型（仍有直接子类型依赖）不允许物理删除，防止查找链条断裂。
	if len(t.children) > 0 {
		return ErrTypeHasChildren
	}
	if t.parent != nil {
		delete(t.parent.children, name)
	}
	delete(o.types, name)
	return nil
}

// Resolve 判定具体类型 typeName 的属性 property 的最终生效规则，并记录遍历链条。
func (o *Ontology) Resolve(typeName, property string) (Resolution, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	t, ok := o.types[typeName]
	if !ok {
		return Resolution{}, ErrNotFound
	}
	rec, ok := o.resolveAndRecordLocked("resolve", "", t, property)
	if !ok {
		return Resolution{}, ErrNotFound
	}
	return rec.Resolution, nil
}

// CreateInstance 创建一个绑定到具体类型的实例。
func (o *Ontology) CreateInstance(name, typeName string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.types[typeName]
	if !ok {
		return ErrNotFound
	}
	if _, ok := o.instances[name]; ok {
		return fmt.Errorf("ontology: instance %q already exists", name)
	}
	o.instances[name] = &instance{
		name:   name,
		typ:    t,
		values: map[string][]HistoryEntry{},
	}
	return nil
}

// WriteResult 返回一次写入校验所使用的生效规则信息。
type WriteResult struct {
	Rule   Rule
	Source string
	Seq    int64
}

// Write 按当前生效规则校验并写入实例属性；历史写入不被追溯修改。
func (o *Ontology) Write(instanceName, property string, value Value) (WriteResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	inst, ok := o.instances[instanceName]
	if !ok {
		return WriteResult{}, ErrNotFound
	}
	rec, ok := o.resolveAndRecordLocked("write", instanceName, inst.typ, property)
	if !ok {
		return WriteResult{}, ErrNotFound
	}
	// 仅新写入按当前生效规则校验；历史取值不被追溯重验。
	if _, allowed := rec.Resolution.Rule.Allowed[value]; !allowed {
		rec.Value = value
		o.updateLastRecordLocked(rec)
		return WriteResult{}, fmt.Errorf("ontology: value %q not allowed by current rule of %q.%q", value, inst.typ.name, property)
	}
	inst.values[property] = append(inst.values[property], HistoryEntry{
		Value:  value,
		RuleID: rec.Resolution.Rule.RuleID,
		Seq:    rec.Seq,
	})
	rec.Value = value
	rec.Accepted = true
	o.updateLastRecordLocked(rec)
	return WriteResult{Rule: rec.Resolution.Rule, Source: rec.Resolution.SourceType, Seq: rec.Seq}, nil
}

// ReadResult 返回历史取值与读取时刻的生效规则（仅用于审计，不参与校验）。
type ReadResult struct {
	Value Value
	// Exists 表示该属性是否曾被写入过。
	Exists     bool
	Resolution Resolution
	Seq        int64
}

// Read 读取实例某属性最近一次历史取值；读取始终成功，不因当前规则收紧而拒绝读取。
func (o *Ontology) Read(instanceName, property string) (ReadResult, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	inst, ok := o.instances[instanceName]
	if !ok {
		return ReadResult{}, ErrNotFound
	}
	rec, ok := o.resolveAndRecordLocked("read", instanceName, inst.typ, property)
	if !ok {
		return ReadResult{}, ErrNotFound
	}
	result := ReadResult{Resolution: rec.Resolution, Seq: rec.Seq}
	if hist := inst.values[property]; len(hist) > 0 {
		// 历史取值原样返回：当前生效规则即使已收紧也不拒绝读取。
		result.Exists = true
		result.Value = hist[len(hist)-1].Value
	}
	return result, nil
}

// Audit 返回截至目前全部查找/读/写审计记录的副本。
func (o *Ontology) Audit() []TraceRecord {
	o.mu.RLock()
	defer o.mu.RUnlock()
	o.auditMu.Lock()
	defer o.auditMu.Unlock()
	out := make([]TraceRecord, len(o.audit))
	copy(out, o.audit)
	return out
}

// ---- 内部实现（调用方负责持锁） ----

func (o *Ontology) allocRuleIDLocked() int64 {
	o.nextRule++
	return o.nextRule
}

// appendRecordLocked 追加一条审计记录并分配全序序号。调用方必须已持有 mu（读或写），
// 本方法额外获取 auditMu，使读操作之间在审计点上也相互串行——该点即操作的线性化点。
func (o *Ontology) appendRecordLocked(rec TraceRecord) TraceRecord {
	o.auditMu.Lock()
	defer o.auditMu.Unlock()
	o.nextSeq++
	rec.Seq = o.nextSeq
	o.audit = append(o.audit, rec)
	return rec
}

// updateLastRecordLocked 在写入校验得出接受/拒绝结论后补全最后一条审计记录。
func (o *Ontology) updateLastRecordLocked(rec TraceRecord) {
	o.auditMu.Lock()
	defer o.auditMu.Unlock()
	o.audit[len(o.audit)-1] = rec
}

// findDeclaration 自 start 沿单继承链条向上，返回第一个显式声明 property 的类型与其声明。
func (o *Ontology) findDeclaration(start *objectType, property string) (*objectType, *propState, bool) {
	for cur := start; cur != nil; cur = cur.parent {
		if ps, ok := cur.props[property]; ok {
			return cur, ps, true
		}
	}
	return nil, nil, false
}

// resolveLocked 仅做规则判定；chainOut 非 nil 时填充实际遍历的链条（含命中节点）。
func (o *Ontology) resolveLocked(start *objectType, property string, chainOut *[]TraceNode) (Resolution, bool) {
	var (
		source     *objectType
		sourceProp *propState
	)
	for cur := start; cur != nil; cur = cur.parent {
		if ps, ok := cur.props[property]; ok {
			if chainOut != nil {
				*chainOut = append(*chainOut, TraceNode{
					Type: cur.name, Hit: true, Original: ps.origin == cur.name,
				})
			}
			source, sourceProp = cur, ps
			break
		}
		if chainOut != nil {
			*chainOut = append(*chainOut, TraceNode{Type: cur.name})
		}
	}
	if source == nil {
		return Resolution{}, false
	}
	return Resolution{
		Property:   property,
		SourceType: source.name,
		OriginType: sourceProp.origin,
		Rule:       sourceProp.rule,
	}, true
}

// resolveAndRecordLocked 执行判定、构建完整链条并追加一条审计记录，返回带全序序号的记录。
func (o *Ontology) resolveAndRecordLocked(op, instanceName string, start *objectType, property string) (TraceRecord, bool) {
	var chain []TraceNode
	res, ok := o.resolveLocked(start, property, &chain)
	if !ok {
		return TraceRecord{}, false
	}
	res.Chain = chain
	rec := o.appendRecordLocked(TraceRecord{
		Op:           op,
		Instance:     instanceName,
		ConcreteType: start.name,
		Resolution:   res,
	})
	return rec, true
}

func toValueSet(values []Value) ValueSet {
	set := make(ValueSet, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

// isSubset 判断 sub 是否为 super 的子集（相等也返回 true）。
func isSubset(sub, super ValueSet) bool {
	for v := range sub {
		if _, ok := super[v]; !ok {
			return false
		}
	}
	return true
}
