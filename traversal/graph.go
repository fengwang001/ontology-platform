package traversal

import (
	"sort"
	"sync"
	"sync/atomic"
)

// adjacencyEntry 是一个邻接索引条目：经由某条链接到达对象 To。
type adjacencyEntry struct {
	link Link
	to   ObjectID
}

// snapshot 是图结构在某个时刻的不可变快照。
//
// 三张基础 map（objects/linkTypes/linksByID）在快照发布后不再被修改；
// 对图的任何结构性修改都通过拷贝这三张 map 并原子替换整个 snapshot
// 完成（copy-on-write）。邻接索引不在修改路径上维护，而是在该快照
// 首次被遍历时通过 ensureIndexes 一次性构建并随快照缓存，因此：
//
//   - 单次增删只拷贝基础 map，代价只随链接总数线性于 map 复制本身；
//   - 遍历开始时原子获取一次快照指针并始终基于该快照执行，天然等价于
//     「开始时刻的确定快照」，遍历期间的并发增删不会混入也不会导致
//     重复或遗漏。
type snapshot struct {
	version   uint64
	objects   map[ObjectID]struct{}
	linkTypes map[LinkTypeID]struct{}
	linksByID map[LinkID]Link

	indexOnce sync.Once
	// outgoing[id] / incoming[id] 按 (链接类型, 目标对象, 链接ID) 排序。
	outgoing map[ObjectID][]adjacencyEntry
	incoming map[ObjectID][]adjacencyEntry
}

// ensureIndexes 惰性构建（恰好一次）两个方向的邻接索引。
// 已发布快照的并发首次遍历由 sync.Once 保证安全且只构建一次。
func (s *snapshot) ensureIndexes() {
	s.indexOnce.Do(func() {
		out := make(map[ObjectID][]adjacencyEntry, len(s.objects))
		in := make(map[ObjectID][]adjacencyEntry, len(s.objects))
		for _, link := range s.linksByID {
			out[link.Source] = append(out[link.Source], adjacencyEntry{link: link, to: link.Target})
			in[link.Target] = append(in[link.Target], adjacencyEntry{link: link, to: link.Source})
		}
		for id := range out {
			sort.Slice(out[id], func(i, j int) bool { return entryLess(out[id][i], out[id][j]) })
		}
		for id := range in {
			sort.Slice(in[id], func(i, j int) bool { return entryLess(in[id][i], in[id][j]) })
		}
		s.outgoing = out
		s.incoming = in
	})
}

func entryLess(a, b adjacencyEntry) bool {
	if a.link.Type != b.link.Type {
		return a.link.Type < b.link.Type
	}
	if a.to != b.to {
		return a.to < b.to
	}
	return a.link.ID < b.link.ID
}

// Graph 是并发安全的本体图，支持对象/链接类型/链接的增删以及快照化遍历。
type Graph struct {
	mu  sync.Mutex
	cur atomic.Pointer[snapshot]
	// snapshotHistory 默认关闭（nil）；仅测试通过 enableSnapshotHistory 开启，
	// 用于留存每个已发布快照以验证快照隔离。生产代码路径不会触发追加。
	snapshotHistory []*snapshot
}

// NewGraph 创建一张空图。
func NewGraph() *Graph {
	g := &Graph{}
	g.cur.Store(&snapshot{
		objects:   map[ObjectID]struct{}{},
		linkTypes: map[LinkTypeID]struct{}{},
		linksByID: map[LinkID]Link{},
	})
	return g
}

// SnapshotVersion 返回当前最新快照版本号（单调递增）。
func (g *Graph) SnapshotVersion() uint64 {
	return g.cur.Load().version
}

// AddLinkType 登记一个链接类型。
func (g *Graph) AddLinkType(t LinkTypeID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.cur.Load()
	if _, ok := cur.linkTypes[t]; ok {
		return ErrDuplicateLinkType
	}
	next := cloneShallow(cur)
	next.linkTypes[t] = struct{}{}
	g.commit(next)
	return nil
}

// AddObject 新增一个对象。
func (g *Graph) AddObject(obj Object) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.cur.Load()
	if _, ok := cur.objects[obj.ID]; ok {
		return ErrDuplicateObject
	}
	next := cloneShallow(cur)
	next.objects[obj.ID] = struct{}{}
	g.commit(next)
	return nil
}

// DeleteObject 删除对象；与其相连的链接一并删除。对象不存在时不报错。
func (g *Graph) DeleteObject(id ObjectID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.cur.Load()
	if _, ok := cur.objects[id]; !ok {
		return
	}
	next := cloneShallow(cur)
	delete(next.objects, id)
	for lid, link := range next.linksByID {
		if link.Source == id || link.Target == id {
			delete(next.linksByID, lid)
		}
	}
	g.commit(next)
}

// AddLink 新增一条链接；允许自环（Source == Target），
// 也允许同一对对象之间存在多条不同 LinkID 的平行链接。
func (g *Graph) AddLink(link Link) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.cur.Load()
	if _, ok := cur.linksByID[link.ID]; ok {
		return ErrDuplicateLink
	}
	if _, ok := cur.linkTypes[link.Type]; !ok {
		return ErrLinkTypeMissing
	}
	if _, ok := cur.objects[link.Source]; !ok {
		return ErrObjectMissing
	}
	if _, ok := cur.objects[link.Target]; !ok {
		return ErrObjectMissing
	}
	next := cloneShallow(cur)
	next.linksByID[link.ID] = link
	g.commit(next)
	return nil
}

// DeleteLink 删除一条链接；不存在时不报错。
func (g *Graph) DeleteLink(id LinkID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.cur.Load()
	if _, ok := cur.linksByID[id]; !ok {
		return
	}
	next := cloneShallow(cur)
	delete(next.linksByID, id)
	g.commit(next)
}

// Mutation 描述一批在同一临界区内一次性生效的图结构修改。
// 整批修改原子地产生一个新快照版本，便于高效构建大图或做事务式变更。
type Mutation struct {
	// AddLinkTypes / AddObjects / AddLinks 为新增项。
	AddLinkTypes []LinkTypeID
	AddObjects   []ObjectID
	AddLinks     []Link
	// DeleteLinkIDs / DeleteObjects 为删除项；删除对象会连带删除其相关链接。
	DeleteLinkIDs []LinkID
	DeleteObjects []ObjectID
}

// Batch 在一次原子发布中应用整批修改并返回新版本号。
// 校验规则与单条操作相同：任何一项非法则整批拒绝、图保持不变。
func (g *Graph) Batch(m Mutation) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.cur.Load()
	for _, t := range m.AddLinkTypes {
		if _, ok := cur.linkTypes[t]; ok {
			return 0, ErrDuplicateLinkType
		}
	}
	for _, id := range m.AddObjects {
		if _, ok := cur.objects[id]; ok {
			return 0, ErrDuplicateObject
		}
	}
	for _, link := range m.AddLinks {
		if _, ok := cur.linksByID[link.ID]; ok {
			return 0, ErrDuplicateLink
		}
	}
	next := cloneShallow(cur)
	for _, t := range m.AddLinkTypes {
		next.linkTypes[t] = struct{}{}
	}
	for _, id := range m.AddObjects {
		next.objects[id] = struct{}{}
	}
	for _, id := range m.DeleteObjects {
		delete(next.objects, id)
	}
	for _, lid := range m.DeleteLinkIDs {
		delete(next.linksByID, lid)
	}
	// 删除对象连带删除相关链接。
	if len(m.DeleteObjects) > 0 {
		deleting := make(map[ObjectID]struct{}, len(m.DeleteObjects))
		for _, id := range m.DeleteObjects {
			deleting[id] = struct{}{}
		}
		for lid, link := range next.linksByID {
			if _, ok1 := deleting[link.Source]; ok1 {
				delete(next.linksByID, lid)
				continue
			}
			if _, ok2 := deleting[link.Target]; ok2 {
				delete(next.linksByID, lid)
			}
		}
	}
	// 新增链接的端点校验以「应用删除之后、新增对象之后」的状态为准，
	// 因此在 map 调整完成后再校验并插入。
	for _, link := range m.AddLinks {
		if _, ok := next.linkTypes[link.Type]; !ok {
			return 0, ErrLinkTypeMissing
		}
		if _, ok := next.objects[link.Source]; !ok {
			return 0, ErrObjectMissing
		}
		if _, ok := next.objects[link.Target]; !ok {
			return 0, ErrObjectMissing
		}
		if _, ok := next.linksByID[link.ID]; ok {
			return 0, ErrDuplicateLink
		}
		next.linksByID[link.ID] = link
	}
	g.commit(next)
	return next.version, nil
}

// cloneShallow 复制快照的三张基础 map。新快照不继承邻接索引，
// 索引将在其首次被遍历时惰性重建。
func cloneShallow(cur *snapshot) *snapshot {
	next := &snapshot{
		objects:   make(map[ObjectID]struct{}, len(cur.objects)),
		linkTypes: make(map[LinkTypeID]struct{}, len(cur.linkTypes)),
		linksByID: make(map[LinkID]Link, len(cur.linksByID)+1),
	}
	for id := range cur.objects {
		next.objects[id] = struct{}{}
	}
	for t := range cur.linkTypes {
		next.linkTypes[t] = struct{}{}
	}
	for lid, link := range cur.linksByID {
		next.linksByID[lid] = link
	}
	return next
}

// commit 递增版本号并原子发布新快照；调用方须持有 mu。
func (g *Graph) commit(next *snapshot) {
	next.version = g.cur.Load().version + 1
	if g.snapshotHistory != nil {
		g.snapshotHistory = append(g.snapshotHistory, next)
	}
	g.cur.Store(next)
}

// loadSnapshot 原子读取当前不可变快照。
func (g *Graph) loadSnapshot() *snapshot {
	return g.cur.Load()
}
