package ontology

import (
	"fmt"
	"sync"
)

// objectType 是继承链条中的一个对象类型节点。
type objectType struct {
	id       string
	parentID string // 空串表示根类型
	sealed   bool   // 是否禁止派生子类型
	declared map[string]Rule
	children map[string]struct{}
}

// instance 是绑定到具体类型的实例。
type instance struct {
	id     string
	typeID string
	values map[string]string
}

// Registry 管理对象类型继承体系、属性规则与实例。
// 全部操作在单一互斥锁下串行化，保证线性一致性。
type Registry struct {
	mu        sync.RWMutex
	types     map[string]*objectType
	instances map[string]*instance
	seq       uint64
	audit     []LookupRecord
	mutations []MutationRecord
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		types:     make(map[string]*objectType),
		instances: make(map[string]*instance),
	}
}

// CreateType 创建对象类型。parentID 为空串表示根类型；
// 否则父类型必须存在且未被标记为禁止派生。
// sealed 为 true 时该类型不允许产生子类型。
func (r *Registry) CreateType(id, parentID string, sealed bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	t := &objectType{
		id:       id,
		parentID: parentID,
		sealed:   sealed,
		declared: make(map[string]Rule),
		children: make(map[string]struct{}),
	}
	if parentID != "" {
		parent, ok := r.types[parentID]
		if !ok {
			return fmt.Errorf("%w: parent type %q", ErrNotFound, parentID)
		}
		if parent.sealed {
			return fmt.Errorf("%w: parent type %q", ErrTypeSealed, parentID)
		}
		parent.children[id] = struct{}{}
	}
	r.types[id] = t
	return nil
}

// DeleteType 删除类型。仅允许删除不再有任何子类型依赖的链条末端类型。
func (r *Registry) DeleteType(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.types[id]
	if !ok {
		return fmt.Errorf("%w: type %q", ErrNotFound, id)
	}
	if len(t.children) > 0 {
		return fmt.Errorf("%w: type %q has %d subtype(s)", ErrTypeHasChildren, id, len(t.children))
	}
	if t.parentID != "" {
		if parent, ok := r.types[t.parentID]; ok {
			delete(parent.children, id)
		}
	}
	delete(r.types, id)
	return nil
}

// DeclareRule 在类型上声明属性取值规则。
// 若继承链条上不存在该属性，则本声明为首次定义；
// 否则为重新声明，新规则允许集合必须是当前生效规则的子集或相等，
// 违反时在声明时即被拒绝（ErrRuleWidening）。
// 类型或属性组合不存在的判定（ErrNotFound）优先于其余校验。
func (r *Registry) DeclareRule(typeID, property string, rule Rule) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.types[typeID]
	if !ok {
		return fmt.Errorf("%w: type %q", ErrNotFound, typeID)
	}
	if current, found := r.effectiveRuleLocked(typeID, property); found {
		if !rule.IsSubsetOf(current.Rule) {
			return fmt.Errorf("%w: type %q property %q", ErrRuleWidening, typeID, property)
		}
	}
	t.declared[property] = rule.clone()
	r.seq++
	r.mutations = append(r.mutations, MutationRecord{
		Seq:      r.seq,
		Kind:     MutationDeclareRule,
		TypeID:   typeID,
		Property: property,
		Rule:     rule.clone(),
	})
	return nil
}
