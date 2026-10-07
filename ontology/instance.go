package ontology

import "fmt"

// CreateInstance 创建绑定到具体类型的实例。类型不存在时返回 ErrNotFound。
func (r *Registry) CreateInstance(id, typeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.types[typeID]; !ok {
		return fmt.Errorf("%w: type %q", ErrNotFound, typeID)
	}
	r.instances[id] = &instance{id: id, typeID: typeID, values: make(map[string]string)}
	return nil
}

// WriteInstance 按当前生效规则校验并写入实例属性值。
// 校验与写入在同一临界区内完成，写入使用的规则被记录到变更日志。
func (r *Registry) WriteInstance(instanceID, property, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	inst, ok := r.instances[instanceID]
	if !ok {
		return fmt.Errorf("%w: instance %q", ErrNotFound, instanceID)
	}
	res, found := r.effectiveRuleLocked(inst.typeID, property)
	if !found {
		return fmt.Errorf("%w: type %q property %q", ErrNotFound, inst.typeID, property)
	}
	if !res.Rule.Allows(value) {
		return fmt.Errorf("%w: value %q for property %q", ErrValueNotAllowed, value, property)
	}
	inst.values[property] = value
	r.seq++
	r.mutations = append(r.mutations, MutationRecord{
		Seq:      r.seq,
		Kind:     MutationWriteValue,
		TypeID:   inst.typeID,
		Property: property,
		Instance: instanceID,
		Value:    value,
		RuleUsed: res.Rule.clone(),
	})
	return nil
}

// ReadInstance 读取实例属性的历史取值。
// 历史取值不做追溯性重新校验，即使当前生效规则已收紧也始终可读。
// 第二个返回值表示该属性是否曾被写入过。
func (r *Registry) ReadInstance(instanceID, property string) (string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inst, ok := r.instances[instanceID]
	if !ok {
		return "", false, fmt.Errorf("%w: instance %q", ErrNotFound, instanceID)
	}
	v, written := inst.values[property]
	return v, written, nil
}
