// Package graph 提供本体平台的对象-链接图存储，支持遍历开始时的一致性快照。
package graph

import "sync"

// Direction 表示遍历沿链接行进的方向。
type Direction string

const (
	Outgoing Direction = "outgoing"
	Incoming Direction = "incoming"
)

// Object 是图中的一个本体对象。
type Object struct {
	ID   string
	Type string
}

// Link 是两个对象之间的一条有类型有向链接。
type Link struct {
	ID       string
	Type     string
	SourceID string
	TargetID string
}

// Store 是线程安全的图存储。遍历通过 Snapshot 获得一致性视图。
type Store struct {
	mu      sync.RWMutex
	objects map[string]Object
	links   map[string]Link
	// out: sourceID -> links; in: targetID -> links
	out     map[string][]Link
	in      map[string][]Link
	version uint64
}

// NewStore 返回一个空图存储。
func NewStore() *Store {
	return &Store{
		objects: make(map[string]Object),
		links:   make(map[string]Link),
		out:     make(map[string][]Link),
		in:      make(map[string][]Link),
	}
}

// Snapshot 是遍历开始时图结构的一个确定的一致性视图。
// 快照上的遍历结果与并发修改无关。
type Snapshot struct {
	objects map[string]Object
	out     map[string][]Link
	in      map[string][]Link
	version uint64
}
