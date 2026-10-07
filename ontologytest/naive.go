// Package ontologytest 提供与生产实现完全独立的朴素参考模型与随机差分对拍。
//
// 朴素模型刻意“不做任何增量维护”：它只保存对象属性与链接边的真值集合，
// 每次需要期望结果时都从真值出发对全量实例重新求值（含多级传递），
// 因此不会继承生产代码里任何潜在的增量传播错误。
package ontologytest

import "sort"

type (
	TypeName   string
	PropName   string
	LinkName   string
	ID         string
	Val        string
	StatusKind int
)

const (
	StIndexable StatusKind = iota
	StMissingSource
	StNotUnique
	StNoValue
)

type State struct {
	Status StatusKind
	Value  Val
}

type LinkSpec struct {
	Name   LinkName
	From   TypeName
	To     TypeName
	Unique bool
}

type DerivedSpec struct {
	OnType TypeName
	Prop   PropName
	Link   LinkName
	Source PropName
}

type instance struct {
	typ   TypeName
	props map[PropName]Val
}

// NaiveModel 是朴素全量重算的参考实现。
type NaiveModel struct {
	types   map[TypeName]map[PropName]bool
	links   map[LinkName]LinkSpec
	derived map[TypeName]map[PropName]DerivedSpec

	inst map[ID]instance
	out  map[ID]map[LinkName]map[ID]bool
}

func NewNaiveModel() *NaiveModel {
	return &NaiveModel{
		types:   map[TypeName]map[PropName]bool{},
		links:   map[LinkName]LinkSpec{},
		derived: map[TypeName]map[PropName]DerivedSpec{},
		inst:    map[ID]instance{},
		out:     map[ID]map[LinkName]map[ID]bool{},
	}
}

func (m *NaiveModel) RegisterType(t TypeName, props []PropName) {
	s := map[PropName]bool{}
	for _, p := range props {
		s[p] = true
	}
	m.types[t] = s
}

func (m *NaiveModel) RegisterLink(l LinkSpec) { m.links[l.Name] = l }

func (m *NaiveModel) RegisterDerived(d DerivedSpec) {
	if m.derived[d.OnType] == nil {
		m.derived[d.OnType] = map[PropName]DerivedSpec{}
	}
	m.derived[d.OnType][d.Prop] = d
}

func (m *NaiveModel) Create(id ID, t TypeName, props map[PropName]Val) {
	ins := instance{typ: t, props: map[PropName]Val{}}
	for p, v := range props {
		ins.props[p] = v
	}
	m.inst[id] = ins
}

func (m *NaiveModel) Delete(id ID) {
	delete(m.inst, id)
	delete(m.out, id)
	for _, links := range m.out {
		for _, tos := range links {
			delete(tos, id)
		}
	}
}

func (m *NaiveModel) SetAttr(id ID, p PropName, v Val, has bool) {
	ins := m.inst[id]
	if ins.props == nil {
		ins.props = map[PropName]Val{}
	}
	if has {
		ins.props[p] = v
	} else {
		delete(ins.props, p)
	}
	m.inst[id] = ins
}

func (m *NaiveModel) AddLink(l LinkName, from, to ID) {
	if m.out[from] == nil {
		m.out[from] = map[LinkName]map[ID]bool{}
	}
	if m.out[from][l] == nil {
		m.out[from][l] = map[ID]bool{}
	}
	m.out[from][l][to] = true
}

func (m *NaiveModel) RemoveLink(l LinkName, from, to ID) {
	if m.out[from] != nil && m.out[from][l] != nil {
		delete(m.out[from][l], to)
	}
}

// Evaluate 从真值全量重算单个 (实例,属性) 的期望状态；多级传递沿派生 DAG 递归。
func (m *NaiveModel) Evaluate(id ID, prop PropName) State {
	return m.eval(id, prop, map[pair]bool{})
}

type pair struct {
	id   ID
	prop PropName
}

func (m *NaiveModel) eval(id ID, prop PropName, onPath map[pair]bool) State {
	ins, ok := m.inst[id]
	if !ok {
		return State{Status: StMissingSource}
	}
	d, isDerived := m.derived[ins.typ][prop]
	if !isDerived {
		v, has := ins.props[prop]
		if !has {
			return State{Status: StNoValue}
		}
		return State{Status: StIndexable, Value: v}
	}
	tos := m.out[id][d.Link]
	if len(tos) == 0 {
		return State{Status: StMissingSource}
	}
	if len(tos) > 1 {
		return State{Status: StNotUnique}
	}
	var target ID
	for to := range tos {
		target = to
	}
	if _, ok := m.inst[target]; !ok {
		return State{Status: StMissingSource}
	}
	pk := pair{target, d.Source}
	if onPath[pk] {
		return State{Status: StMissingSource}
	}
	onPath[pk] = true
	st := m.eval(target, d.Source, onPath)
	delete(onPath, pk)
	return st
}

// FullIndex 为某 (类型,派生属性) 重建完整期望索引，仅包含可索引实例。
func (m *NaiveModel) FullIndex(t TypeName, prop PropName) map[Val][]ID {
	out := map[Val][]ID{}
	for id, ins := range m.inst {
		if ins.typ != t {
			continue
		}
		if _, isDerived := m.derived[t][prop]; !isDerived {
			continue
		}
		st := m.Evaluate(id, prop)
		if st.Status == StIndexable {
			out[st.Value] = append(out[st.Value], id)
		}
	}
	for v := range out {
		sort.Slice(out[v], func(i, j int) bool { return out[v][i] < out[v][j] })
	}
	return out
}
