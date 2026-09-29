package ontology

import "strings"

// refLocation 标记图中一个引用位置：Kind 为属性/链接/Action 的字段路径，Type/Action/Property/Link 为定位信息。
type refLocation struct {
	Kind     string
	TypeRID  string
	Action   string
	Property string
	Link     string
}

func (l refLocation) String() string {
	var b strings.Builder
	b.WriteString(l.Kind)
	b.WriteByte('{')
	first := true
	add := func(k, v string) {
		if v == "" {
			return
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
	}
	add("type", l.TypeRID)
	add("action", l.Action)
	add("property", l.Property)
	add("link", l.Link)
	b.WriteByte('}')
	return b.String()
}

// walkRefs 遍历图中所有对象类型名引用位置，对每个引用调用 fn。
// 覆盖对象类型属性（Property.TypeRef）、链接（SourceType/TargetType）以及 Action 中的全部类型引用。
func walkRefs(g *Graph, fn func(loc refLocation, ref string)) {
	for _, t := range g.Types {
		for _, p := range t.Properties {
			if p.TypeRef != "" {
				fn(refLocation{Kind: "property.type", TypeRID: t.RID, Property: p.Name}, p.TypeRef)
			}
		}
		for _, l := range t.Links {
			fn(refLocation{Kind: "link.source", TypeRID: t.RID, Link: l.Name}, l.SourceType)
			fn(refLocation{Kind: "link.target", TypeRID: t.RID, Link: l.Name}, l.TargetType)
		}
	}
	for _, a := range g.Actions {
		for _, p := range a.Params {
			if p.TypeRef != "" {
				fn(refLocation{Kind: "action.param", Action: a.Name, Property: p.Name}, p.TypeRef)
			}
		}
		for i, in := range a.Inputs {
			fn(refLocation{Kind: "action.inputs", Action: a.Name, Property: itoa(i)}, in)
		}
		for i, n := range a.Creates {
			fn(refLocation{Kind: "action.creates", Action: a.Name, Property: itoa(i)}, n)
		}
		for i, n := range a.Reads {
			fn(refLocation{Kind: "action.reads", Action: a.Name, Property: itoa(i)}, n)
		}
		for i, n := range a.Updates {
			fn(refLocation{Kind: "action.updates", Action: a.Name, Property: itoa(i)}, n)
		}
		for i, n := range a.Deletes {
			fn(refLocation{Kind: "action.deletes", Action: a.Name, Property: itoa(i)}, n)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// cloneGraph 深拷贝图，保证重命名暂存阶段的修改不影响已发布快照。
func cloneGraph(g *Graph) *Graph {
	if g == nil {
		return nil
	}
	cp := &Graph{
		Types:   make([]*ObjectType, len(g.Types)),
		Actions: make([]*Action, len(g.Actions)),
	}
	for i, t := range g.Types {
		nt := &ObjectType{
			RID:        t.RID,
			Name:       t.Name,
			Properties: append([]Property(nil), t.Properties...),
			Links:      append([]Link(nil), t.Links...),
		}
		cp.Types[i] = nt
	}
	for i, a := range g.Actions {
		na := &Action{
			Name:    a.Name,
			Params:  append([]Param(nil), a.Params...),
			Inputs:  append([]string(nil), a.Inputs...),
			Creates: append([]string(nil), a.Creates...),
			Reads:   append([]string(nil), a.Reads...),
			Updates: append([]string(nil), a.Updates...),
			Deletes: append([]string(nil), a.Deletes...),
		}
		cp.Actions[i] = na
	}
	if g.Pending != nil {
		p := *g.Pending
		cp.Pending = &p
	}
	return cp
}
