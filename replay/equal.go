package replay

import "reflect"

// validPropType 报告属性类型是否合法。
func validPropType(t string) bool {
	return t == PropString || t == PropInt || t == PropBool
}

// valueMatchesPropType 报告属性值是否与声明的属性类型匹配。
func valueMatchesPropType(t string, v any) bool {
	switch t {
	case PropString:
		_, ok := v.(string)
		return ok
	case PropInt:
		_, ok := v.(int)
		return ok
	case PropBool:
		_, ok := v.(bool)
		return ok
	}
	return false
}

func equalObjectType(a, b *ObjectType) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Name == b.Name && reflect.DeepEqual(a.Properties, b.Properties)
}

func equalLinkType(a, b *LinkType) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalObject(a, b *ObjectInstance) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Type == b.Type && reflect.DeepEqual(a.Properties, b.Properties)
}

func equalLink(a, b *LinkInstance) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func cloneObjectType(t *ObjectType) *ObjectType {
	if t == nil {
		return nil
	}
	props := make(map[string]string, len(t.Properties))
	for k, v := range t.Properties {
		props[k] = v
	}
	return &ObjectType{Name: t.Name, Properties: props}
}

func cloneLinkType(t *LinkType) *LinkType {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func cloneObject(o *ObjectInstance) *ObjectInstance {
	if o == nil {
		return nil
	}
	props := make(map[string]any, len(o.Properties))
	for k, v := range o.Properties {
		props[k] = v
	}
	return &ObjectInstance{ID: o.ID, Type: o.Type, Properties: props}
}

func cloneLink(l *LinkInstance) *LinkInstance {
	if l == nil {
		return nil
	}
	c := *l
	return &c
}

// CloneSnapshot 深拷贝一份快照（测试与朴素参照模型使用）。
func CloneSnapshot(s *Snapshot) *Snapshot {
	objectTypes := make(map[string]ObjectType, len(s.Schema.ObjectTypes))
	for k, v := range s.Schema.ObjectTypes {
		objectTypes[k] = *cloneObjectType(&v)
	}
	linkTypes := make(map[string]LinkType, len(s.Schema.LinkTypes))
	for k, v := range s.Schema.LinkTypes {
		linkTypes[k] = *cloneLinkType(&v)
	}
	objects := make(map[string]ObjectInstance, len(s.Objects))
	for k, v := range s.Objects {
		objects[k] = *cloneObject(&v)
	}
	links := make(map[string]LinkInstance, len(s.Links))
	for k, v := range s.Links {
		links[k] = *cloneLink(&v)
	}
	return NewSnapshot(Schema{ObjectTypes: objectTypes, LinkTypes: linkTypes}, objects, links)
}
