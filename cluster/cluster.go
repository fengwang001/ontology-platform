package cluster

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalid  = errors.New("参数非法")
	ErrExists   = errors.New("已存在")
	ErrNoNode   = errors.New("无节点")
	ErrNotFound = errors.New("不存在")
)

type Node struct {
	Name string
	Max  int
}

type Registry struct {
	mu    sync.RWMutex
	nodes map[string]int
}

func New() *Registry {
	return &Registry{nodes: make(map[string]int)}
}

// Join 注册节点；max 为该节点能读的最高格式级别。Join 总被允许，
// 即使 max 低于当前集群级别（调用方据此识别降级节点）。
func (r *Registry) Join(name string, max int) error {
	if name == "" || max < 1 || max > 3 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[name]; ok {
		return ErrExists
	}
	r.nodes[name] = max
	return nil
}

func (r *Registry) Leave(name string) error {
	if name == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[name]; !ok {
		return ErrNotFound
	}
	delete(r.nodes, name)
	return nil
}

func (r *Registry) Get(name string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	max, ok := r.nodes[name]
	return Node{Name: name, Max: max}, ok
}

// Snapshot 返回按名字字节序升序排列的在册节点副本。
func (r *Registry) Snapshot() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, 0, len(r.nodes))
	for name, max := range r.nodes {
		out = append(out, Node{Name: name, Max: max})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MinMax 返回全部在册节点 max 的最小值，以及取得该最小值的节点中
// 名字字节序最小者；无节点时 ok=false。
func (r *Registry) MinMax() (minMax int, minName string, ok bool) {
	nodes := r.Snapshot()
	if len(nodes) == 0 {
		return 0, "", false
	}
	minMax = nodes[0].Max
	minName = nodes[0].Name
	for _, n := range nodes[1:] {
		if n.Max < minMax || (n.Max == minMax && n.Name < minName) {
			minMax = n.Max
			minName = n.Name
		}
	}
	return minMax, minName, true
}
