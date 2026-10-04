package grant

import "container/heap"

// ID 是系统按 1,2,3… 顺序分配的授权编号；被拒绝的操作不占号。
type ID int64

// Key 是 (主体, 数据集) 对。
type Key struct {
	U, R string
}

// Grant 是一条授权（根授权深度 0）。
type Grant struct {
	ID      ID
	U, R    string
	Start   int64
	End     int64
	Parent  ID // 根授权为 0
	Depth   int
	Kids    []ID // 按创建先后（ID 升序）
	Extends int  // 已延长次数（仅根授权可非 0）
	Dead    bool // 已在 Reaped 中落地失效
}

// Store 是授权记录、转授树、活跃索引与到期最小堆。
type Store struct {
	byID    map[ID]*Grant
	active  map[Key]ID
	userIDs map[string][]ID
	ends    *endHeap
	next    ID
}

func NewStore() *Store {
	return &Store{
		byID:    map[ID]*Grant{},
		active:  map[Key]ID{},
		userIDs: map[string][]ID{},
		ends:    &endHeap{},
	}
}

// Create 分配新编号并登记授权（含全部索引与到期堆）。
func (s *Store) Create(u, r string, start, end int64, parent ID, depth int) *Grant {
	s.next++
	g := &Grant{ID: s.next, U: u, R: r, Start: start, End: end, Parent: parent, Depth: depth}
	s.byID[g.ID] = g
	s.active[Key{u, r}] = g.ID
	s.userIDs[u] = append(s.userIDs[u], g.ID)
	s.ends.push(heapEntry{end: end, id: g.ID})
	if parent != 0 {
		p := s.byID[parent]
		p.Kids = append(p.Kids, g.ID)
	}
	return g
}

// Get 返回授权记录（含已失效者）；不存在返回 nil。
func (s *Store) Get(id ID) *Grant { return s.byID[id] }

// Active 返回 (u,r) 上索引中的授权编号（可能已到 end 但尚未落地）。
func (s *Store) Active(u, r string) (ID, bool) {
	id, ok := s.active[Key{u, r}]
	return id, ok
}

// LiveAt 沿父链判断授权在时刻 t 是否“有效视图”：未落地失效、end>t 且所有祖先同样有效。
func (s *Store) LiveAt(id ID, t int64) *Grant {
	g := s.byID[id]
	if g == nil {
		return nil
	}
	for cur := g; cur != nil; cur = s.byID[cur.Parent] {
		if cur.Dead || cur.End <= t {
			return nil
		}
		if cur.Parent == 0 {
			break
		}
	}
	return g
}

// LiveCountAt 返回主体 u 在时刻 t 的有效授权数（逻辑视图，不落地）。
func (s *Store) LiveCountAt(u string, t int64) int {
	n := 0
	for _, id := range s.userIDs[u] {
		if s.LiveAt(id, t) != nil {
			n++
		}
	}
	return n
}

// PopDue 在堆顶存在 end≤t 的有效条目时弹出其授权编号。
// 延长产生的旧条目与已落地条目不匹配当前记录，直接丢弃。
func (s *Store) PopDue(t int64) (ID, int64, bool) {
	for s.ends.Len() > 0 {
		top := (*s.ends)[0]
		if top.end > t {
			return 0, 0, false
		}
		heap.Pop(s.ends)
		g := s.byID[top.id]
		if g == nil || g.Dead || g.End != top.end {
			continue
		}
		return g.ID, top.end, true
	}
	return 0, 0, false
}

// MarkDead 标记落地失效并清理活跃索引。
func (s *Store) MarkDead(g *Grant) {
	if g.Dead {
		return
	}
	g.Dead = true
	if s.active[Key{g.U, g.R}] == g.ID {
		delete(s.active, Key{g.U, g.R})
	}
}

// LiveDescendants 返回 g 仍有效（未落地）的后代，前序 DFS，子按 ID 升序、孙紧随其子。
func (s *Store) LiveDescendants(g *Grant) []*Grant {
	out := make([]*Grant, 0)
	var walk func(*Grant)
	walk = func(p *Grant) {
		for _, kid := range p.Kids {
			k := s.byID[kid]
			if k == nil || k.Dead {
				continue
			}
			out = append(out, k)
			walk(k)
		}
	}
	walk(g)
	return out
}

// ChangeEnd 延长根授权，并向堆中追加新的到期条目（旧条目自然作废）。
func (s *Store) ChangeEnd(g *Grant, end int64) {
	g.End = end
	s.ends.push(heapEntry{end: end, id: g.ID})
}

type heapEntry struct {
	end int64
	id  ID
}

type endHeap []heapEntry

func (h endHeap) Len() int { return len(h) }
func (h endHeap) Less(i, j int) bool {
	if h[i].end != h[j].end {
		return h[i].end < h[j].end
	}
	return h[i].id < h[j].id
}
func (h endHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *endHeap) Push(x any)   { *h = append(*h, x.(heapEntry)) }
func (h *endHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func (h *endHeap) push(e heapEntry) {
	heap.Push(h, e)
}
