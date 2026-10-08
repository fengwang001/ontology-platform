package ontology

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

// state 是不可变的图 + 权限状态。一旦发布（存入 Store.cur）即不再被修改，
// 所有变更通过克隆-交换产生新版本，因此一次快照读取即是一个线性化点。
type state struct {
	version uint64

	objectTypes map[ObjectTypeID]ObjectType
	linkTypes   map[LinkTypeID]LinkType
	objects     map[ObjectID]Object
	// out 按源对象组织的出边邻接表。
	out map[ObjectID][]Link

	// existence[caller][object]：调用者对对象的存在性权限。
	existence map[CallerID]map[ObjectID]bool
	// traversal[caller][linkType]：调用者对链接类型的遍历权限。
	traversal map[CallerID]map[LinkTypeID]bool
}

// clone 返回当前状态的浅克隆：所有顶层 map 重新分配，
// 供写操作在副本上修改后再原子发布。
func (st *state) clone() *state {
	next := &state{
		version:     st.version,
		objectTypes: maps.Clone(st.objectTypes),
		linkTypes:   maps.Clone(st.linkTypes),
		objects:     maps.Clone(st.objects),
		out:         make(map[ObjectID][]Link, len(st.out)),
		existence:   make(map[CallerID]map[ObjectID]bool, len(st.existence)),
		traversal:   make(map[CallerID]map[LinkTypeID]bool, len(st.traversal)),
	}
	for id, links := range st.out {
		next.out[id] = links // 切片共享，修改前由写方复制
	}
	for c, m := range st.existence {
		next.existence[c] = maps.Clone(m)
	}
	for c, m := range st.traversal {
		next.traversal[c] = maps.Clone(m)
	}
	return next
}

func emptyState() *state {
	return &state{
		objectTypes: map[ObjectTypeID]ObjectType{},
		linkTypes:   map[LinkTypeID]LinkType{},
		objects:     map[ObjectID]Object{},
		out:         map[ObjectID][]Link{},
		existence:   map[CallerID]map[ObjectID]bool{},
		traversal:   map[CallerID]map[LinkTypeID]bool{},
	}
}

// 变更错误。
var (
	ErrObjectTypeExists   = errors.New("ontology: object type already exists")
	ErrObjectTypeUnknown  = errors.New("ontology: unknown object type")
	ErrLinkTypeExists     = errors.New("ontology: link type already exists")
	ErrLinkTypeUnknown    = errors.New("ontology: unknown link type")
	ErrObjectExists       = errors.New("ontology: object already exists")
	ErrObjectUnknown      = errors.New("ontology: unknown object")
	ErrLinkExists         = errors.New("ontology: link already exists")
	ErrLinkUnknown        = errors.New("ontology: unknown link")
	ErrInvalidObjectIDFmt = errors.New("ontology: malformed object id")
)

// Store 持有当前状态，写操作串行化，读快照无锁。
type Store struct {
	mu  sync.Mutex // 仅写操作持有
	cur atomic.Pointer[state]
}

// NewStore 返回一个空 Store。
func NewStore() *Store {
	s := &Store{}
	s.cur.Store(emptyState())
	return s
}

// snapshot 原子地读取当前状态，是查询的线性化点。
func (s *Store) snapshot() *state {
	return s.cur.Load()
}

// mutate 在写锁内执行 fn：fn 读取当前状态并返回克隆修改后的新状态，
// 提交时递增版本号并原子发布，保证校验-修改-发布是一个串行化单元。
// 返回新状态的版本号。
func (s *Store) mutate(fn func(cur *state) (*state, error)) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := fn(s.cur.Load())
	if err != nil {
		return 0, err
	}
	next.version = s.cur.Load().version + 1
	s.cur.Store(next)
	return next.version, nil
}

// PutObjectType 注册对象类型。已存在时返回 ErrObjectTypeExists。
func (s *Store) PutObjectType(t ObjectType) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		if _, ok := cur.objectTypes[t.ID]; ok {
			return nil, ErrObjectTypeExists
		}
		next := cur.clone()
		next.objectTypes[t.ID] = t
		return next, nil
	})
}

// PutLinkType 注册链接类型。已存在时返回 ErrLinkTypeExists。
func (s *Store) PutLinkType(t LinkType) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		if _, ok := cur.linkTypes[t.ID]; ok {
			return nil, ErrLinkTypeExists
		}
		next := cur.clone()
		next.linkTypes[t.ID] = t
		return next, nil
	})
}

// AddObject 添加对象。类型未知或对象已存在时报错。
func (s *Store) AddObject(o Object) (uint64, error) {
	if !o.ID.Valid() {
		return 0, ErrInvalidObjectIDFmt
	}
	return s.mutate(func(cur *state) (*state, error) {
		if _, ok := cur.objectTypes[o.Type]; !ok {
			return nil, ErrObjectTypeUnknown
		}
		if _, ok := cur.objects[o.ID]; ok {
			return nil, ErrObjectExists
		}
		next := cur.clone()
		next.objects[o.ID] = o
		return next, nil
	})
}

// RemoveObject 删除对象，并级联删除其关联的所有链接。
func (s *Store) RemoveObject(id ObjectID) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		if _, ok := cur.objects[id]; !ok {
			return nil, ErrObjectUnknown
		}
		next := cur.clone()
		delete(next.objects, id)
		delete(next.out, id)
		for src, links := range next.out {
			filtered := slices.DeleteFunc(slices.Clone(links), func(l Link) bool {
				return l.To == id
			})
			if len(filtered) == 0 {
				delete(next.out, src)
			} else {
				next.out[src] = filtered
			}
		}
		for c, m := range next.existence {
			if _, ok := m[id]; ok {
				delete(m, id)
				if len(m) == 0 {
					delete(next.existence, c)
				}
			}
		}
		return next, nil
	})
}

// AddLink 添加有向链接。链接类型或两端对象未知、链接已存在时报错。
func (s *Store) AddLink(l Link) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		if _, ok := cur.linkTypes[l.Type]; !ok {
			return nil, ErrLinkTypeUnknown
		}
		if _, ok := cur.objects[l.From]; !ok {
			return nil, ErrObjectUnknown
		}
		if _, ok := cur.objects[l.To]; !ok {
			return nil, ErrObjectUnknown
		}
		if slices.Contains(cur.out[l.From], l) {
			return nil, ErrLinkExists
		}
		next := cur.clone()
		next.out[l.From] = append(slices.Clone(next.out[l.From]), l)
		return next, nil
	})
}

// RemoveLink 删除有向链接。
func (s *Store) RemoveLink(l Link) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		links := cur.out[l.From]
		idx := slices.Index(links, l)
		if idx < 0 {
			return nil, ErrLinkUnknown
		}
		next := cur.clone()
		filtered := slices.Delete(slices.Clone(links), idx, idx+1)
		if len(filtered) == 0 {
			delete(next.out, l.From)
		} else {
			next.out[l.From] = filtered
		}
		return next, nil
	})
}

// SetExistence 授予或回收调用者对对象的存在性权限。
func (s *Store) SetExistence(caller CallerID, object ObjectID, allow bool) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		next := cur.clone()
		m := next.existence[caller]
		if m == nil {
			m = map[ObjectID]bool{}
			next.existence[caller] = m
		}
		if allow {
			m[object] = true
		} else {
			delete(m, object)
			if len(m) == 0 {
				delete(next.existence, caller)
			}
		}
		return next, nil
	})
}

// SetTraversal 授予或回收调用者对链接类型的遍历权限。
func (s *Store) SetTraversal(caller CallerID, linkType LinkTypeID, allow bool) (uint64, error) {
	return s.mutate(func(cur *state) (*state, error) {
		next := cur.clone()
		m := next.traversal[caller]
		if m == nil {
			m = map[LinkTypeID]bool{}
			next.traversal[caller] = m
		}
		if allow {
			m[linkType] = true
		} else {
			delete(m, linkType)
			if len(m) == 0 {
				delete(next.traversal, caller)
			}
		}
		return next, nil
	})
}
