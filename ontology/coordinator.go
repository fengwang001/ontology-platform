package ontology

import "sync"

// defaultRule 是继承链走到根仍未见任何显式声明时的平台默认规则：拒绝写。
var defaultRule = Rule{}

// Coordinator 协调继承权限判定与实例乐观并发写入。
// 全部状态由一把互斥锁保护，所有操作天然等价于某个全局串行顺序。
type Coordinator struct {
	mu      sync.Mutex
	types   map[string]*ObjectType
	objects map[string]*Instance
	log     *DecisionLog
}

func NewCoordinator() *Coordinator {
	return &Coordinator{
		types:   make(map[string]*ObjectType),
		objects: make(map[string]*Instance),
		log:     NewDecisionLog(),
	}
}

// Log 返回判定日志（只读视图由 DecisionLog 自身保证）。
func (c *Coordinator) Log() *DecisionLog {
	return c.log
}

// resolveRule 沿继承链从最具体类型向上查找 prop 的生效规则。
// 只沿 parent 指针行走，访问的类型数等于链深，与类型总量无关。
// 返回生效规则、命中类型与沿途访问的类型序列（供内部验证，不对外暴露）。
// 调用方须持有 c.mu。
func (c *Coordinator) resolveRule(typeID, prop string) (Rule, string, []string) {
	visited := []string{}
	for id := typeID; id != ""; {
		t := c.types[id]
		if t == nil {
			break
		}
		visited = append(visited, id)
		if rule, ok := t.Rules[prop]; ok {
			return rule, id, visited
		}
		id = t.Parent
	}
	return defaultRule, "", visited
}

// CreateType 注册对象类型；parent 为空串表示根类型。
func (c *Coordinator) CreateType(id, parent string, rules map[string]Rule, required []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if parent != "" {
		if _, ok := c.types[parent]; !ok {
			return newError(ErrTypeNotFound, "parent type %q not found", parent)
		}
	}
	cloned := make(map[string]Rule, len(rules))
	for k, v := range rules {
		cloned[k] = v
	}
	c.types[id] = &ObjectType{
		ID:       id,
		Parent:   parent,
		Rules:    cloned,
		Required: append([]string(nil), required...),
	}
	c.log.add(Decision{
		Op:     "CreateType",
		Input:  id + " parent=" + parent,
		Output: "ok",
		Basis:  "type registered",
	})
	return nil
}

// SetRule 在指定类型上显式声明（覆盖）某属性的权限规则。
// 对未覆盖的下层类型实时生效；已显式覆盖的下层类型不受影响。
func (c *Coordinator) SetRule(typeID, prop string, rule Rule) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.types[typeID]
	if !ok {
		return newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	t.Rules[prop] = rule
	c.log.add(Decision{
		Op:     "SetRule",
		Input:  typeID + "." + prop,
		Output: "ok",
		Basis:  "explicit rule declared; effective immediately for non-overriding descendants",
	})
	return nil
}

// ClearRule 移除指定类型在某属性上的显式声明，恢复继承。
func (c *Coordinator) ClearRule(typeID, prop string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.types[typeID]
	if !ok {
		return newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	delete(t.Rules, prop)
	c.log.add(Decision{
		Op:     "ClearRule",
		Input:  typeID + "." + prop,
		Output: "ok",
		Basis:  "explicit rule removed; inheritance restored",
	})
	return nil
}

// Reparent 重新指定类型的父类型（继承关系重组）。
// 未显式覆盖的属性立即按新父链重新解析；已覆盖的属性不受影响。
// 拒绝产生继承环的重组。
func (c *Coordinator) Reparent(typeID, newParent string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.types[typeID]
	if !ok {
		return newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	if newParent != "" {
		if _, ok := c.types[newParent]; !ok {
			return newError(ErrTypeNotFound, "parent type %q not found", newParent)
		}
	}
	// 环检测：从 newParent 沿父链向上，若遇到 typeID 则成环。
	for id := newParent; id != ""; {
		if id == typeID {
			c.log.add(Decision{
				Op:     "Reparent",
				Input:  typeID + " -> " + newParent,
				Output: ErrCycle.String(),
				Basis:  "new parent chain contains the type itself",
			})
			return newError(ErrCycle, "reparenting %q under %q would create a cycle", typeID, newParent)
		}
		id = c.types[id].Parent
	}
	t.Parent = newParent
	c.log.add(Decision{
		Op:     "Reparent",
		Input:  typeID + " -> " + newParent,
		Output: "ok",
		Basis:  "non-overridden properties re-resolve against the new parent chain immediately",
	})
	return nil
}

// SetRequired 重设类型的必需属性列表（字段级细则）。
func (c *Coordinator) SetRequired(typeID string, required []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.types[typeID]
	if !ok {
		return newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	t.Required = append([]string(nil), required...)
	c.log.add(Decision{
		Op:     "SetRequired",
		Input:  typeID,
		Output: "ok",
		Basis:  "required property list replaced",
	})
	return nil
}

// CreateObject 创建实例，初始版本号为 1。
func (c *Coordinator) CreateObject(id, typeID string, props map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.types[typeID]
	if !ok {
		return newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	if missing := missingRequired(t, props); missing != "" {
		c.log.add(Decision{
			Op:     "CreateObject",
			Input:  id + " type=" + typeID,
			Output: ErrMissingRequired.String(),
			Basis:  "required property " + missing + " missing",
		})
		return newError(ErrMissingRequired, "object %q missing required property %q", id, missing)
	}
	cloned := make(map[string]any, len(props))
	for k, v := range props {
		cloned[k] = v
	}
	c.objects[id] = &Instance{ID: id, Type: typeID, Version: 1, Props: cloned}
	c.log.add(Decision{
		Op:     "CreateObject",
		Input:  id + " type=" + typeID,
		Output: "ok version=1",
		Basis:  "object created",
	})
	return nil
}

// Write 乐观并发写入：先校验版本，再判定权限，再校验字段级细则。
// 拒绝优先级：对象不存在 > 版本冲突 > 继承链权限拒绝 > 必需属性缺失。
// 任何拒绝都不会改变版本号或对象状态。
func (c *Coordinator) Write(objectID string, expectedVersion int64, props map[string]any, role string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	inst, ok := c.objects[objectID]
	if !ok {
		c.log.add(Decision{
			Op:     "Write",
			Input:  writeInput(objectID, expectedVersion, props, role),
			Output: ErrObjectNotFound.String(),
			Basis:  "object not found",
		})
		return 0, newError(ErrObjectNotFound, "object %q not found", objectID)
	}
	if inst.Version != expectedVersion {
		c.log.add(Decision{
			Op:     "Write",
			Input:  writeInput(objectID, expectedVersion, props, role),
			Output: ErrVersionConflict.String(),
			Basis:  "expected version does not match current version; version checked before permission",
		})
		return inst.Version, newError(ErrVersionConflict,
			"object %q expected version %d, current %d", objectID, expectedVersion, inst.Version)
	}
	for prop := range props {
		rule, _, _ := c.resolveRule(inst.Type, prop)
		if !rule.Allows(role) {
			c.log.add(Decision{
				Op:     "Write",
				Input:  writeInput(objectID, expectedVersion, props, role),
				Output: ErrPermissionDenied.String(),
				Basis:  "inherited rule denies role " + role + " on property " + prop,
			})
			return inst.Version, newError(ErrPermissionDenied,
				"role %q not allowed to write property %q on object %q", role, prop, objectID)
		}
	}
	merged := make(map[string]any, len(inst.Props)+len(props))
	for k, v := range inst.Props {
		merged[k] = v
	}
	for k, v := range props {
		merged[k] = v
	}
	if missing := missingRequired(c.types[inst.Type], merged); missing != "" {
		c.log.add(Decision{
			Op:     "Write",
			Input:  writeInput(objectID, expectedVersion, props, role),
			Output: ErrMissingRequired.String(),
			Basis:  "required property " + missing + " missing after merge",
		})
		return inst.Version, newError(ErrMissingRequired,
			"object %q missing required property %q", objectID, missing)
	}
	inst.Props = merged
	inst.Version++
	c.log.add(Decision{
		Op:     "Write",
		Input:  writeInput(objectID, expectedVersion, props, role),
		Output: "ok",
		Basis:  "version matched, permission granted, version incremented",
	})
	return inst.Version, nil
}

// Get 返回实例的只读快照。
func (c *Coordinator) Get(objectID string) (Instance, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inst, ok := c.objects[objectID]
	if !ok {
		return Instance{}, false
	}
	cloned := make(map[string]any, len(inst.Props))
	for k, v := range inst.Props {
		cloned[k] = v
	}
	return Instance{ID: inst.ID, Type: inst.Type, Version: inst.Version, Props: cloned}, true
}

// missingRequired 返回第一个缺失的必需属性，空串表示无缺失。
// 调用方须持有 c.mu。
func missingRequired(t *ObjectType, props map[string]any) string {
	for _, req := range t.Required {
		if _, ok := props[req]; !ok {
			return req
		}
	}
	return ""
}

func writeInput(objectID string, expectedVersion int64, props map[string]any, role string) string {
	return objectID + " expected=" + itoa(expectedVersion) + " role=" + role + " props=" + propKeys(props)
}
