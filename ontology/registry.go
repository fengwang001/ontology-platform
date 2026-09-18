package ontology

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"
)

// MigrateFunc 把某个 ObjectType 的一个存量实例从旧形态迁移到新形态。
// 入参 instance 是只读的拷贝；返回迁移后的新实例。
// 返回 error 或 panic 都会导致整次演进回滚，版本号不前进。
//
// 迁移函数在注册表内部锁持有期间被调用，因此不得回调同一个 Registry。
type MigrateFunc func(instance map[string]any) (map[string]any, error)

// Registry 是进程内存中的 Schema 注册中心，管理 Schema 的版本演进
// 与各 ObjectType 的存量实例。所有方法均可并发安全调用。
type Registry struct {
	mu        sync.Mutex
	history   []*Schema
	current   *Schema
	instances map[string]map[string]map[string]any
}

func NewRegistry() *Registry {
	return &Registry{instances: map[string]map[string]map[string]any{}}
}

// CurrentSchema 返回当前版本的深拷贝；注册表为空时返回 nil。
func (r *Registry) CurrentSchema() *Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSchema(r.current)
}

// GetSchema 按版本号取回历史 Schema 的深拷贝，取回内容不受后续演进影响。
func (r *Registry) GetSchema(version int) (*Schema, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if version < 1 || version > len(r.history) {
		return nil, false
	}
	return cloneSchema(r.history[version-1]), true
}

// Evolve 提交一组 ObjectType 的新定义（未包含的 ObjectType 保持不变）。
//
// 兼容变更（新增可空属性、必填放宽为可空、新增 ObjectType）直接升版本。
// 破坏性变更只有在 migrations 中为对应 ObjectType 显式提供迁移函数时才放行；
// 任意实例迁移失败（返回 error 或 panic）都会整体回滚。
// 多处变更中任意一处不兼容或任一实例迁移失败，版本号都不前进。
func (r *Registry) Evolve(types []ObjectType, migrations map[string]MigrateFunc) (*Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range types {
		if err := validateObjectType(&types[i]); err != nil {
			return nil, err
		}
	}

	next := r.buildNextSchema(types)

	changes := map[string][]BreakingChange{}
	var unresolved []BreakingChange
	for i := range next.ObjectTypes {
		newOT := &next.ObjectTypes[i]
		oldOT, existed := r.lookupObjectType(newOT.Name)
		cs := diffObjectType(oldOT, newOT, existed)
		if len(cs) == 0 {
			continue
		}
		changes[newOT.Name] = cs
		if _, ok := migrations[newOT.Name]; !ok {
			unresolved = append(unresolved, cs...)
		}
	}
	if len(unresolved) > 0 {
		return nil, &BreakingChangesError{Changes: unresolved}
	}

	staged := r.stageInstances()
	for typeName := range changes {
		migrate := migrations[typeName]
		newOT, _ := next.objectType(typeName)
		if err := r.migrateInstances(staged, newOT, migrate); err != nil {
			return nil, err
		}
	}

	next.Version = len(r.history) + 1
	r.history = append(r.history, cloneSchema(next))
	r.current = cloneSchema(next)
	r.instances = staged
	return cloneSchema(next), nil
}

func (r *Registry) buildNextSchema(types []ObjectType) *Schema {
	next := &Schema{}
	if r.current != nil {
		next.ObjectTypes = make([]ObjectType, len(r.current.ObjectTypes))
		for i, ot := range r.current.ObjectTypes {
			next.ObjectTypes[i] = cloneObjectType(ot)
		}
	}
	for _, nt := range types {
		found := false
		for i := range next.ObjectTypes {
			if next.ObjectTypes[i].Name == nt.Name {
				next.ObjectTypes[i] = cloneObjectType(nt)
				found = true
				break
			}
		}
		if !found {
			next.ObjectTypes = append(next.ObjectTypes, cloneObjectType(nt))
		}
	}
	return next
}

func (r *Registry) lookupObjectType(name string) (ObjectType, bool) {
	if r.current == nil {
		return ObjectType{}, false
	}
	return r.current.objectType(name)
}

// stageInstances 产出实例存储的深拷贝，迁移只作用于拷贝；
// 迁移失败时丢弃拷贝即可，已提交状态不受任何影响。
func (r *Registry) stageInstances() map[string]map[string]map[string]any {
	staged := make(map[string]map[string]map[string]any, len(r.instances))
	for typeName, byID := range r.instances {
		cp := make(map[string]map[string]any, len(byID))
		for id, inst := range byID {
			cp[id] = cloneInstance(inst)
		}
		staged[typeName] = cp
	}
	return staged
}

func (r *Registry) migrateInstances(
	staged map[string]map[string]map[string]any,
	newOT ObjectType,
	migrate MigrateFunc,
) (err error) {
	byID := staged[newOT.Name]
	if len(byID) == 0 {
		return nil
	}
	migrated := make(map[string]map[string]any, len(byID))
	for id, inst := range byID {
		out, merr := runMigration(migrate, cloneInstance(inst))
		if merr != nil {
			return &MigrationError{ObjectType: newOT.Name, InstanceID: id, Err: merr}
		}
		if verr := validateInstance(newOT, out); verr != nil {
			return &MigrationError{ObjectType: newOT.Name, InstanceID: id, Err: verr}
		}
		migrated[instanceID(newOT, out)] = out
	}
	staged[newOT.Name] = migrated
	return nil
}

func runMigration(migrate MigrateFunc, inst map[string]any) (out map[string]any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("migration panicked: %v", rec)
		}
	}()
	return migrate(inst)
}

func validateObjectType(ot *ObjectType) error {
	if ot.Name == "" {
		return &ValidationError{Reason: "objectType name must not be empty"}
	}
	seen := map[string]bool{}
	primaryCount := 0
	var primaryName string
	for _, p := range ot.Properties {
		if p.Name == "" {
			return &ValidationError{ObjectType: ot.Name, Reason: "property name must not be empty"}
		}
		if seen[p.Name] {
			return &ValidationError{ObjectType: ot.Name, Property: p.Name, Reason: "duplicate property name"}
		}
		seen[p.Name] = true
		if !validValueType(p.Type) {
			return &ValidationError{ObjectType: ot.Name, Property: p.Name, Reason: "unknown value type: " + string(p.Type)}
		}
		if p.Primary {
			primaryCount++
			primaryName = p.Name
		}
	}
	if primaryCount == 0 {
		return &ValidationError{ObjectType: ot.Name, Reason: "exactly one primary key is required, got 0"}
	}
	if primaryCount > 1 {
		return &ValidationError{ObjectType: ot.Name, Property: primaryName,
			Reason: "exactly one primary key is required, got " + strconv.Itoa(primaryCount)}
	}
	return nil
}

func validValueType(t ValueType) bool {
	switch t {
	case TypeString, TypeInt, TypeFloat, TypeBool:
		return true
	}
	return false
}

// diffObjectType 比较新旧 ObjectType，列出全部破坏性变更。
func diffObjectType(oldOT ObjectType, newOT *ObjectType, existed bool) []BreakingChange {
	if !existed {
		return nil
	}

	oldProps := map[string]PropertyType{}
	for _, p := range oldOT.Properties {
		oldProps[p.Name] = p
	}
	newProps := map[string]PropertyType{}
	for _, p := range newOT.Properties {
		newProps[p.Name] = p
	}

	var cs []BreakingChange

	for name, op := range oldProps {
		np, ok := newProps[name]
		if !ok {
			cs = append(cs, BreakingChange{ObjectType: newOT.Name, Property: name, Kind: BreakPropertyRemoved})
			continue
		}
		if np.Type != op.Type {
			cs = append(cs, BreakingChange{ObjectType: newOT.Name, Property: name, Kind: BreakTypeChanged})
		}
		if !op.Required && np.Required {
			cs = append(cs, BreakingChange{ObjectType: newOT.Name, Property: name, Kind: BreakRequiredTightened})
		}
	}
	for name := range newProps {
		if _, ok := oldProps[name]; !ok {
			np := newProps[name]
			if np.Required {
				cs = append(cs, BreakingChange{ObjectType: newOT.Name, Property: name, Kind: BreakRequiredPropertyAdded})
			}
		}
	}

	if oldPrimary, newPrimary := primaryKeyName(oldOT), primaryKeyName(*newOT); oldPrimary != newPrimary {
		cs = append(cs, BreakingChange{ObjectType: newOT.Name, Property: newPrimary, Kind: BreakPrimaryKeyChanged})
	}

	return cs
}

func primaryKeyName(ot ObjectType) string {
	for _, p := range ot.Properties {
		if p.Primary {
			return p.Name
		}
	}
	return ""
}

func cloneInstance(inst map[string]any) map[string]any {
	cp := make(map[string]any, len(inst))
	for k, v := range inst {
		cp[k] = v
	}
	return cp
}

func instanceID(ot ObjectType, inst map[string]any) string {
	for _, p := range ot.Properties {
		if p.Primary {
			return fmt.Sprintf("%v", inst[p.Name])
		}
	}
	return ""
}

func validateInstance(ot ObjectType, inst map[string]any) error {
	props := map[string]PropertyType{}
	for _, p := range ot.Properties {
		props[p.Name] = p
	}
	for name, val := range inst {
		p, ok := props[name]
		if !ok {
			return &ValidationError{ObjectType: ot.Name, Property: name, Reason: "unknown property"}
		}
		if val == nil {
			if p.Required || p.Primary {
				return &ValidationError{ObjectType: ot.Name, Property: name, Reason: "required property is nil"}
			}
			continue
		}
		if !valueMatchesType(val, p.Type) {
			return &ValidationError{ObjectType: ot.Name, Property: name,
				Reason: "value " + fmt.Sprintf("%v", val) + " does not match type " + string(p.Type)}
		}
	}
	for _, p := range ot.Properties {
		if (p.Required || p.Primary) && inst[p.Name] == nil {
			return &ValidationError{ObjectType: ot.Name, Property: p.Name, Reason: "required property is missing"}
		}
	}
	return nil
}

func valueMatchesType(val any, t ValueType) bool {
	switch t {
	case TypeString:
		_, ok := val.(string)
		return ok
	case TypeBool:
		_, ok := val.(bool)
		return ok
	case TypeInt:
		return reflect.TypeOf(val).Kind() == reflect.Int
	case TypeFloat:
		k := reflect.TypeOf(val).Kind()
		return k == reflect.Float32 || k == reflect.Float64
	}
	return false
}

// PutInstance 按当前版本的 Schema 校验并写入一个实例，主键值作为实例 ID。
func (r *Registry) PutInstance(objectType string, values map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == nil {
		return fmt.Errorf("no schema registered yet")
	}
	ot, ok := r.current.objectType(objectType)
	if !ok {
		return fmt.Errorf("unknown objectType %q", objectType)
	}
	inst := cloneInstance(values)
	if err := validateInstance(ot, inst); err != nil {
		return err
	}
	byID, ok := r.instances[objectType]
	if !ok {
		byID = map[string]map[string]any{}
		r.instances[objectType] = byID
	}
	byID[instanceID(ot, inst)] = inst
	return nil
}

// GetInstance 按主键取回实例的深拷贝。
func (r *Registry) GetInstance(objectType, id string) (map[string]any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if byID, ok := r.instances[objectType]; ok {
		if inst, ok := byID[id]; ok {
			return cloneInstance(inst), true
		}
	}
	return nil, false
}

// ListInstances 返回某 ObjectType 全部实例的深拷贝。
func (r *Registry) ListInstances(objectType string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	byID := r.instances[objectType]
	out := make([]map[string]any, 0, len(byID))
	for _, inst := range byID {
		out = append(out, cloneInstance(inst))
	}
	return out
}
