// Package cluster 维护滚动升级期间在册节点的注册表。
package cluster

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArg = errors.New("参数非法")
	ErrExists     = errors.New("已存在")
	ErrNotExist   = errors.New("不存在")
)

// Node 是在册节点：Name 非空，Max 为该节点能读的最高格式级别（1..3）。
type Node struct {
	Name string
	Max  int
}

// Registry 是线程安全的节点注册表。
type Registry struct {
	mu    sync.Mutex
	nodes map[string]Node
}

// New 创建空注册表。
func New() *Registry {
	return &Registry{nodes: make(map[string]Node)}
}

// Join 注册节点；name 非空、max 属于 [1,3]，同名报「已存在」。
// Join 总被允许：max 低于集群级别只是调用方语义上的降级节点。
func (r *Registry) Join(name string, max int) error {
	if name == "" || max < 1 || max > 3 {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[name]; ok {
		return ErrExists
	}
	r.nodes[name] = Node{Name: name, Max: max}
	return nil
}

// Leave 按名移除节点，不影响集群级别；不在册报「不存在」。
func (r *Registry) Leave(name string) error {
	if name == "" {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[name]; !ok {
		return ErrNotExist
	}
	delete(r.nodes, name)
	return nil
}

// Get 查询单个节点。
func (r *Registry) Get(name string) (Node, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[name]
	return n, ok
}

// Snapshot 返回按名字字节序升序排列的在册节点副本。
func (r *Registry) Snapshot() []Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sortedNodes(r.nodes)
}

// Len 返回在册节点数。
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.nodes)
}

// MinMax 返回全部在册节点 max 的最小值与是否存在节点。
func (r *Registry) MinMax() (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.nodes) == 0 {
		return 0, false
	}
	min := 4
	for _, n := range r.nodes {
		if n.Max < min {
			min = n.Max
		}
	}
	return min, true
}

func sortedNodes(m map[string]Node) []Node {
	out := make([]Node, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
