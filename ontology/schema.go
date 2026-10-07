package ontology

import "fmt"

// PropertySpec 描述对象类型的一个属性。
type PropertySpec struct {
	Name string
	// Mergeable 为 true 时允许并发写冲突自动合并，必须指定 MergeRule；
	// 为 false 时并发写新值不同将整体拒绝较晚判定的那次写入。
	Mergeable bool
	// MergeRule 合并规则名称（仅 Mergeable 为 true 时有效），见 merge.go 内置规则。
	MergeRule string
}

// ObjectType 描述一个对象类型的 schema。
type ObjectType struct {
	Name       string
	Properties []PropertySpec
}

// Validate 校验 schema 自身合法性（属性名唯一、可合并属性必须带合法规则等）。
func (t *ObjectType) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("对象类型名不能为空")
	}
	seen := make(map[string]struct{}, len(t.Properties))
	for i := range t.Properties {
		p := &t.Properties[i]
		if p.Name == "" {
			return fmt.Errorf("属性名不能为空")
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("属性 %q 重复定义", p.Name)
		}
		seen[p.Name] = struct{}{}
		if p.Mergeable {
			if RuleByName(p.MergeRule) == nil {
				return fmt.Errorf("属性 %q 声明了未知合并规则 %q", p.Name, p.MergeRule)
			}
		} else if p.MergeRule != "" {
			return fmt.Errorf("属性 %q 不可合并但声明了合并规则 %q", p.Name, p.MergeRule)
		}
	}
	return nil
}

// property 按名查找属性定义，未找到返回 nil。
func (t *ObjectType) property(name string) *PropertySpec {
	for i := range t.Properties {
		if t.Properties[i].Name == name {
			return &t.Properties[i]
		}
	}
	return nil
}

// Property 按名查找属性定义（导出供独立实现的对照模型使用）。
func (t *ObjectType) Property(name string) (PropertySpec, bool) {
	if p := t.property(name); p != nil {
		return *p, true
	}
	return PropertySpec{}, false
}
