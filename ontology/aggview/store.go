package aggview

import "math/big"

// ID 是实例、对象类型、链接类型、视图的统一标识。
type ID string

// Policy 声明一个实例同时归属多个分组时对每个分组的贡献方式。
type Policy int

const (
	// PolicyFull：实例经由多条同类型链接归属 k 个分组时，
	// 对每个分组都贡献完整值 v（共贡献 k*v）。
	PolicyFull Policy = iota + 1
	// PolicyEvenShare：实例对每个所属分组贡献 v/k。
	PolicyEvenShare
)

// Value 表示一个可能处于"不存在"状态的数值。
type Value struct {
	Present bool
	Rat     *big.Rat
}

// Aggregate 是一个分组的增量聚合结果。
type Aggregate struct {
	Sum   *big.Rat
	Count int
}

// 构造与读取 Value 的辅助函数。所有 Rat 均做拷贝，避免调用方别名修改。

// AbsentValue 返回"不存在"状态的值（区别于取值为零）。
func AbsentValue() Value { return Value{Present: false} }

// FromInt 用整数构造一个存在的数值。
func FromInt(n int64) Value {
	return Value{Present: true, Rat: big.NewRat(n, 1)}
}

// FromRat 用 *big.Rat 构造一个存在的数值（内部拷贝）。
func FromRat(r *big.Rat) Value {
	return Value{Present: true, Rat: new(big.Rat).Set(r)}
}

// IsAbsent 报告值是否处于不存在状态。
func (v Value) IsAbsent() bool { return !v.Present }

func cloneValue(v Value) Value {
	if !v.Present {
		return AbsentValue()
	}
	return FromRat(v.Rat)
}

// objSnapshot 是单个实例的原始数据快照。
type objSnapshot struct {
	alive bool
	typ   ID
	links map[ID]map[ID]bool // linkType -> targetID 集合
	props map[ID]Value       // propType -> value
}

// Store 是最小本体平台：实例、数值属性与有向链接（含反向索引）。
// Store 本身不保证并发安全；所有访问经由 Engine 的单一互斥锁串行化，
// 这也是属性写入与归属改变能够放进同一处理单元、并保证可串行化的基础。
type Store struct {
	objs map[ID]*objSnapshot
	// rev[linkType][target] = 指向 target 的全部 source 集合
	rev map[ID]map[ID]map[ID]bool
}

func NewStore() *Store {
	return &Store{
		objs: map[ID]*objSnapshot{},
		rev:  map[ID]map[ID]map[ID]bool{},
	}
}

// CreateObject 创建给定类型的实例。
func (s *Store) CreateObject(id, typ ID) error {
	if _, ok := s.objs[id]; ok {
		return &plainError{"object already exists: " + string(id)}
	}
	s.objs[id] = &objSnapshot{
		alive: true,
		typ:   typ,
		links: map[ID]map[ID]bool{},
		props: map[ID]Value{},
	}
	return nil
}

func (s *Store) object(id ID) (*objSnapshot, bool) {
	o, ok := s.objs[id]
	if !ok || !o.alive {
		return nil, false
	}
	return o, true
}

func (s *Store) Exists(id ID) bool {
	_, ok := s.object(id)
	return ok
}

// ObjectType 返回存活实例的类型。
func (s *Store) ObjectType(id ID) (ID, bool) {
	o, ok := s.object(id)
	if !ok {
		return "", false
	}
	return o.typ, true
}

func (s *Store) SetProperty(id, prop ID, v Value) (Value, error) {
	o, ok := s.object(id)
	if !ok {
		return Value{}, &plainError{"object not found: " + string(id)}
	}
	old := cloneValue(o.props[prop])
	if v.Present {
		o.props[prop] = cloneValue(v)
	} else {
		delete(o.props, prop)
	}
	return old, nil
}

func (s *Store) GetProperty(id, prop ID) Value {
	o, ok := s.object(id)
	if !ok {
		return AbsentValue()
	}
	return cloneValue(o.props[prop])
}

func (s *Store) AddLink(from, linkType, to ID) (bool, error) {
	o, ok := s.object(from)
	if !ok {
		return false, &plainError{"object not found: " + string(from)}
	}
	if _, ok := s.object(to); !ok {
		return false, &plainError{"object not found: " + string(to)}
	}
	targets, ok := o.links[linkType]
	if !ok {
		targets = map[ID]bool{}
		o.links[linkType] = targets
	}
	if targets[to] {
		return false, nil // 重复链接不产生新的归属
	}
	targets[to] = true
	byTarget, ok := s.rev[linkType]
	if !ok {
		byTarget = map[ID]map[ID]bool{}
		s.rev[linkType] = byTarget
	}
	sources, ok := byTarget[to]
	if !ok {
		sources = map[ID]bool{}
		byTarget[to] = sources
	}
	sources[from] = true
	return true, nil
}

func (s *Store) RemoveLink(from, linkType, to ID) (bool, error) {
	o, ok := s.object(from)
	if !ok {
		return false, &plainError{"object not found: " + string(from)}
	}
	if !o.links[linkType][to] {
		return false, nil
	}
	delete(o.links[linkType], to)
	if len(o.links[linkType]) == 0 {
		delete(o.links, linkType)
	}
	if sources := s.rev[linkType][to]; sources != nil {
		delete(sources, from)
		if len(sources) == 0 {
			delete(s.rev[linkType], to)
		}
	}
	return true, nil
}

func (s *Store) HasLink(from, linkType, to ID) bool {
	o, ok := s.object(from)
	return ok && o.links[linkType][to]
}

func (s *Store) LinksFrom(from, linkType ID) []ID {
	o, ok := s.object(from)
	if !ok {
		return nil
	}
	var out []ID
	for target := range o.links[linkType] {
		out = append(out, target)
	}
	return out
}

func (s *Store) LinksTo(linkType, to ID) []ID {
	var out []ID
	for source := range s.rev[linkType][to] {
		out = append(out, source)
	}
	return out
}

// DeleteObject 删除实例并清除其全部出入链接的索引。
// 调用方（Engine）必须在此之前完成聚合结果的级联扣减。
func (s *Store) DeleteObject(id ID) (bool, error) {
	o, ok := s.object(id)
	if !ok {
		return false, nil
	}
	for linkType, targets := range o.links {
		for to := range targets {
			if sources := s.rev[linkType][to]; sources != nil {
				delete(sources, id)
				if len(sources) == 0 {
					delete(s.rev[linkType], to)
				}
			}
		}
	}
	for linkType, byTarget := range s.rev {
		if sources, ok := byTarget[id]; ok {
			for from := range sources {
				if src := s.objs[from]; src != nil {
					delete(src.links[linkType], id)
					if len(src.links[linkType]) == 0 {
						delete(src.links, linkType)
					}
				}
			}
			delete(byTarget, id)
		}
	}
	o.alive = false
	delete(s.objs, id)
	return true, nil
}

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }
