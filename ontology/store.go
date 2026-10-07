package ontology

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Snapshot 是图在某一确定版本下的不可变只读视图。
type Snapshot interface {
	Version() int64
}

// --- 内部版本化结构：所有历史只追加，读路径与图当前规模无关 ---

type objRevision struct {
	version int64
	obj     Object // Deleted 时为零值
	deleted bool
}

type edgeRevision struct {
	version int64
	deleted bool
}

type edgeKey struct {
	source, target string
	typ            LinkTypeID
}

type linkTypeRevision struct {
	version int64
	cost    int
	deleted bool
}

type aclRevision struct {
	version int64
	// 空集合且非 tombstone 表示“除默认拒绝外无人有权”。
	see      map[string]bool
	traverse map[string]bool
	deleted  bool
}

type revList[T any] struct{ p atomic.Pointer[[]*T] }

func storeRevision[T any](m *sync.Map, key any, rev *T) {
	l, _ := m.LoadOrStore(key, &revList[T]{})
	list := l.(*revList[T])
	old := list.p.Load()
	var next []*T
	if old != nil {
		next = append(*old, rev)
	} else {
		next = []*T{rev}
	}
	list.p.Store(&next)
}

func loadRevisions[T any](m *sync.Map, key any) []*T {
	if l, ok := m.Load(key); ok {
		if p := l.(*revList[T]).p.Load(); p != nil {
			return *p
		}
	}
	return nil
}

type graphState struct {
	version atomic.Int64

	objects sync.Map // string -> *revList[objRevision]

	// 出边邻接表：source -> 每条边的版本化记录。
	adj sync.Map // string(source) -> *sync.Map(edgeKey -> *revList[edgeRevision])

	linkTypes sync.Map // LinkTypeID -> *revList[linkTypeRevision]

	objectACLs sync.Map // string -> *revList[aclRevision]
	linkACLs   sync.Map // edgeKey -> *revList[aclRevision]
}

func newGraphState() *graphState { return &graphState{} }

func revisionAt[T any](revs []*T, version int64, pick func(*T) int64) *T {
	i := sort.Search(len(revs), func(i int) bool { return pick(revs[i]) > version })
	if i == 0 {
		return nil
	}
	return revs[i-1]
}

// snapshotHandle 是某一版本下的只读句柄；遍历过程的访问计数记录在 metrics 中。
type snapshotHandle struct {
	state   *graphState
	version int64

	metrics *Metrics
}

func (h *snapshotHandle) Version() int64 { return h.version }

func (h *snapshotHandle) object(id string) (*Object, bool) {
	h.metrics.ObjectsLoaded++
	rev := revisionAt(loadRevisions[objRevision](&h.state.objects, id), h.version,
		func(r *objRevision) int64 { return r.version })
	if rev == nil || rev.deleted {
		return nil, false
	}
	obj := rev.obj
	return &obj, true
}

// outgoing 返回该快照下某对象全部存活出边（顺序不定，由调用方排序/过滤）。
func (h *snapshotHandle) outgoing(source string) []edgeKey {
	raw, ok := h.state.adj.Load(source)
	if !ok {
		return nil
	}
	bucket := raw.(*sync.Map)
	out := make([]edgeKey, 0)
	bucket.Range(func(keyAny, l any) bool {
		key := keyAny.(edgeKey)
		h.metrics.LinksScanned++
		revs := l.(*revList[edgeRevision]).p.Load()
		var rev *edgeRevision
		if revs != nil {
			rev = revisionAt(*revs, h.version, func(r *edgeRevision) int64 { return r.version })
		}
		if rev != nil && !rev.deleted {
			out = append(out, key)
		}
		return true
	})
	return out
}

// Store 是支持版本化快照与串行化变更的图存储。
type Store struct {
	mu      sync.Mutex // 串行化所有变更，提供线性化点
	current *graphState
}

func NewStore() *Store {
	s := &Store{}
	s.current = newGraphState()
	return s
}

func (s *Store) Current() *snapshotHandle {
	st := s.current
	return &snapshotHandle{state: st, version: st.version.Load(), metrics: &Metrics{}}
}

func (s *Store) Get(version int64) (*snapshotHandle, bool) {
	st := s.current
	if version <= 0 || version > st.version.Load() {
		return nil, false
	}
	return &snapshotHandle{state: st, version: version, metrics: &Metrics{}}, true
}

func (s *snapshotHandle) forkMetrics() *snapshotHandle {
	return &snapshotHandle{state: s.state, version: s.version, metrics: &Metrics{}}
}

// commit 在互斥区内派生新版本；所有变更操作由此串行化（线性化点）。
// 版本号最后发布：读快照 version=V 时，revisionAt 天然忽略版本 > V 的修订，
// 因此续读始终锚定在 token 产生时的快照，后续变更对其不可见。
func (s *Store) commit(mutate func(st *graphState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.current
	mutate(st)
	st.version.Add(1)
}

// --- 对象变更 ---

func (s *Store) PutObject(obj Object) {
	s.commit(func(st *graphState) {
		storeRevision(&st.objects, obj.ID,
			&objRevision{version: st.version.Load() + 1, obj: obj})
	})
}

func (s *Store) DeleteObject(id string) {
	s.commit(func(st *graphState) {
		storeRevision(&st.objects, id,
			&objRevision{version: st.version.Load() + 1, deleted: true})
	})
}

// --- 链接类型变更 ---

func (s *Store) PutLinkType(t LinkType) {
	s.commit(func(st *graphState) {
		storeRevision(&st.linkTypes, t.ID,
			&linkTypeRevision{version: st.version.Load() + 1, cost: t.Cost})
	})
}

func (s *Store) DeleteLinkType(id LinkTypeID) {
	s.commit(func(st *graphState) {
		storeRevision(&st.linkTypes, id,
			&linkTypeRevision{version: st.version.Load() + 1, deleted: true})
	})
}

func (h *snapshotHandle) linkType(id LinkTypeID) (*linkTypeRevision, bool) {
	rev := revisionAt(loadRevisions[linkTypeRevision](&h.state.linkTypes, id), h.version,
		func(r *linkTypeRevision) int64 { return r.version })
	if rev == nil || rev.deleted {
		return nil, false
	}
	return rev, true
}

// --- 链接实例变更（有向） ---

func (s *Store) AddLink(link Link) error {
	if link.Source == "" || link.Target == "" || link.Type == "" {
		return fmt.Errorf("%w: link fields must not be empty", ErrInvalidArgument)
	}
	s.commit(func(st *graphState) {
		key := edgeKey{source: link.Source, target: link.Target, typ: link.Type}
		appendAdj(st, link.Source, key, &edgeRevision{version: st.version.Load() + 1})
	})
	return nil
}

func (s *Store) RemoveLink(link Link) {
	s.commit(func(st *graphState) {
		key := edgeKey{source: link.Source, target: link.Target, typ: link.Type}
		appendAdj(st, link.Source, key, &edgeRevision{version: st.version.Load() + 1, deleted: true})
	})
}

func appendAdj(st *graphState, source string, key edgeKey, rev *edgeRevision) {
	raw, _ := st.adj.LoadOrStore(source, &sync.Map{})
	bucket := raw.(*sync.Map)
	l, _ := bucket.LoadOrStore(key, &revList[edgeRevision]{})
	list := l.(*revList[edgeRevision])
	old := list.p.Load()
	var next []*edgeRevision
	if old != nil {
		next = append(*old, rev)
	} else {
		next = []*edgeRevision{rev}
	}
	list.p.Store(&next)
}

// --- ACL：基于授权表，默认拒绝；策略同样版本化，锚定快照 ---

// ACL 是基于授权表的权限模型实现。
type ACL struct {
	store *Store
}

func NewACL(s *Store) *ACL { return &ACL{store: s} }

// GrantObjectSee 授予 actor 对对象的存在性权限（全量替换该版本的授权集合）。
func (s *Store) GrantObjectSee(id string, actors ...string) {
	s.commit(func(st *graphState) {
		storeRevision(&st.objectACLs, id, &aclRevision{
			version: st.version.Load() + 1,
			see:     toSet(actors),
		})
	})
}

// GrantLinkTraverse 授予 actor 对一条链接的遍历权限。
func (s *Store) GrantLinkTraverse(link Link, actors ...string) {
	s.commit(func(st *graphState) {
		key := edgeKey{source: link.Source, target: link.Target, typ: link.Type}
		storeRevision(&st.linkACLs, key, &aclRevision{
			version:  st.version.Load() + 1,
			traverse: toSet(actors),
		})
	})
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

func (a *ACL) objectPolicy(h *snapshotHandle, id string) *aclRevision {
	h.metrics.ACLChecks++
	return revisionAt(loadRevisions[aclRevision](&a.store.current.objectACLs, id), h.version,
		func(r *aclRevision) int64 { return r.version })
}

func (a *ACL) linkPolicy(h *snapshotHandle, key edgeKey) *aclRevision {
	h.metrics.ACLChecks++
	return revisionAt(loadRevisions[aclRevision](&a.store.current.linkACLs, key), h.version,
		func(r *aclRevision) int64 { return r.version })
}

func (a *ACL) CanSee(snap Snapshot, actor Actor, objectID string) bool {
	h, ok := snap.(*snapshotHandle)
	if !ok {
		return false
	}
	rev := a.objectPolicy(h, objectID)
	return rev != nil && !rev.deleted && rev.see[actor.ID]
}

func (a *ACL) CanTraverse(snap Snapshot, actor Actor, link Link) bool {
	h, ok := snap.(*snapshotHandle)
	if !ok {
		return false
	}
	key := edgeKey{source: link.Source, target: link.Target, typ: link.Type}
	rev := a.linkPolicy(h, key)
	return rev != nil && !rev.deleted && rev.traverse[actor.ID]
}
