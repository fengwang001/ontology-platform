package ontology

import "fmt"

// PropertyDef 描述对象类型的一个属性：类型、是否允许自动合并，
// 以及允许合并时声明的合并规则。
type PropertyDef struct {
	Name      string
	Kind      ValueKind
	Mergeable bool
	Rule      MergeRule // 仅当 Mergeable 为 true 时有效，否则必须为 RuleNone
}

// ObjectType 是本体平台中的对象类型定义。
type ObjectType struct {
	Name  string
	Props map[string]PropertyDef
}

// Validate 校验对象类型定义自身的合法性。
func (t *ObjectType) Validate() error {
	if t == nil {
		return fmt.Errorf("object type is nil")
	}
	if t.Name == "" {
		return fmt.Errorf("object type: name is required")
	}
	if len(t.Props) == 0 {
		return fmt.Errorf("object type %q: at least one property is required", t.Name)
	}
	for name, p := range t.Props {
		if p.Name != name {
			return fmt.Errorf("object type %q: property key %q does not match property name %q", t.Name, name, p.Name)
		}
		if p.Mergeable {
			if p.Rule == RuleNone {
				return fmt.Errorf("object type %q: mergeable property %q must declare a merge rule", t.Name, name)
			}
			if !p.Rule.compatibleWith(p.Kind) {
				return fmt.Errorf("object type %q: property %q: merge rule %s is incompatible with kind %s", t.Name, name, p.Rule, p.Kind)
			}
		} else if p.Rule != RuleNone {
			return fmt.Errorf("object type %q: non-mergeable property %q must use RuleNone", t.Name, name)
		}
	}
	return nil
}

// validateRequest 校验一次写入请求是否符合对象类型定义。
// 校验失败属于请求非法（返回 error），不属于三种互斥判定结果。
func (t *ObjectType) validateRequest(req WriteRequest) error {
	if req.InstanceID == "" {
		return fmt.Errorf("write request: instance id is required")
	}
	if req.WriteID == "" {
		return fmt.Errorf("write request: write id is required")
	}
	if len(req.Changes) == 0 {
		return fmt.Errorf("write request: at least one changed property is required")
	}
	for name, v := range req.Changes {
		def, ok := t.Props[name]
		if !ok {
			return fmt.Errorf("write request: unknown property %q", name)
		}
		if v.Kind != def.Kind {
			return fmt.Errorf("write request: property %q expects kind %s, got %s", name, def.Kind, v.Kind)
		}
	}
	return nil
}
