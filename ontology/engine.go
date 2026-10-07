package ontology

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// Engine 是多租户命名空间权限继承覆盖模块的主实现。
//
// 并发语义：所有导出的调用都在同一把互斥锁下串行化，
// 因此任意并发调用的结果必然等价于某个串行执行顺序（可线性化）。
type Engine struct {
	mu        sync.Mutex
	tenants   map[string]struct{}
	types     map[string]ObjectTypeDef
	defaults  map[string]*defaultRules
	overrides map[string]map[string]*overrideRules // tenant -> objectType -> rules
	instances map[string]Instance
	audit     []AuditRecord
}

type defaultRules struct {
	version uint64
	entries []RuleEntry
}

type overrideRules struct {
	version uint64
	entries []RuleEntry
}

// NewEngine 创建一个空引擎。
func NewEngine() *Engine {
	return &Engine{
		tenants:   make(map[string]struct{}),
		types:     make(map[string]ObjectTypeDef),
		defaults:  make(map[string]*defaultRules),
		overrides: make(map[string]map[string]*overrideRules),
		instances: make(map[string]Instance),
	}
}

// RegisterTenant 注册一个租户命名空间。
func (e *Engine) RegisterTenant(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return fmt.Errorf("tenant id must not be empty")
	}
	if _, ok := e.tenants[id]; ok {
		return fmt.Errorf("tenant %q already registered", id)
	}
	e.tenants[id] = struct{}{}
	e.appendAudit(OpRegisterTenant, id, nil)
	return nil
}

// RegisterObjectType 在全局命名空间注册一个对象类型。
func (e *Engine) RegisterObjectType(def ObjectTypeDef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if def.Name == "" {
		return fmt.Errorf("object type name must not be empty")
	}
	if _, ok := e.types[def.Name]; ok {
		return fmt.Errorf("object type %q already registered", def.Name)
	}
	e.types[def.Name] = def
	e.appendAudit(OpRegisterObjectType, def.Name, nil)
	return nil
}

// RegisterInstance 登记一个归属某租户的实例。
func (e *Engine) RegisterInstance(inst Instance) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.types[inst.Type]; !ok {
		return newError(ErrNotFound, "object type %q not found", inst.Type)
	}
	if _, ok := e.tenants[inst.OwnerTenant]; !ok {
		return newError(ErrNotFound, "tenant %q not found", inst.OwnerTenant)
	}
	if _, ok := e.instances[inst.ID]; ok {
		return fmt.Errorf("instance %q already registered", inst.ID)
	}
	e.instances[inst.ID] = inst
	e.appendAudit(OpRegisterInstance, inst.ID, nil)
	return nil
}

// SetGlobalDefault 原子地整体替换某对象类型的全局默认规则集。
//
// 替换在单次加锁内完成：未对变更部分声明覆盖的租户立即感知新默认规则，
// 已声明覆盖的租户继续沿用其覆盖规则，二者在同一时刻原子生效。
func (e *Engine) SetGlobalDefault(typeName string, entries []RuleEntry) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.types[typeName]; !ok {
		return newError(ErrNotFound, "object type %q not found", typeName)
	}
	deduped, err := checkInternalConflict(entries)
	if err != nil {
		return err
	}
	d := e.defaults[typeName]
	if d == nil {
		d = &defaultRules{}
		e.defaults[typeName] = d
	}
	d.version++
	d.entries = deduped
	e.appendAudit(OpSetGlobalDefault, setRulesInput{Type: typeName, Entries: deduped}, d.version)
	return nil
}

// SetTenantOverride 原子地整体替换某租户对某对象类型的覆盖规则集。
//
// 校验按固定优先顺序进行：先存在性，再放宽授权依据，最后声明内冲突。
// 被拒绝的声明不改变任何规则状态，也不写入审计日志。
func (e *Engine) SetTenantOverride(tenant, typeName string, entries []RuleEntry) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tenants[tenant]; !ok {
		return newError(ErrNotFound, "tenant %q not found", tenant)
	}
	def, ok := e.types[typeName]
	if !ok {
		return newError(ErrNotFound, "object type %q not found", typeName)
	}
	var defaults []RuleEntry
	if d := e.defaults[typeName]; d != nil {
		defaults = d.entries
	}
	if def.RequireRelaxationBasis {
		for _, entry := range entries {
			if entry.Basis == "" && isRelaxation(entry, defaults) {
				return newError(ErrMissingBasis,
					"override relaxes the global default (action=%q attribute=%q predicate=%q role=%q) without an authorization basis",
					entry.Action, entry.Attribute, entry.Predicate, entry.Role)
			}
		}
	}
	deduped, err := checkInternalConflict(entries)
	if err != nil {
		return err
	}
	byType := e.overrides[tenant]
	if byType == nil {
		byType = make(map[string]*overrideRules)
		e.overrides[tenant] = byType
	}
	o := byType[typeName]
	if o == nil {
		o = &overrideRules{}
		byType[typeName] = o
	}
	o.version++
	o.entries = deduped
	e.appendAudit(OpSetTenantOverride, setOverrideInput{Tenant: tenant, Type: typeName, Entries: deduped}, o.version)
	return nil
}

// RevokeTenantOverride 原子地撤销某租户对某对象类型的全部覆盖规则，
// 恢复为沿用全局默认规则。历史判定记录保持不变。
func (e *Engine) RevokeTenantOverride(tenant, typeName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tenants[tenant]; !ok {
		return newError(ErrNotFound, "tenant %q not found", tenant)
	}
	if _, ok := e.types[typeName]; !ok {
		return newError(ErrNotFound, "object type %q not found", typeName)
	}
	// 整个覆盖规则集在一次加锁内删除，不存在部分属性仍沿用旧覆盖、
	// 部分属性已恢复默认的中间状态。撤销未登记的覆盖是幂等空操作。
	if byType := e.overrides[tenant]; byType != nil {
		delete(byType, typeName)
	}
	e.appendAudit(OpRevokeOverride, setOverrideInput{Tenant: tenant, Type: typeName}, nil)
	return nil
}

// Decide 判定 subject 对 instanceID 的 attribute 执行 action 是否被允许。
// 判定依据实例归属租户的覆盖规则，而非主体归属租户。
func (e *Engine) Decide(subject Subject, action Action, instanceID, attribute string) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst, ok := e.instances[instanceID]
	if !ok {
		return Decision{}, newError(ErrNotFound, "instance %q not found", instanceID)
	}
	def, ok := e.types[inst.Type]
	if !ok {
		return Decision{}, newError(ErrNotFound, "object type %q not found", inst.Type)
	}
	if _, ok := e.tenants[inst.OwnerTenant]; !ok {
		return Decision{}, newError(ErrNotFound, "tenant %q not found", inst.OwnerTenant)
	}
	if !slices.Contains(def.Attributes, attribute) {
		return Decision{}, newError(ErrNotFound, "attribute %q not defined on object type %q", attribute, inst.Type)
	}

	// 归属方向：只取实例归属租户的覆盖规则，主体归属租户的覆盖规则不参与。
	var defaults *defaultRules
	if d := e.defaults[inst.Type]; d != nil {
		defaults = d
	}
	var overrides *overrideRules
	if byType := e.overrides[inst.OwnerTenant]; byType != nil {
		overrides = byType[inst.Type]
	}

	basis := DecisionBasis{OwnerTenant: inst.OwnerTenant}
	if defaults != nil {
		basis.DefaultVersion = defaults.version
		basis.DefaultsExamined = len(defaults.entries)
	}
	if overrides != nil {
		basis.OverrideVersion = overrides.version
		basis.OverridesExamined = len(overrides.entries)
	}

	merged, subsumed := mergeLayers(defaults, overrides)
	basis.DefaultsSubsumed = subsumed

	maximal := maximalApplicable(merged, def, inst, subject, action, attribute)
	decision := Decision{Basis: basis}
	switch {
	case len(maximal) == 0:
		// 封闭世界：无任何适用条目时默认拒绝。
		decision.Effect = Deny
	case uniformEffect(maximal):
		decision.Effect = maximal[0].entry.Effect
		decision.Basis.WinningEntries = toWinning(maximal)
	default:
		if allFromLayer(maximal, LayerOverride) {
			return Decision{}, newError(ErrOverrideConflict,
				"override rules of tenant %q on type %q are irreconcilable for attribute %q",
				inst.OwnerTenant, inst.Type, attribute)
		}
		return Decision{}, newError(ErrMergeNonUnique,
			"merge of default and override rules on type %q is not unique for attribute %q",
			inst.Type, attribute)
	}
	e.appendAudit(OpDecide, DecideInput{Subject: subject, Action: action, InstanceID: instanceID, Attribute: attribute}, decision)
	return decision, nil
}

// Audit 返回审计日志的副本（按提交顺序）。
func (e *Engine) Audit() []AuditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.audit)
}

// appendAudit 追加一条审计记录。调用方必须持有锁，且调用必须已被接受。
func (e *Engine) appendAudit(op OpKind, input, output any) {
	e.audit = append(e.audit, AuditRecord{
		Seq:    uint64(len(e.audit)) + 1,
		Op:     op,
		At:     time.Now(),
		Input:  input,
		Output: output,
	})
}

type setRulesInput struct {
	Type    string
	Entries []RuleEntry
}

type setOverrideInput struct {
	Tenant  string
	Type    string
	Entries []RuleEntry
}
