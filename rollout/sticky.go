package rollout

import "container/list"

// stickyEntry 是一条会话粘性记录：标识最近一次被路由的版本与时刻。
type stickyEntry struct {
	id      string
	version Version
	atMs    int64
	seq     uint64 // 路由序号，用于精确淘汰最久未被路由的记录
}

// stickyStore 用 map + 双向链表维护粘性记录。
// 链表前端为最近被路由的记录，尾端为最久未被路由的记录。
// 查找、刷新、插入、淘汰均为 O(1)，且只保留至多 maxEntries 条，
// 因此 Route 的开销不随历史请求总数增长。
type stickyStore struct {
	maxEntries int
	ll         *list.List               // front: MRU, back: LRU
	index      map[string]*list.Element // id -> *list.Element of *stickyEntry
	now        int64                    // 最近一次路由时间
	ttl        int64                    // L
	seq        uint64
}

func newStickyStore(maxEntries int, ttlMs int64) *stickyStore {
	return &stickyStore{
		maxEntries: maxEntries,
		ll:         list.New(),
		index:      make(map[string]*list.Element),
		ttl:        ttlMs,
	}
}

// lookup 返回未过期（左闭右开 [now-L, now)）的粘性版本；过期视为不存在。
// 纯读取不改变 LRU 顺序：按规范，过期记录不会被“沿用”，
// 而随后的路由会以全新归属刷新该记录。
func (m *stickyStore) lookup(id string, nowMs int64) (Version, bool) {
	e, ok := m.index[id]
	if !ok {
		return VersionStable, false
	}
	en := e.Value.(*stickyEntry)
	if nowMs >= en.atMs && nowMs-en.atMs < m.ttl {
		return en.version, true
	}
	return VersionStable, false
}

// touch 记录或刷新标识的最近路由版本与时刻。
// 若条数将超过上限，淘汰链表尾端（最久未被路由）的记录。
func (m *stickyStore) touch(id string, v Version, nowMs int64) {
	if m.maxEntries == 0 {
		return
	}
	m.seq++
	if e, ok := m.index[id]; ok {
		en := e.Value.(*stickyEntry)
		en.version = v
		en.atMs = nowMs
		en.seq = m.seq
		m.ll.MoveToFront(e)
		return
	}
	en := &stickyEntry{id: id, version: v, atMs: nowMs, seq: m.seq}
	e := m.ll.PushFront(en)
	m.index[id] = e
	for m.ll.Len() > m.maxEntries {
		oldest := m.ll.Back()
		old := oldest.Value.(*stickyEntry)
		m.ll.Remove(oldest)
		delete(m.index, old.id)
	}
}

func (m *stickyStore) clear() {
	m.ll.Init()
	m.index = make(map[string]*list.Element)
}

func (m *stickyStore) len() int { return m.ll.Len() }
