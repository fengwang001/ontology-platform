package ontology

import (
	"sort"
	"strconv"
	"sync"
)

// Ring 是一致性哈希环。节点以字符串 ID 标识，
// 每个节点在环上放置 vnodes 个虚拟节点。
// 零值不可用，请用 New 构造。Ring 可安全并发使用。
type Ring struct {
	mu     sync.RWMutex
	hash   HashFunc
	vnodes map[string]int // 节点 ID -> 虚拟节点数
	points map[uint64]string
	sorted []uint64 // points 的键，升序
}

// Option 用于定制 Ring。
type Option func(*Ring)

// WithHash 指定哈希函数（主要用于测试构造环绕与碰撞场景）。
// 不传时使用确定性的 FNV-1a 64。
func WithHash(h HashFunc) Option {
	return func(r *Ring) { r.hash = h }
}

// New 创建一个空环。
func New(opts ...Option) *Ring {
	r := &Ring{
		hash:   defaultHash,
		vnodes: make(map[string]int),
		points: make(map[uint64]string),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// vnodeKey 生成第 i 个虚拟节点的哈希输入。
func vnodeKey(id string, i int) string {
	return id + "#" + strconv.Itoa(i)
}

// Add 把节点 id 及其 vnodes 个虚拟节点加入环。
// vnodes <= 0 返回 ErrInvalidVnodes；id 已存在返回 ErrNodeExists，
// 且环保持不变。
func (r *Ring) Add(id string, vnodes int) error {
	if vnodes <= 0 {
		return ErrInvalidVnodes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[id]; ok {
		return ErrNodeExists
	}
	r.vnodes[id] = vnodes
	r.addVnodesLocked(id, vnodes)
	r.rebuildSortedLocked()
	return nil
}

// addVnodesLocked 把节点的虚拟节点写入 points。
// 位置碰撞时保留节点 ID 字典序更小者，与插入顺序无关。
func (r *Ring) addVnodesLocked(id string, vnodes int) {
	for i := 0; i < vnodes; i++ {
		pos := r.hash(vnodeKey(id, i))
		if cur, ok := r.points[pos]; !ok || id < cur {
			r.points[pos] = id
		}
	}
}

// rebuildSortedLocked 从 points 重建升序位置切片。
func (r *Ring) rebuildSortedLocked() {
	r.sorted = make([]uint64, 0, len(r.points))
	for pos := range r.points {
		r.sorted = append(r.sorted, pos)
	}
	sort.Slice(r.sorted, func(i, j int) bool { return r.sorted[i] < r.sorted[j] })
}

// Remove 把节点 id 及其全部虚拟节点从环上删除。
// 节点不存在返回 ErrNodeNotFound。
func (r *Ring) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[id]; !ok {
		return ErrNodeNotFound
	}
	delete(r.vnodes, id)
	// 全量重建，保证碰撞位置的所有权在删除后仍然确定、
	// 且与历史插入顺序无关。
	r.points = make(map[uint64]string, len(r.points))
	for nid, n := range r.vnodes {
		r.addVnodesLocked(nid, n)
	}
	r.rebuildSortedLocked()
	return nil
}

// Locate 返回 key 应归属的节点 ID。
// 沿环顺时针找到第一个位置 >= hash(key) 的虚拟节点；
// 若 hash(key) 大于最大位置则回绕到最小位置。
// 空环返回 ErrEmptyRing。
func (r *Ring) Locate(key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.sorted) == 0 {
		return "", ErrEmptyRing
	}
	h := r.hash(key)
	i := sort.Search(len(r.sorted), func(i int) bool { return r.sorted[i] >= h })
	if i == len(r.sorted) {
		i = 0 // 回绕到最小位置
	}
	return r.points[r.sorted[i]], nil
}

// Size 返回环上虚拟节点的数量（碰撞位置只计一次）。
func (r *Ring) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sorted)
}

// Nodes 返回当前环上所有节点 ID（升序）。
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.vnodes))
	for id := range r.vnodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
