package ontology

import "sync"

// Store 是本体对象类型、实例与链接的线程安全内存存储。
// aggview.Engine 复用同一实例：所有变更都经过 Store 登记，
// 引擎通过快照（Snapshot）在单个临界区内完成"读旧状态—改状态—维护视图"。
type Store struct {
	mu sync.RWMutex

	types   map[string]*ObjectType
	objects map[string]*Object
	links   map[string]*Link

	// 邻接索引：from -> linkID 集合；incoming：to -> linkID 集合。
	outgoing map[string]map[string]struct{}
	incoming map[string]map[string]struct{}
}

func NewStore() *Store {
	return &Store{
		types:    map[string]*ObjectType{},
		objects:  map[string]*Object{},
		links:    map[string]*Link{},
		outgoing: map[string]map[string]struct{}{},
		incoming: map[string]map[string]struct{}{},
	}
}

// ObjectType 描述一个对象类型。
type ObjectType struct {
	Name string
}

// Object 是一个对象实例。
type Object struct {
	ID         string
	Type       string
	Attributes map[string]float64
}

// Link 是两个实例之间一跳有向链接。
type Link struct {
	ID   string
	From string
	To   string
	Rel  string
}

// LinkIDs 返回当前全部链接 ID（快照）。
func (s *Store) LinkIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.links))
	for id := range s.links {
		ids = append(ids, id)
	}
	return ids
}

// GetLink 返回链接副本；不存在时 ok=false。
func (s *Store) GetLink(id string) (Link, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.links[id]
	if !ok {
		return Link{}, false
	}
	return *l, true
}

// writeAttrLocked 在持锁状态下写入属性，返回旧值与其是否存在。
func (s *Store) WriteAttrLocked(id, attr string, v float64) (float64, bool) {
	o := s.objects[id]
	old, had := o.Attributes[attr]
	o.Attributes[attr] = v
	return old, had
}

// EnsureType 登记一个对象类型（幂等）。
func (s *Store) EnsureType(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.types[name]; !ok {
		s.types[name] = &ObjectType{Name: name}
	}
}

// HasType 报告类型是否已声明。
func (s *Store) HasType(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.types[name]
	return ok
}

// CreateObject 创建实例；重复创建同一 ID 返回 false。
func (s *Store) CreateObject(id, typ string, attrs map[string]float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[id]; ok {
		return false
	}
	cp := make(map[string]float64, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	s.objects[id] = &Object{ID: id, Type: typ, Attributes: cp}
	return true
}

// ObjectTypeOf 返回实例类型；不存在时 ok=false。
func (s *Store) ObjectTypeOf(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	if !ok {
		return "", false
	}
	return o.Type, true
}

// Attr 读取实例的某个数值属性；实例不存在时 ok=false，属性缺失时 ok=false。
func (s *Store) Attr(id, attr string) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	if !ok {
		return 0, false
	}
	v, ok := o.Attributes[attr]
	return v, ok
}

// HasObject 报告实例是否存在。
func (s *Store) HasObject(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objects[id]
	return ok
}

// ---- Locked 变体：调用方必须已持有 s 的读锁或写锁（Engine 在联合临界区内使用）。----

func (s *Store) HasObjectLocked(id string) bool {
	_, ok := s.objects[id]
	return ok
}

func (s *Store) HasTypeLocked(name string) bool {
	_, ok := s.types[name]
	return ok
}

func (s *Store) ObjectTypeOfLocked(id string) (string, bool) {
	o, ok := s.objects[id]
	if !ok {
		return "", false
	}
	return o.Type, true
}

func (s *Store) AttrLocked(id, attr string) (float64, bool) {
	o, ok := s.objects[id]
	if !ok {
		return 0, false
	}
	v, ok := o.Attributes[attr]
	return v, ok
}

func (s *Store) GetLinkLocked(id string) (Link, bool) {
	l, ok := s.links[id]
	if !ok {
		return Link{}, false
	}
	return *l, true
}

// OutgoingLocked 返回 from 出发的全部链接副本。
func (s *Store) OutgoingLocked(from string) []Link {
	out := s.outgoing[from]
	res := make([]Link, 0, len(out))
	for id := range out {
		res = append(res, *s.links[id])
	}
	return res
}

// IncomingLocked 返回指向 to 的全部链接副本。
func (s *Store) IncomingLocked(to string) []Link {
	in := s.incoming[to]
	res := make([]Link, 0, len(in))
	for id := range in {
		res = append(res, *s.links[id])
	}
	return res
}

// AllObjectsLocked 返回全部 (id,type)，用于视图初始化/结构变更后的全量重建。
func (s *Store) AllObjectsLocked() []Object {
	res := make([]Object, 0, len(s.objects))
	for _, o := range s.objects {
		res = append(res, *o)
	}
	return res
}

// Objects 返回全部对象实例副本（快照）。
func (s *Store) Objects() []Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AllObjectsLocked()
}

// Links 返回全部链接副本（快照）。
func (s *Store) Links() []Link {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Link, 0, len(s.links))
	for _, l := range s.links {
		res = append(res, *l)
	}
	return res
}

// Lock / Unlock 暴露给 aggview 引擎做"存储+视图"联合临界区。
// 引擎在一次写变更的整个过程中持有写锁，保证并发变更串行等价。
func (s *Store) Lock()    { s.mu.Lock() }
func (s *Store) Unlock()  { s.mu.Unlock() }
func (s *Store) RLock()   { s.mu.RLock() }
func (s *Store) RUnlock() { s.mu.RUnlock() }

// addLinkLocked 在已持锁状态下加入链接。
func (s *Store) AddLinkLocked(l *Link) bool {
	if _, ok := s.links[l.ID]; ok {
		return false
	}
	s.links[l.ID] = l
	if s.outgoing[l.From] == nil {
		s.outgoing[l.From] = map[string]struct{}{}
	}
	s.outgoing[l.From][l.ID] = struct{}{}
	if s.incoming[l.To] == nil {
		s.incoming[l.To] = map[string]struct{}{}
	}
	s.incoming[l.To][l.ID] = struct{}{}
	return true
}

// removeLinkLocked 在已持锁状态下删除链接，返回被删链接；不存在返回 nil。
func (s *Store) RemoveLinkLocked(id string) *Link {
	l, ok := s.links[id]
	if !ok {
		return nil
	}
	delete(s.links, id)
	delete(s.outgoing[l.From], id)
	delete(s.incoming[l.To], id)
	return l
}
