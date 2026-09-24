package ontology

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
)

type point struct {
	pos  uint64
	node string
}

// Ring 是一致性哈希环。Locate 可并发调用，Add/Remove 与其并发安全。
type Ring struct {
	mu     sync.RWMutex
	hash   func([]byte) uint64
	vnodes map[string]int
	points []point // 按 (pos, node) 升序
}

type Option func(*Ring)

// WithHash 覆盖默认哈希函数，主要用于测试手工构造环。
func WithHash(h func([]byte) uint64) Option {
	return func(r *Ring) { r.hash = h }
}

func New(opts ...Option) *Ring {
	r := &Ring{hash: defaultHash, vnodes: make(map[string]int)}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func vnodeKey(node string, i int) string {
	return node + "#" + strconv.Itoa(i)
}

func (r *Ring) Add(node string, vnodes int) error {
	if vnodes <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidVnodes, vnodes)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[node]; ok {
		return fmt.Errorf("%w: %q", ErrNodeExists, node)
	}
	r.vnodes[node] = vnodes
	for i := 0; i < vnodes; i++ {
		r.points = append(r.points, point{pos: r.hash([]byte(vnodeKey(node, i))), node: node})
	}
	r.sortPoints()
	return nil
}

func (r *Ring) Remove(node string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[node]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, node)
	}
	delete(r.vnodes, node)
	kept := r.points[:0]
	for _, p := range r.points {
		if p.node != node {
			kept = append(kept, p)
		}
	}
	r.points = kept
	return nil
}

// Locate 返回 key 归属的节点。哈希值大于环上最大位置时回绕到最小位置。
func (r *Ring) Locate(key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.points) == 0 {
		return "", ErrEmptyRing
	}
	h := r.hash([]byte(key))
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i].pos >= h })
	if i == len(r.points) {
		i = 0
	}
	return r.points[i].node, nil
}

// NumNodes 返回当前环上的节点数。
func (r *Ring) NumNodes() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.vnodes)
}

// sortPoints 按位置升序排序；位置相同（哈希碰撞）时按节点 ID 字典序，
// 因此并列打破规则与插入顺序无关。
func (r *Ring) sortPoints() {
	sort.Slice(r.points, func(i, j int) bool {
		if r.points[i].pos != r.points[j].pos {
			return r.points[i].pos < r.points[j].pos
		}
		return r.points[i].node < r.points[j].node
	})
}
