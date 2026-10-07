package ontology

import "sort"

// typeDef 描述一个对象类型：直接声明的属性与父类型。
type typeDef struct {
	name       string
	parent     string
	properties map[string]bool
}

// typeRegistry 维护对象类型与继承关系。
type typeRegistry struct {
	types map[string]*typeDef
}

func newTypeRegistry() *typeRegistry {
	return &typeRegistry{types: map[string]*typeDef{}}
}

// addType 注册一个对象类型；parent 为空表示继承链顶端。
func (r *typeRegistry) addType(name, parent string, properties []string) error {
	if name == "" {
		return newError(CategoryInvalidArgument, "type name must not be empty")
	}
	if _, exists := r.types[name]; exists {
		return newError(CategoryInvalidArgument, "type already exists: "+name)
	}
	if parent != "" {
		if _, ok := r.types[parent]; !ok {
			return newError(CategoryObjectTypeNotFound, "parent type not found: "+parent)
		}
	}
	props := map[string]bool{}
	for _, p := range properties {
		props[p] = true
	}
	r.types[name] = &typeDef{name: name, parent: parent, properties: props}
	return nil
}

// setParent 重组继承关系；须拒绝成环与自继承。
func (r *typeRegistry) setParent(name, parent string) error {
	t, ok := r.types[name]
	if !ok {
		return newError(CategoryObjectTypeNotFound, "type not found: "+name)
	}
	if parent == name {
		return newError(CategoryInheritanceCycle, "type cannot inherit from itself: "+name)
	}
	if parent != "" {
		if _, ok := r.types[parent]; !ok {
			return newError(CategoryObjectTypeNotFound, "parent type not found: "+parent)
		}
		// 从 parent 沿链向上若遇到 name，则构成环。
		for cur, seen := parent, map[string]bool{}; cur != ""; {
			if cur == name {
				return newError(CategoryInheritanceCycle, "setting parent would create a cycle: "+name+" -> "+parent)
			}
			if seen[cur] {
				break // 既有数据保证无环，防御性退出
			}
			seen[cur] = true
			cur = r.types[cur].parent
		}
	}
	t.parent = parent
	return nil
}

// chain 返回从 name 到继承链顶端的类型名序列（含 name 自身，近端在前）。
func (r *typeRegistry) chain(name string) ([]string, bool) {
	if _, ok := r.types[name]; !ok {
		return nil, false
	}
	var out []string
	seen := map[string]bool{}
	for cur := name; cur != ""; {
		if seen[cur] {
			break
		}
		seen[cur] = true
		out = append(out, cur)
		cur = r.types[cur].parent
	}
	return out, true
}

// allProperties 返回类型沿继承链的完整属性集合（有序、去重）。
func (r *typeRegistry) allProperties(name string) ([]string, bool) {
	chain, ok := r.chain(name)
	if !ok {
		return nil, false
	}
	set := map[string]bool{}
	for _, tn := range chain {
		for p := range r.types[tn].properties {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, true
}
