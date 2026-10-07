package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// ObjectID 唯一标识一个本体对象。
type ObjectID string

// LinkID 唯一标识一条对象之间的链接。
type LinkID string

// Object 是本体图中的一个对象。created/deleted 为 MVCC 版本号，
// deleted == 0 表示存活。
type Object struct {
	ID         ObjectID
	Type       string
	Properties map[string]string

	created uint64
	deleted uint64
}

// Link 是对象之间的一条有向链接。
type Link struct {
	ID   LinkID
	Type string
	From ObjectID
	To   ObjectID

	created uint64
	deleted uint64
}

// Store 是一个支持 MVCC 快照的版本化内存图存储。
// 每次结构修改（增删对象/链接）都会使全局版本号单调递增，
// 遍历会话在开始时钉住（pin）一个版本，之后该版本上的读视图
// 不受后续并发修改影响。
type Store struct {
	mu       sync.RWMutex
	version  uint64
	objects  map[ObjectID]*Object
	links    map[LinkID]*Link
	outIndex map[ObjectID][]LinkID
	inIndex  map[ObjectID][]LinkID
	pinned   map[uint64]int
}

// NewStore 创建一个空图存储。
func NewStore() *Store {
	return &Store{
		objects:  make(map[ObjectID]*Object),
		links:    make(map[LinkID]*Link),
		outIndex: make(map[ObjectID][]LinkID),
		inIndex:  make(map[ObjectID][]LinkID),
		pinned:   make(map[uint64]int),
	}
}

// AddObject 新增对象；ID 已存在（含被删除的墓碑）时报错。
func (s *Store) AddObject(o Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[o.ID]; exists {
		return fmt.Errorf("object %q already exists", o.ID)
	}
	s.version++
	o.created = s.version
	o.deleted = 0
	s.objects[o.ID] = &o
	return nil
}

// DeleteObject 删除对象，并连带删除其在当前版本上仍存活的关联链接。
func (s *Store) DeleteObject(id ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	if !ok || o.deleted != 0 {
		return fmt.Errorf("object %q not found", id)
	}
	s.version++
	o.deleted = s.version
	// 连带删除当前仍存活的关联链接，保持图一致性。
	incident := append(append([]LinkID{}, s.outIndex[id]...), s.inIndex[id]...)
	for _, lid := range incident {
		if l, ok := s.links[lid]; ok && l.deleted == 0 {
			l.deleted = s.version
		}
	}
	return nil
}

// AddLink 新增链接；要求两端对象在当前版本存活。
func (s *Store) AddLink(l Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.links[l.ID]; exists {
		return fmt.Errorf("link %q already exists", l.ID)
	}
	for _, ep := range []ObjectID{l.From, l.To} {
		o, ok := s.objects[ep]
		if !ok || o.deleted != 0 {
			return fmt.Errorf("link %q endpoint object %q not found", l.ID, ep)
		}
	}
	s.version++
	l.created = s.version
	l.deleted = 0
	s.links[l.ID] = &l
	s.outIndex[l.From] = append(s.outIndex[l.From], l.ID)
	s.inIndex[l.To] = append(s.inIndex[l.To], l.ID)
	return nil
}

// DeleteLink 删除链接。
func (s *Store) DeleteLink(id LinkID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[id]
	if !ok || l.deleted != 0 {
		return fmt.Errorf("link %q not found", id)
	}
	s.version++
	l.deleted = s.version
	return nil
}

// Snapshot 返回当前全局版本号，可作为遍历快照使用。
func (s *Store) Snapshot() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Pin 钉住一个快照版本，返回释放函数。被钉住的版本在释放前，
// 其所需的墓碑数据不会被物理回收。
func (s *Store) Pin(v uint64) (release func()) {
	s.mu.Lock()
	s.pinned[v]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.pinned[v]--
			if s.pinned[v] <= 0 {
				delete(s.pinned, v)
			}
			s.gcLocked()
		})
	}
}

// gcLocked 物理回收所有被钉住版本都不再需要的墓碑数据。
// 调用方须持有 s.mu。
func (s *Store) gcLocked() {
	// 没有任何活跃快照时也不能回收“当前仍存活”的数据，
	// 只能回收墓碑；下限取最小被钉版本，无钉时取当前版本。
	floor := s.version
	for v := range s.pinned {
		if v < floor {
			floor = v
		}
	}
	for id, o := range s.objects {
		// 墓碑版本不超过 floor，即所有活跃快照都看不到该对象，可物理回收。
		if o.deleted != 0 && o.deleted <= floor {
			delete(s.objects, id)
			delete(s.outIndex, id)
			delete(s.inIndex, id)
		}
	}
	for id, l := range s.links {
		if l.deleted != 0 && l.deleted <= floor {
			delete(s.links, id)
			s.outIndex[l.From] = removeLinkID(s.outIndex[l.From], id)
			s.inIndex[l.To] = removeLinkID(s.inIndex[l.To], id)
		}
	}
}

func removeLinkID(ids []LinkID, id LinkID) []LinkID {
	for i, v := range ids {
		if v == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

// visible 报告实体在版本 v 上是否可见。
func visible(created, deleted, v uint64) bool {
	return created <= v && (deleted == 0 || deleted > v)
}

// objectAt 读取版本 v 上的对象。
func (s *Store) objectAt(v uint64, id ObjectID) (*Object, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	if !ok || !visible(o.created, o.deleted, v) {
		return nil, false
	}
	return o, true
}

// candidate 是扩展时的一个候选邻居。
type candidate struct {
	link     LinkID
	neighbor ObjectID
}

// neighborsAt 返回版本 v 上从 id 出发、满足方向与链接类型过滤的候选
// 邻居，按 LinkID 字典序排列，保证确定性顺序。
func (s *Store) neighborsAt(v uint64, id ObjectID, linkType string, dir Direction) []candidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []candidate
	collect := func(lids []LinkID, outgoing bool) {
		for _, lid := range lids {
			l, ok := s.links[lid]
			if !ok || !visible(l.created, l.deleted, v) {
				continue
			}
			if linkType != "" && l.Type != linkType {
				continue
			}
			neighbor := l.To
			if !outgoing {
				neighbor = l.From
			}
			// 邻居对象在该快照上必须同样可见。
			if no, ok := s.objects[neighbor]; !ok || !visible(no.created, no.deleted, v) {
				continue
			}
			out = append(out, candidate{link: lid, neighbor: neighbor})
		}
	}
	if dir == DirOut || dir == DirBoth {
		collect(s.outIndex[id], true)
	}
	if dir == DirIn || dir == DirBoth {
		collect(s.inIndex[id], false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].link < out[j].link })
	return out
}
