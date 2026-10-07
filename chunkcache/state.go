package chunkcache

import (
	"container/list"
	"fmt"
)

// chunkKey 唯一标识一个切片：对象键 + 版本 + 下标。
type chunkKey struct {
	key     string
	version string
	index   int
}

// pinKey 只按对象键与下标引用（版本无关，见 DESIGN.md“pin 与版本解耦”）。
type pinKey struct {
	key   string
	index int
}

// chunkEntry 为缓存中的一个切片。
type chunkEntry struct {
	id   chunkKey
	data []byte
	elem *list.Element
	gen  int64 // 放入缓存时的全局回源代际
}

// objectMeta 记录某对象键当前已知的版本与总长度。
type objectMeta struct {
	version string
	length  int64
}

// state 是缓存全部可变状态，任何访问都须持有 Cache.mu。
type state struct {
	// chunks 为已缓存切片；map 命中/插入/删除均为 O(1) 均摊。
	chunks map[chunkKey]*chunkEntry
	// meta 为每对象键的当前版本/长度；条目仅由回源建立。
	meta map[string]objectMeta
	// lru 链表头为最近访问，尾为最久未访问；元素值为 *chunkEntry。
	lru *list.List
	// pins 记录每个 (key,index) 被多少未完成请求引用；>0 不得淘汰。
	pins map[pinKey]int
	// present 为按 pinKey 聚合的版本集合，支持 O(1) 判断某下标是否有任意版本缓存。
	present map[pinKey]map[string]struct{}

	// gen 为单调递增的回源代际；每次成功的源站返回递增一次。
	gen int64
}

func newState() *state {
	return &state{
		chunks:  make(map[chunkKey]*chunkEntry),
		meta:    make(map[string]objectMeta),
		lru:     list.New(),
		pins:    make(map[pinKey]int),
		present: make(map[pinKey]map[string]struct{}),
	}
}

// getLocked 命中即刷新 LRU 到最前（命中判定开销与缓存切片总数无关）。
func (s *state) getLocked(id chunkKey) (*chunkEntry, bool) {
	e, ok := s.chunks[id]
	if !ok {
		return nil, false
	}
	s.lru.MoveToFront(e.elem)
	return e, true
}

// peekLocked 读取条目但不刷新 LRU（供一次请求收集数据阶段使用）。
func (s *state) peekLocked(id chunkKey) (*chunkEntry, bool) {
	e, ok := s.chunks[id]
	return e, ok
}

// hasLocked 只判断是否存在，不刷新访问顺序（用于规划期快照判定）。
func (s *state) hasLocked(id chunkKey) bool {
	_, ok := s.chunks[id]
	return ok
}

// pinLocked / unpinLocked 维护在途引用计数。
func (s *state) pinLocked(k pinKey) { s.pins[k]++ }

func (s *state) unpinLocked(k pinKey) {
	n := s.pins[k]
	if n <= 1 {
		delete(s.pins, k)
	} else {
		s.pins[k] = n - 1
	}
}

func (s *state) pinnedLocked(k pinKey) bool { return s.pins[k] > 0 }

// canAdmitLocked 判断本请求是否可被接纳。
//
// 不变式：请求一旦接纳，其所需切片要么已驻留缓存，要么被 pin（在途），
// 最终都会驻留。因此接纳条件是下列两类切片的并集大小不超过 C：
//   - 当前已缓存切片（len(chunks)，含可能已过期但未清理的旧版本）
//   - 已被 pin 但尚未入库的飞行中切片，加上本请求新增 pin 的切片
//
// need 为本请求覆盖的全部 (key,index)；本函数成功时会把其中未 pin
// 的下标加入 pin 集合（原子完成“判定+占位”，拒绝则什么都不改变）。
func (s *state) canAdmitLocked(c int, need map[pinKey]struct{}) bool {
	// “为完成一次请求必须同时持有的切片数”即其 span 大小（该 span 在请求
	// 结束前通过 pin 保证不被淘汰）。span 外的驻留切片可由 LRU 淘汰腾位，
	// 其他在途请求的 pin 只阻止其自身切片被淘汰，不扩大本请求的 span 需求
	// （并发结果等价于某串行顺序：它们完成后即释放）。
	if len(need) > c {
		return false
	}
	return true
}

// presentLocked 判断某 (key,index) 是否有任意版本的缓存切片（O(1)）。
func (s *state) presentLocked(k pinKey) bool { return len(s.present[k]) > 0 }

// reserveLocked 为 n 个即将插入的新切片一次性腾出空位（只淘汰未 pin 的最旧片）。
// 返回可直接插入的新切片数。调用方保证通过容量门控。
func (s *state) reserveLocked(c int, n int, protected map[chunkKey]bool) {
	target := s.lru.Len() + n
	for target > c {
		victim := s.evictExceptLocked(protected)
		if !victim {
			panic(fmt.Sprintf("chunkcache: cannot reserve: len=%d n=%d c=%d protected=%d pins=%d",
				s.lru.Len(), n, c, len(protected), len(s.pins)))
		}
		target--
	}
}

// evictExceptLocked 淘汰最久未访问、既未被 pin 也不在 protected 中的切片。
func (s *state) evictExceptLocked(protected map[chunkKey]bool) bool {
	for el := s.lru.Back(); el != nil; el = el.Prev() {
		e := el.Value.(*chunkEntry)
		pk := pinKey{key: e.id.key, index: e.id.index}
		if s.pinnedLocked(pk) || protected[e.id] {
			continue
		}
		s.lru.Remove(el)
		delete(s.chunks, e.id)
		if vs := s.present[pk]; vs != nil {
			delete(vs, e.id.version)
			if len(vs) == 0 {
				delete(s.present, pk)
			}
		}
		return true
	}
	return false
}

// insertLocked 插入一个已确保有空位的新切片（不做淘汰）。
func (s *state) insertLocked(id chunkKey, data []byte, gen int64) {
	if e, ok := s.chunks[id]; ok {
		e.data = data
		s.lru.MoveToFront(e.elem)
		return
	}
	e := &chunkEntry{id: id, data: data, gen: gen}
	e.elem = s.lru.PushFront(e)
	s.chunks[id] = e
	pk := pinKey{key: id.key, index: id.index}
	vs := s.present[pk]
	if vs == nil {
		vs = make(map[string]struct{})
		s.present[pk] = vs
	}
	vs[id.version] = struct{}{}
}

// insertBackLocked 把新切片挂到 LRU 尾部（不视为最近访问），
// 供 fillSpan 批量安装：最后由 reorderSpanLocked 按响应顺序统一置顶。
func (s *state) insertBackLocked(id chunkKey, data []byte, gen int64) {
	if e, ok := s.chunks[id]; ok {
		e.data = data
		return
	}
	e := &chunkEntry{id: id, data: data, gen: gen}
	e.elem = s.lru.PushBack(e)
	s.chunks[id] = e
	pk := pinKey{key: id.key, index: id.index}
	vs := s.present[pk]
	if vs == nil {
		vs = make(map[string]struct{})
		s.present[pk] = vs
	}
	vs[id.version] = struct{}{}
}

// reorderSpanLocked 按 [first,last] 升序依次置顶；较大下标最后置顶，
// 故最终 LRU 前部顺序为下标降序（确定性，等价于按响应顺序访问）。
func (s *state) reorderSpanLocked(key, version string, first, last int) {
	for i := first; i <= last; i++ {
		if e, ok := s.chunks[chunkKey{key: key, version: version, index: i}]; ok {
			s.lru.MoveToFront(e.elem)
		}
	}
}

// invalidateKeyLocked 作废某对象键旧版本的全部切片。
// 被在途请求 pin 的切片条目也直接移除（pin 引用的是下标而非版本），
// 这样旧版本数据绝不可能在版本切换后被读出。
func (s *state) invalidateKeyLocked(key string) int {
	removed := 0
	for id, e := range s.chunks {
		if id.key != key {
			continue
		}
		s.lru.Remove(e.elem)
		delete(s.chunks, id)
		pk := pinKey{key: key, index: id.index}
		if vs := s.present[pk]; vs != nil {
			delete(vs, id.version)
			if len(vs) == 0 {
				delete(s.present, pk)
			}
		}
		removed++
	}
	return removed
}
