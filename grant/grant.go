// Package grant 维护授权节点与转授树（无锁数据结构，由上层加锁）。
package grant

import (
	"sort"
	"strconv"
)

// Node 是一个授权区间节点。深度 0 为根，最大可转授深度为 1。
type Node struct {
	ID       int64
	User     []byte
	Res      []byte
	Start    int64
	End      int64
	Depth    int
	ParentID int64
	Exts     int

	alive bool
}

// Alive 报告节点是否尚未进入回收日志。
func (n *Node) Alive() bool { return n.alive }

// Tree 是全部授权的存储与父子/主体索引。
type Tree struct {
	nodes    map[int64]*Node
	children map[int64][]int64
	active   map[string]int
	byUser   map[string][]int64
}

// NewTree 创建空树。
func NewTree() *Tree {
	return &Tree{
		nodes:    make(map[int64]*Node),
		children: make(map[int64][]int64),
		active:   make(map[string]int),
		byUser:   make(map[string][]int64),
	}
}

func urKey(u, r []byte) string {
	return strconv.Itoa(len(u)) + ":" + string(u) + "/" + strconv.Itoa(len(r)) + ":" + string(r)
}

func userKey(u []byte) string {
	return strconv.Itoa(len(u)) + ":" + string(u)
}

// Get 按编号取节点。
func (t *Tree) Get(id int64) *Node { return t.nodes[id] }

// Children 返回父节点的直接子授权编号（按 id 升序）。
func (t *Tree) Children(id int64) []int64 { return t.children[id] }

// ActiveID 返回 (u,r) 当前活动授权编号，无则 0。
func (t *Tree) ActiveID(u, r []byte) int64 { return int64(t.active[urKey(u, r)]) }

// AddRoot 登记根授权并更新索引。
func (t *Tree) AddRoot(id int64, u, r []byte, start, end int64) *Node {
	n := &Node{ID: id, User: append([]byte(nil), u...), Res: append([]byte(nil), r...),
		Start: start, End: end, alive: true}
	t.nodes[id] = n
	t.active[urKey(n.User, n.Res)] = int(id)
	t.byUser[userKey(n.User)] = append(t.byUser[userKey(n.User)], id)
	return n
}

// AddChild 登记子授权并更新索引。
func (t *Tree) AddChild(id int64, parent *Node, to []byte, start, end int64) *Node {
	n := &Node{ID: id, User: append([]byte(nil), to...), Res: append([]byte(nil), parent.Res...),
		Start: start, End: end, Depth: parent.Depth + 1, ParentID: parent.ID, alive: true}
	t.nodes[id] = n
	t.children[parent.ID] = append(t.children[parent.ID], id)
	t.active[urKey(n.User, n.Res)] = int(id)
	t.byUser[userKey(n.User)] = append(t.byUser[userKey(n.User)], id)
	return n
}

// Invalidate 标记节点失效并同步索引（幂等）。
func (t *Tree) Invalidate(n *Node) {
	if n == nil || !n.alive {
		return
	}
	n.alive = false
	if t.active[urKey(n.User, n.Res)] == int(n.ID) {
		delete(t.active, urKey(n.User, n.Res))
	}
}

// ByUser 返回该主体名下全部授权编号（含已失效），按登记次序。
func (t *Tree) ByUser(u []byte) []int64 { return t.byUser[userKey(u)] }

// Snapshot 按 id 升序返回全部节点。
func (t *Tree) Snapshot() []*Node {
	out := make([]*Node, 0, len(t.nodes))
	for _, n := range t.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].End != out[j].End {
			return out[i].End < out[j].End
		}
		return out[i].ID < out[j].ID
	})
	return out
}
