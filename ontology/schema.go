package ontology

// PropertyValue 是属性的可空字符串值。
type PropertyValue struct {
	Val string
	Has bool
}

// LinkType 声明一种有向链接：从 SrcType 实例指向 DstType 实例。
// Unique=true 表示链接基数本身受限（每个源实例至多一条出边，建边时强制）。
// 注意：派生索引的“声明要求唯一”是独立概念——即便链接类型允许多条出边，
// 派生索引仍要求取值时目标唯一，多于一个时进入明确的不可索引状态。
type LinkType struct {
	Name    LinkTypeName
	SrcType ObjectTypeName
	DstType ObjectTypeName
	Unique  bool
}

// DerivedIndex 声明一个派生索引：
// 在 SrcType 的下游实例上维护属性 PropName 的派生索引，
// 取值来自下游实例经由 Link 类型唯一链接到的目标实例的 DstProp 属性。
type DerivedIndex struct {
	SrcType  ObjectTypeName
	PropName PropertyName
	Link     LinkTypeName
	DstProp  PropertyName
}

type ObjectTypeName string
type PropertyName string
type LinkTypeName string
type ObjectID string

type propKey struct {
	typ  ObjectTypeName
	prop PropertyName
}

type Schema struct{ state *schemaState }

type schemaState struct {
	props   map[ObjectTypeName]map[PropertyName]struct{}
	links   map[LinkTypeName]LinkType
	derived map[propKey]DerivedIndex
	// dependents: 取值来源节点 -> 依赖它的下游节点集合（派生 DAG 的出边）。
	dependents map[propKey][]propKey
	// incomingLinks: 目标 (类型,链接名) -> 声明为该链接来源的下游节点。
	// 实际按 link 名索引即可，因为链接名全局唯一，这里记录“哪些派生节点经过该链接取值”。
	derivedByLink map[LinkTypeName][]propKey
}

func NewSchema() *Schema {
	return &Schema{state: &schemaState{
		props:         map[ObjectTypeName]map[PropertyName]struct{}{},
		links:         map[LinkTypeName]LinkType{},
		derived:       map[propKey]DerivedIndex{},
		dependents:    map[propKey][]propKey{},
		derivedByLink: map[LinkTypeName][]propKey{},
	}}
}

func (s *Schema) RegisterObjectType(name ObjectTypeName, props []PropertyName) error {
	if name == "" {
		return newError(KindSourceNotFound, "object type name is empty")
	}
	if _, exists := s.state.props[name]; exists {
		return newError(KindSourceNotFound, "object type %q already registered", name)
	}
	set := make(map[PropertyName]struct{}, len(props))
	for _, p := range props {
		set[p] = struct{}{}
	}
	s.state.props[name] = set
	return nil
}

func (s *Schema) RegisterLinkType(lt LinkType) error {
	if _, ok := s.state.props[lt.SrcType]; !ok {
		return newError(KindSourceNotFound, "source object type %q not registered", lt.SrcType)
	}
	if _, ok := s.state.props[lt.DstType]; !ok {
		return newError(KindSourceNotFound, "target object type %q not registered", lt.DstType)
	}
	if _, exists := s.state.links[lt.Name]; exists {
		return newError(KindLinkTypeNotSupported, "link type %q already registered", lt.Name)
	}
	s.state.links[lt.Name] = lt
	return nil
}

// RegisterDerivedIndex 注册派生索引；若会形成循环传递，在生效前返回 KindCyclicDerivation。
//
// 派生关系形成一张以 (对象类型, 属性名) 为节点的有向图：
// 下游节点 (d.SrcType, d.PropName) 的取值来自来源节点 (lt.DstType, d.DstProp)。
// 新边 source -> downstream 若让来源节点沿既有边能到达下游节点，则加入后必然成环，
// 必须在声明生效之前拒绝。
func (s *Schema) RegisterDerivedIndex(d DerivedIndex) error {
	lt, ok := s.state.links[d.Link]
	if !ok {
		return newError(KindLinkTypeNotSupported, "link type %q not registered", d.Link)
	}
	if lt.SrcType != d.SrcType {
		return newError(KindLinkTypeNotSupported,
			"link type %q originates from %q, not %q", d.Link, lt.SrcType, d.SrcType)
	}
	if !s.hasProperty(lt.DstType, d.DstProp) {
		return newError(KindSourceNotFound,
			"target type %q has no property %q", lt.DstType, d.DstProp)
	}
	downstream := propKey{d.SrcType, d.PropName}
	if !s.hasProperty(d.SrcType, d.PropName) {
		// 派生属性也必须先在对象类型上登记为属性槽位。
		return newError(KindSourceNotFound,
			"downstream type %q has no property %q", d.SrcType, d.PropName)
	}
	if _, exists := s.state.derived[downstream]; exists {
		return newError(KindLinkTypeNotSupported,
			"derived index on (%q,%q) already registered", d.SrcType, d.PropName)
	}
	source := propKey{lt.DstType, d.DstProp}
	if source == downstream {
		return newError(KindCyclicDerivation,
			"derived index (%q,%q) would derive from itself via %q",
			d.SrcType, d.PropName, d.Link)
	}
	// 新边方向为 source -> downstream。若 downstream 已能沿既有依赖边
	// 到达 source，则加入新边后 source -> ... -> downstream -> source 成环。
	if s.reaches(downstream, source) {
		return newError(KindCyclicDerivation,
			"registering (%q,%q) via %q closes a derivation cycle",
			d.SrcType, d.PropName, d.Link)
	}
	s.state.derived[downstream] = d
	s.state.dependents[source] = append(s.state.dependents[source], downstream)
	s.state.derivedByLink[d.Link] = append(s.state.derivedByLink[d.Link], downstream)
	return nil
}

// reaches 判断从 from 沿既有派生依赖边能否到达 to。
func (s *Schema) reaches(from, to propKey) bool {
	stack := []propKey{from}
	seen := map[propKey]bool{from: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range s.state.dependents[cur] {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

func (s *Schema) linkType(name LinkTypeName) (LinkType, bool) {
	lt, ok := s.state.links[name]
	return lt, ok
}

func (s *Schema) hasProperty(t ObjectTypeName, p PropertyName) bool {
	set, ok := s.state.props[t]
	if !ok {
		return false
	}
	_, ok = set[p]
	return ok
}

func (s *Schema) derivedIndex(t ObjectTypeName, p PropertyName) (DerivedIndex, bool) {
	d, ok := s.state.derived[propKey{t, p}]
	return d, ok
}
