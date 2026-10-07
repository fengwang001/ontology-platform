package ontology

// catalog 是被仲裁器与朴素模型共享的模式目录：对象类型、实例类型归属、
// 链接类型，以及类型层属性默认可见性。它是纯查询数据，不包含任何仲裁逻辑，
// 因此仲裁器与朴素模型可以各自独立地组合它。
type catalog struct {
	// objectAttrs: 对象类型 -> 属性 -> 类型层默认可见性。
	objectAttrs map[string]map[string]Visibility
	// instanceType: 实例 ID -> 所属对象类型。
	instanceType map[string]string
	// linkTypes: 链接类型名 -> 规格。
	linkTypes map[string]LinkTypeSpec
}

func newCatalog() *catalog {
	return &catalog{
		objectAttrs:  map[string]map[string]Visibility{},
		instanceType: map[string]string{},
		linkTypes:    map[string]LinkTypeSpec{},
	}
}

func (c *catalog) registerObjectType(name string, attrs []AttrSpec) bool {
	if name == "" {
		return false
	}
	if _, exists := c.objectAttrs[name]; exists {
		return false
	}
	m := make(map[string]Visibility, len(attrs))
	for _, a := range attrs {
		if a.Name == "" {
			return false
		}
		if _, dup := m[a.Name]; dup {
			return false
		}
		m[a.Name] = a.Default
	}
	c.objectAttrs[name] = m
	return true
}

func (c *catalog) setTypeDefault(typeName, attr string, vis Visibility) bool {
	attrs, ok := c.objectAttrs[typeName]
	if !ok {
		return false
	}
	if _, ok := attrs[attr]; !ok {
		return false
	}
	attrs[attr] = vis
	return true
}

func (c *catalog) registerLinkType(spec LinkTypeSpec) bool {
	if spec.Name == "" {
		return false
	}
	if _, exists := c.linkTypes[spec.Name]; exists {
		return false
	}
	srcAttrs, ok := c.objectAttrs[spec.SrcType]
	if !ok {
		return false
	}
	tgtAttrs, ok := c.objectAttrs[spec.TgtType]
	if !ok {
		return false
	}
	if _, ok := srcAttrs[spec.SrcAttr]; !ok {
		return false
	}
	if _, ok := tgtAttrs[spec.TgtAttr]; !ok {
		return false
	}
	c.linkTypes[spec.Name] = spec
	return true
}

func (c *catalog) createInstance(id, typeName string) bool {
	if id == "" {
		return false
	}
	if _, ok := c.objectAttrs[typeName]; !ok {
		return false
	}
	if _, exists := c.instanceType[id]; exists {
		return false
	}
	c.instanceType[id] = typeName
	return true
}

// typeDefault 返回类型层默认；属性未声明时默认可见（缺省开放）。
func (c *catalog) typeDefault(instanceID, attr string) Visibility {
	typeName, ok := c.instanceType[instanceID]
	if !ok {
		return Visible
	}
	attrs, ok := c.objectAttrs[typeName]
	if !ok {
		return Visible
	}
	if v, ok := attrs[attr]; ok {
		return v
	}
	return Visible
}
