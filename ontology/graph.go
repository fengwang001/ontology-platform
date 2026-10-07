package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Link 是本体图中的一条有向链接。
type Link struct {
	// From 起点对象。
	From ObjectID `json:"from"`
	// To 终点对象。
	To ObjectID `json:"to"`
	// Type 链接类型。
	Type LinkTypeID `json:"type"`
	// Cost 遍历代价，必须为正数。
	Cost int64 `json:"cost"`
}

// Graph 是本体链接图。对象与链接可并发增删，查询期间通过读锁
// 获得一致视图；邻接表按（终点, 链接类型, 代价）排序以保证遍历
// 顺序确定。
type Graph struct {
	mu      sync.RWMutex
	objects map[ObjectID]ObjectTypeID
	adj     map[ObjectID][]Link
}

// NewGraph 创建空图。
func NewGraph() *Graph {
	return &Graph{
		objects: make(map[ObjectID]ObjectTypeID),
		adj:     make(map[ObjectID][]Link),
	}
}

// AddObject 添加或覆盖一个对象及其类型。
func (g *Graph) AddObject(id ObjectID, objType ObjectTypeID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.objects[id] = objType
}

// AddLink 添加一条有向链接，两端对象必须已存在且代价为正。
func (g *Graph) AddLink(link Link) error {
	if link.Cost <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidCost, link.Cost)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[link.From]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownObject, string(link.From))
	}
	if _, ok := g.objects[link.To]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownObject, string(link.To))
	}
	g.adj[link.From] = append(g.adj[link.From], link)
	g.sortOutgoingLocked(link.From)
	return nil
}

// sortOutgoingLocked 对指定对象的出边排序，保证遍历顺序确定。
func (g *Graph) sortOutgoingLocked(id ObjectID) {
	links := g.adj[id]
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].To != links[j].To {
			return links[i].To < links[j].To
		}
		if links[i].Type != links[j].Type {
			return links[i].Type < links[j].Type
		}
		return links[i].Cost < links[j].Cost
	})
}

// objectType 返回对象类型及对象是否存在。
func (g *Graph) objectType(id ObjectID) (ObjectTypeID, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	t, ok := g.objects[id]
	return t, ok
}

// outgoing 返回对象出边的拷贝，调用期间图可被并发修改。
func (g *Graph) outgoing(id ObjectID) []Link {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]Link(nil), g.adj[id]...)
}
