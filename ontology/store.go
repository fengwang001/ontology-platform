package ontology

import "sort"

type instance struct {
	id    ObjectID
	typ   ObjectTypeName
	props map[PropertyName]PropertyValue
	out   map[LinkTypeName]map[ObjectID]struct{}
}

// storeState 是全部可变数据。所有公开操作都在 Store.mu 下进行，
// 先拷贝 state 得到事务工作副本，成功后一次性提交，失败则丢弃副本，
// 从而保证“属性写入与下游索引更新同单元、任一失败整体不生效”。
type storeState struct {
	inst map[ObjectID]*instance
	// incoming[目标实例][链接名] = 指向它的源实例集合。
	incoming map[ObjectID]map[LinkTypeName]map[ObjectID]struct{}
	// index[(类型,派生属性)][键值] = 当前可索引且取到该键值的下游实例集合。
	index map[propKey]map[string]map[ObjectID]struct{}
	// states 记录每个下游实例在每个派生属性上的当前索引状态（含不可索引状态）。
	states map[ObjectID]map[PropertyName]IndexState
}

type Store struct {
	schema   *Schema
	journal  *Journal
	mu       rwmutex
	state    *storeState
	failHook func() error
}

func NewStore(schema *Schema) *Store {
	return &Store{
		schema: schema,
		state: &storeState{
			inst:     map[ObjectID]*instance{},
			incoming: map[ObjectID]map[LinkTypeName]map[ObjectID]struct{}{},
			index:    map[propKey]map[string]map[ObjectID]struct{}{},
			states:   map[ObjectID]map[PropertyName]IndexState{},
		},
		journal: NewJournal(),
	}
}

// Journal 暴露变更日志（只读快照）。
func (st *Store) Journal() *Journal { return st.journal }

// snapshot 是不可变一致性读视图：持有 mu 的读锁期间拷贝出的状态副本。
type snapshotData struct {
	inst     map[ObjectID]instance
	incoming map[ObjectID]map[LinkTypeName][]ObjectID
	index    map[propKey]map[string][]ObjectID
}

type Snapshot struct{ data snapshotData }

// Snapshot 取出当前完整状态的深拷贝视图，供查询、朴素对拍与测试断言使用。
func (st *Store) Snapshot() Snapshot {
	st.mu.rLock()
	defer st.mu.rUnlock()
	return Snapshot{data: st.state.snapshotData(st.schema)}
}

func (s *storeState) snapshotData(schema *Schema) snapshotData {
	out := snapshotData{
		inst:     make(map[ObjectID]instance, len(s.inst)),
		incoming: map[ObjectID]map[LinkTypeName][]ObjectID{},
		index:    map[propKey]map[string][]ObjectID{},
	}
	for id, ins := range s.inst {
		cp := instance{id: ins.id, typ: ins.typ,
			props: make(map[PropertyName]PropertyValue, len(ins.props)),
			out:   map[LinkTypeName]map[ObjectID]struct{}{}}
		for p, v := range ins.props {
			cp.props[p] = v
		}
		for ln, tos := range ins.out {
			set := make(map[ObjectID]struct{}, len(tos))
			for to := range tos {
				set[to] = struct{}{}
			}
			cp.out[ln] = set
		}
		out.inst[id] = cp
	}
	for target, byLink := range s.incoming {
		m := map[LinkTypeName][]ObjectID{}
		for ln, froms := range byLink {
			ids := make([]ObjectID, 0, len(froms))
			for from := range froms {
				ids = append(ids, from)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			m[ln] = ids
		}
		out.incoming[target] = m
	}
	for k, buckets := range s.index {
		m := map[string][]ObjectID{}
		for val, ids := range buckets {
			lst := make([]ObjectID, 0, len(ids))
			for id := range ids {
				lst = append(lst, id)
			}
			sort.Slice(lst, func(i, j int) bool { return lst[i] < lst[j] })
			m[val] = lst
		}
		out.index[k] = m
	}
	return out
}

// clone 生成事务工作副本（深拷贝）。
func (s *storeState) clone() *storeState {
	cp := &storeState{
		inst:     make(map[ObjectID]*instance, len(s.inst)),
		incoming: map[ObjectID]map[LinkTypeName]map[ObjectID]struct{}{},
		index:    map[propKey]map[string]map[ObjectID]struct{}{},
		states:   map[ObjectID]map[PropertyName]IndexState{},
	}
	for id, ins := range s.inst {
		c := &instance{
			id:    ins.id,
			typ:   ins.typ,
			props: make(map[PropertyName]PropertyValue, len(ins.props)),
			out:   map[LinkTypeName]map[ObjectID]struct{}{},
		}
		for p, v := range ins.props {
			c.props[p] = v
		}
		for ln, tos := range ins.out {
			set := make(map[ObjectID]struct{}, len(tos))
			for to := range tos {
				set[to] = struct{}{}
			}
			c.out[ln] = set
		}
		cp.inst[id] = c
	}
	for target, byLink := range s.incoming {
		m := map[LinkTypeName]map[ObjectID]struct{}{}
		for ln, froms := range byLink {
			set := make(map[ObjectID]struct{}, len(froms))
			for from := range froms {
				set[from] = struct{}{}
			}
			m[ln] = set
		}
		cp.incoming[target] = m
	}
	for k, buckets := range s.index {
		m := map[string]map[ObjectID]struct{}{}
		for val, ids := range buckets {
			set := make(map[ObjectID]struct{}, len(ids))
			for id := range ids {
				set[id] = struct{}{}
			}
			m[val] = set
		}
		cp.index[k] = m
	}
	for id, props := range s.states {
		m := make(map[PropertyName]IndexState, len(props))
		for p, st := range props {
			m[p] = st
		}
		cp.states[id] = m
	}
	return cp
}

func (cp *storeState) commitInto(dst *storeState) {
	*dst = *cp
}
