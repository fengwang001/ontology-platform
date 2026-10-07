package ontology

import "fmt"

// ReferenceEngine 是独立维护的朴素参照实现：
// 使用扁平切片与线性扫描，不做任何索引优化，
// 用于在测试中与 Engine 的主实现逐项对照。
//
// 参照实现不维护审计日志，也不做并发控制；
// 它只保证与 DESIGN.md 中语义规范一致的可比对输出。
type ReferenceEngine struct {
	tenants   []string
	types     []ObjectTypeDef
	defaults  []referenceDefault  // 按类型名线性查找
	overrides []referenceOverride // 扁平列表，线性扫描
	instances []Instance
}

type referenceDefault struct {
	typeName string
	version  uint64
	entries  []RuleEntry
}

type referenceOverride struct {
	tenant   string
	typeName string
	version  uint64
	entries  []RuleEntry
}

// NewReferenceEngine 创建一个朴素的参照引擎。
func NewReferenceEngine() *ReferenceEngine {
	return &ReferenceEngine{}
}

func (r *ReferenceEngine) findTenant(id string) bool {
	for _, t := range r.tenants {
		if t == id {
			return true
		}
	}
	return false
}

func (r *ReferenceEngine) findType(name string) (ObjectTypeDef, bool) {
	for _, t := range r.types {
		if t.Name == name {
			return t, true
		}
	}
	return ObjectTypeDef{}, false
}

func (r *ReferenceEngine) findDefault(typeName string) *referenceDefault {
	for i := range r.defaults {
		if r.defaults[i].typeName == typeName {
			return &r.defaults[i]
		}
	}
	return nil
}

func (r *ReferenceEngine) findOverride(tenant, typeName string) *referenceOverride {
	for i := range r.overrides {
		if r.overrides[i].tenant == tenant && r.overrides[i].typeName == typeName {
			return &r.overrides[i]
		}
	}
	return nil
}

// RegisterTenant 参照实现：线性查重后追加。
func (r *ReferenceEngine) RegisterTenant(id string) error {
	if id == "" {
		return fmt.Errorf("tenant id must not be empty")
	}
	if r.findTenant(id) {
		return fmt.Errorf("tenant %q already registered", id)
	}
	r.tenants = append(r.tenants, id)
	return nil
}

// RegisterObjectType 参照实现：线性查重后追加。
func (r *ReferenceEngine) RegisterObjectType(def ObjectTypeDef) error {
	if def.Name == "" {
		return fmt.Errorf("object type name must not be empty")
	}
	if _, ok := r.findType(def.Name); ok {
		return fmt.Errorf("object type %q already registered", def.Name)
	}
	r.types = append(r.types, def)
	return nil
}

// RegisterInstance 参照实现：线性查找后追加。
func (r *ReferenceEngine) RegisterInstance(inst Instance) error {
	if _, ok := r.findType(inst.Type); !ok {
		return newError(ErrNotFound, "object type %q not found", inst.Type)
	}
	if !r.findTenant(inst.OwnerTenant) {
		return newError(ErrNotFound, "tenant %q not found", inst.OwnerTenant)
	}
	for _, in := range r.instances {
		if in.ID == inst.ID {
			return fmt.Errorf("instance %q already registered", inst.ID)
		}
	}
	r.instances = append(r.instances, inst)
	return nil
}

// SetGlobalDefault 参照实现：整体替换默认规则集。
func (r *ReferenceEngine) SetGlobalDefault(typeName string, entries []RuleEntry) error {
	if _, ok := r.findType(typeName); !ok {
		return newError(ErrNotFound, "object type %q not found", typeName)
	}
	deduped, err := refCheckConflict(entries)
	if err != nil {
		return err
	}
	d := r.findDefault(typeName)
	if d == nil {
		r.defaults = append(r.defaults, referenceDefault{typeName: typeName})
		d = &r.defaults[len(r.defaults)-1]
	}
	d.version++
	d.entries = deduped
	return nil
}

// SetTenantOverride 参照实现：线性扫描定位后整体替换。
func (r *ReferenceEngine) SetTenantOverride(tenant, typeName string, entries []RuleEntry) error {
	if !r.findTenant(tenant) {
		return newError(ErrNotFound, "tenant %q not found", tenant)
	}
	def, ok := r.findType(typeName)
	if !ok {
		return newError(ErrNotFound, "object type %q not found", typeName)
	}
	var defaults []RuleEntry
	if d := r.findDefault(typeName); d != nil {
		defaults = d.entries
	}
	if def.RequireRelaxationBasis {
		for _, e := range entries {
			if e.Basis == "" && refIsRelaxation(e, defaults) {
				return newError(ErrMissingBasis,
					"override relaxes the global default (action=%q attribute=%q predicate=%q role=%q) without an authorization basis",
					e.Action, e.Attribute, e.Predicate, e.Role)
			}
		}
	}
	deduped, err := refCheckConflict(entries)
	if err != nil {
		return err
	}
	o := r.findOverride(tenant, typeName)
	if o == nil {
		r.overrides = append(r.overrides, referenceOverride{tenant: tenant, typeName: typeName})
		o = &r.overrides[len(r.overrides)-1]
	}
	o.version++
	o.entries = deduped
	return nil
}

// RevokeTenantOverride 参照实现：线性扫描定位后删除。
func (r *ReferenceEngine) RevokeTenantOverride(tenant, typeName string) error {
	if !r.findTenant(tenant) {
		return newError(ErrNotFound, "tenant %q not found", tenant)
	}
	if _, ok := r.findType(typeName); !ok {
		return newError(ErrNotFound, "object type %q not found", typeName)
	}
	for i := range r.overrides {
		if r.overrides[i].tenant == tenant && r.overrides[i].typeName == typeName {
			r.overrides = append(r.overrides[:i], r.overrides[i+1:]...)
			break
		}
	}
	return nil
}

// Decide 参照实现：线性扫描全部相关条目后按规范裁决。
func (r *ReferenceEngine) Decide(subject Subject, action Action, instanceID, attribute string) (Decision, error) {
	var inst Instance
	found := false
	for _, in := range r.instances {
		if in.ID == instanceID {
			inst, found = in, true
			break
		}
	}
	if !found {
		return Decision{}, newError(ErrNotFound, "instance %q not found", instanceID)
	}
	def, ok := r.findType(inst.Type)
	if !ok {
		return Decision{}, newError(ErrNotFound, "object type %q not found", inst.Type)
	}
	if !r.findTenant(inst.OwnerTenant) {
		return Decision{}, newError(ErrNotFound, "tenant %q not found", inst.OwnerTenant)
	}
	attrOK := false
	for _, a := range def.Attributes {
		if a == attribute {
			attrOK = true
			break
		}
	}
	if !attrOK {
		return Decision{}, newError(ErrNotFound, "attribute %q not defined on object type %q", attribute, inst.Type)
	}

	basis := DecisionBasis{OwnerTenant: inst.OwnerTenant}
	var defEntries, ovrEntries []RuleEntry
	if d := r.findDefault(inst.Type); d != nil {
		defEntries = d.entries
		basis.DefaultVersion = d.version
		basis.DefaultsExamined = len(d.entries)
	}
	// 归属方向：只扫描实例归属租户的覆盖规则。
	if o := r.findOverride(inst.OwnerTenant, inst.Type); o != nil {
		ovrEntries = o.entries
		basis.OverrideVersion = o.version
		basis.OverridesExamined = len(o.entries)
	}

	// 合并：默认条目只要被任一覆盖条目完全包含即被替代。
	var merged []layeredEntry
	for _, d := range defEntries {
		replaced := false
		for _, o := range ovrEntries {
			if refSubsumes(o, d) {
				replaced = true
				break
			}
		}
		if replaced {
			basis.DefaultsSubsumed++
		} else {
			merged = append(merged, layeredEntry{layer: LayerDefault, entry: d})
		}
	}
	for _, o := range ovrEntries {
		merged = append(merged, layeredEntry{layer: LayerOverride, entry: o})
	}

	// 过滤适用条目。
	var apps []layeredEntry
	for _, le := range merged {
		if refApplicable(le.entry, def, inst, subject, action, attribute) {
			apps = append(apps, le)
		}
	}
	// 最高具体度：没有被任何其他适用条目严格更具体的条目。
	var maximal []layeredEntry
	for i, e := range apps {
		shadowed := false
		for j, f := range apps {
			if i != j && refSubsumes(e.entry, f.entry) && !refSameKey(e.entry, f.entry) {
				shadowed = true
				break
			}
		}
		if !shadowed {
			maximal = append(maximal, e)
		}
	}

	decision := Decision{Basis: basis}
	if len(maximal) == 0 {
		decision.Effect = Deny
		return decision, nil
	}
	uniform := true
	for _, m := range maximal[1:] {
		if m.entry.Effect != maximal[0].entry.Effect {
			uniform = false
			break
		}
	}
	if !uniform {
		allOverride := true
		for _, m := range maximal {
			if m.layer != LayerOverride {
				allOverride = false
				break
			}
		}
		if allOverride {
			return Decision{}, newError(ErrOverrideConflict,
				"override rules of tenant %q on type %q are irreconcilable for attribute %q",
				inst.OwnerTenant, inst.Type, attribute)
		}
		return Decision{}, newError(ErrMergeNonUnique,
			"merge of default and override rules on type %q is not unique for attribute %q",
			inst.Type, attribute)
	}
	decision.Effect = maximal[0].entry.Effect
	for _, m := range maximal {
		decision.Basis.WinningEntries = append(decision.Basis.WinningEntries, WinningEntry{Layer: m.layer, Entry: m.entry})
	}
	return decision, nil
}

// 以下是参照实现独立编写的规范辅助函数（ref 前缀），
// 与主实现的 merge.go 不共享代码，以便差分对照能发现实现层面的分歧。

func refSubsumes(a, b RuleEntry) bool {
	if a.Action != b.Action {
		return false
	}
	if a.Role != "" && a.Role != b.Role {
		return false
	}
	if a.Attribute != "" && a.Attribute != b.Attribute {
		return false
	}
	if a.Predicate != "" && a.Predicate != b.Predicate {
		return false
	}
	return true
}

func refSameKey(a, b RuleEntry) bool {
	return a.Action == b.Action && a.Role == b.Role &&
		a.Attribute == b.Attribute && a.Predicate == b.Predicate
}

func refOverlap(a, b RuleEntry) bool {
	if a.Action != b.Action {
		return false
	}
	if a.Role != "" && b.Role != "" && a.Role != b.Role {
		return false
	}
	if a.Attribute != "" && b.Attribute != "" && a.Attribute != b.Attribute {
		return false
	}
	if a.Predicate != "" && b.Predicate != "" && a.Predicate != b.Predicate {
		return false
	}
	return true
}

func refIsRelaxation(o RuleEntry, defaults []RuleEntry) bool {
	if o.Effect != Allow {
		return false
	}
	coveredAllow := false
	touchesDeny := false
	for _, d := range defaults {
		if d.Effect == Allow && refSubsumes(d, o) {
			coveredAllow = true
		}
		if d.Effect == Deny && refOverlap(o, d) {
			touchesDeny = true
		}
	}
	return !coveredAllow || touchesDeny
}

func refCheckConflict(entries []RuleEntry) ([]RuleEntry, error) {
	var out []RuleEntry
	for _, e := range entries {
		dup := false
		for _, seen := range out {
			if refSameKey(seen, e) {
				if seen.Effect != e.Effect {
					return nil, newError(ErrOverrideConflict,
						"rule set declares conflicting effects (action=%q attribute=%q predicate=%q role=%q)",
						e.Action, e.Attribute, e.Predicate, e.Role)
				}
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out, nil
}

func refApplicable(e RuleEntry, def ObjectTypeDef, inst Instance, subj Subject, action Action, attribute string) bool {
	if e.Action != action {
		return false
	}
	if e.Role != "" {
		has := false
		for _, r := range subj.Roles {
			if r == e.Role {
				has = true
				break
			}
		}
		if !has {
			return false
		}
	}
	if e.Attribute != "" && e.Attribute != attribute {
		return false
	}
	if e.Predicate != "" {
		p, ok := def.Predicates[e.Predicate]
		if !ok || !p(inst, subj) {
			return false
		}
	}
	return true
}
