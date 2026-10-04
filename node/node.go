// Package node 持有集群的唯一可变状态：节点、索引、分片位置与磁盘用量。
package node

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrInvalid   = errors.New("invalid argument")
	ErrExists    = errors.New("already exists")
	ErrNotFound  = errors.New("not found")
	ErrShardGone = errors.New("shard not found")
)

const (
	MaxName = 64
	MaxSize = int64(1_000_000_000_000)
)

type node struct {
	id      string
	zone    string
	total   int64
	other   int64
	used    int64
	exclude bool
	copies  map[string]map[int]bool
	copyCnt int
}

type shard struct {
	primary  string
	replicas map[string]struct{}
}

type index struct {
	shards []*shard
	r      int
	size   int64
}

// Cluster 是分配决策器操作的集群状态。
type Cluster struct {
	mu    sync.Mutex
	L, H  int
	nodes map[string]*node
	order []string
	zones map[string]struct{}
	idx   map[string]*index
}

// New 创建集群，L、H 为低、高水位百分数，要求 1 ≤ L ≤ H ≤ 100。
func New(L, H int) (*Cluster, error) {
	if L < 1 || H < L || H > 100 {
		return nil, fmt.Errorf("%w: watermarks require 1 <= L <= H <= 100, got L=%d H=%d",
			ErrInvalid, L, H)
	}
	return &Cluster{
		L: L, H: H,
		nodes: map[string]*node{},
		zones: map[string]struct{}{},
		idx:   map[string]*index{},
	}, nil
}

func validName(s string) bool {
	return len(s) >= 1 && len(s) <= MaxName
}

// AddNode 加入节点；id、zone 为 1..64 字节，total 为 1..10^12。
func (c *Cluster) AddNode(id, zone string, total int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validName(id) || !validName(zone) || total < 1 || total > MaxSize {
		return fmt.Errorf("%w: AddNode requires id/zone of 1..%d bytes and total of 1..%d",
			ErrInvalid, MaxName, MaxSize)
	}
	if _, ok := c.nodes[id]; ok {
		return fmt.Errorf("%w: node %q", ErrExists, id)
	}
	c.nodes[id] = &node{id: id, zone: zone, total: total, copies: map[string]map[int]bool{}}
	c.order = append(c.order, id)
	c.zones[zone] = struct{}{}
	return nil
}

// SetOther 设置非分片占用（0..10^12，可超过 total）。
func (c *Cluster) SetOther(id string, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validName(id) || v < 0 || v > MaxSize {
		return fmt.Errorf("%w: SetOther requires id of 1..%d bytes and v of 0..%d",
			ErrInvalid, MaxName, MaxSize)
	}
	n, ok := c.nodes[id]
	if !ok {
		return fmt.Errorf("%w: node %q", ErrNotFound, id)
	}
	n.used += v - n.other
	n.other = v
	return nil
}

// SetExclude 标记/取消排除。
func (c *Cluster) SetExclude(id string, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validName(id) {
		return fmt.Errorf("%w: SetExclude requires id of 1..%d bytes", ErrInvalid, MaxName)
	}
	n, ok := c.nodes[id]
	if !ok {
		return fmt.Errorf("%w: node %q", ErrNotFound, id)
	}
	n.exclude = on
	return nil
}

// CreateIndex 创建索引：S 分片（1..64）、r 副本（0..5）、每份 size（1..10^12）；初始全部未分配。
func (c *Cluster) CreateIndex(name string, S, r int, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validName(name) || S < 1 || S > MaxName || r < 0 || r > 5 || size < 1 || size > MaxSize {
		return fmt.Errorf("%w: CreateIndex requires name 1..%d bytes, S 1..%d, r 0..5, size 1..%d",
			ErrInvalid, MaxName, MaxName, MaxSize)
	}
	if _, ok := c.idx[name]; ok {
		return fmt.Errorf("%w: index %q", ErrExists, name)
	}
	idx := &index{shards: make([]*shard, S), r: r, size: size}
	for i := range idx.shards {
		idx.shards[i] = &shard{replicas: map[string]struct{}{}}
	}
	c.idx[name] = idx
	return nil
}

// Lock/Unlock 暴露给 alloc，使其多步编排等价于某个串行顺序。
func (c *Cluster) Lock()   { c.mu.Lock() }
func (c *Cluster) Unlock() { c.mu.Unlock() }

// SortedNodeIDs 返回按 id 字节序排列的全部节点 id（调用方持锁）。
func (c *Cluster) SortedNodeIDs() []string {
	ids := append(make([]string, 0, len(c.order)), c.order...)
	sort.Strings(ids)
	return ids
}

// SortedIndexNames 返回按名字节序排列的全部索引名（调用方持锁）。
func (c *Cluster) SortedIndexNames() []string {
	names := make([]string, 0, len(c.idx))
	for name := range c.idx {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Cluster) nodeLocked(id string) (*node, error) {
	n, ok := c.nodes[id]
	if !ok {
		return nil, fmt.Errorf("%w: node %q", ErrNotFound, id)
	}
	return n, nil
}

func (c *Cluster) indexLocked(name string) (*index, error) {
	idx, ok := c.idx[name]
	if !ok {
		return nil, fmt.Errorf("%w: index %q", ErrNotFound, name)
	}
	return idx, nil
}

func (c *Cluster) shardLocked(name string, s int) (*index, *shard, error) {
	idx, err := c.indexLocked(name)
	if err != nil {
		return nil, nil, err
	}
	if s < 0 || s >= len(idx.shards) {
		return nil, nil, fmt.Errorf("%w: %s shard %d", ErrShardGone, name, s)
	}
	return idx, idx.shards[s], nil
}

// ShardSpec 返回分片数、副本数、份大小（调用方持锁）。
func (c *Cluster) ShardSpec(name string) (S, r int, size int64, err error) {
	idx, err := c.indexLocked(name)
	if err != nil {
		return 0, 0, 0, err
	}
	return len(idx.shards), idx.r, idx.size, nil
}

// Primary 返回主所在节点；未分配为 ""（调用方持锁）。
func (c *Cluster) Primary(name string, s int) (string, error) {
	_, sh, err := c.shardLocked(name, s)
	if err != nil {
		return "", err
	}
	return sh.primary, nil
}

// ReplicaNodes 返回当前持有副本的节点集合（调用方持锁）。
func (c *Cluster) ReplicaNodes(name string, s int) (map[string]struct{}, int64, error) {
	idx, sh, err := c.shardLocked(name, s)
	if err != nil {
		return nil, 0, err
	}
	out := make(map[string]struct{}, len(sh.replicas))
	for nid := range sh.replicas {
		out[nid] = struct{}{}
	}
	return out, idx.size, nil
}

func (n *node) addCopy(name string, s int, size int64) {
	set := n.copies[name]
	if set == nil {
		set = map[int]bool{}
		n.copies[name] = set
	}
	if !set[s] {
		set[s] = true
		n.copyCnt++
		n.used += size
	}
}

func (n *node) removeCopy(name string, s int, size int64) {
	if set, ok := n.copies[name]; ok && set[s] {
		delete(set, s)
		if len(set) == 0 {
			delete(n.copies, name)
		}
		n.copyCnt--
		n.used -= size
	}
}

// AssignPrimary 把未分配的主放到 dst（调用方持锁）。
func (c *Cluster) AssignPrimary(name string, s int, dst string) error {
	idx, sh, err := c.shardLocked(name, s)
	if err != nil {
		return err
	}
	n, err := c.nodeLocked(dst)
	if err != nil {
		return err
	}
	if sh.primary != "" {
		return fmt.Errorf("%w: %s/%d primary already assigned", ErrExists, name, s)
	}
	sh.primary = dst
	n.addCopy(name, s, idx.size)
	return nil
}

// AssignReplica 增加一副本到 dst（调用方持锁）。
func (c *Cluster) AssignReplica(name string, s int, dst string) error {
	idx, sh, err := c.shardLocked(name, s)
	if err != nil {
		return err
	}
	n, err := c.nodeLocked(dst)
	if err != nil {
		return err
	}
	if _, ok := sh.replicas[dst]; ok {
		return fmt.Errorf("%w: %s/%d replica already on %q", ErrExists, name, s, dst)
	}
	sh.replicas[dst] = struct{}{}
	n.addCopy(name, s, idx.size)
	return nil
}

// MoveCopy 把一份从 src 迁到 dst，主副身份不变（调用方持锁）。
func (c *Cluster) MoveCopy(name string, s int, src, dst string, primary bool) error {
	idx, sh, err := c.shardLocked(name, s)
	if err != nil {
		return err
	}
	sn, err := c.nodeLocked(src)
	if err != nil {
		return err
	}
	dn, err := c.nodeLocked(dst)
	if err != nil {
		return err
	}
	if primary {
		if sh.primary != src {
			return fmt.Errorf("%w: %s/%d primary not on %q", ErrNotFound, name, s, src)
		}
		sh.primary = dst
	} else {
		if _, ok := sh.replicas[src]; !ok {
			return fmt.Errorf("%w: %s/%d replica not on %q", ErrNotFound, name, s, src)
		}
		delete(sh.replicas, src)
		sh.replicas[dst] = struct{}{}
	}
	sn.removeCopy(name, s, idx.size)
	dn.addCopy(name, s, idx.size)
	return nil
}

// CopyCount 返回节点上的分片份数（调用方持锁）。
func (c *Cluster) CopyCount(nid string) (int, error) {
	n, err := c.nodeLocked(nid)
	if err != nil {
		return 0, err
	}
	return n.copyCnt, nil
}

// NodeView 返回决策器需要的节点只读属性（调用方持锁）。
func (c *Cluster) NodeView(nid string) (zone string, used, total int64, excluded bool, err error) {
	n, err := c.nodeLocked(nid)
	if err != nil {
		return "", 0, 0, false, err
	}
	return n.zone, n.used, n.total, n.exclude, nil
}

// ZoneCount 返回当前全部节点（含被排除者）的不同区域数（调用方持锁）。
func (c *Cluster) ZoneCount() int { return len(c.zones) }

// HasShardCopy 报告节点上是否已有该分片的任意一份（调用方持锁）。
func (c *Cluster) HasShardCopy(nid, name string, s int) (bool, error) {
	n, err := c.nodeLocked(nid)
	if err != nil {
		return false, err
	}
	if _, _, err := c.shardLocked(name, s); err != nil {
		return false, err
	}
	return n.copies[name][s], nil
}

// ZoneCopyCount 统计某区域内该分片已有份数（调用方持锁）。
func (c *Cluster) ZoneCopyCount(zone, name string, s int) (int, error) {
	if _, _, err := c.shardLocked(name, s); err != nil {
		return 0, err
	}
	cnt := 0
	for _, nid := range c.order {
		if n := c.nodes[nid]; n.zone == zone && n.copies[name][s] {
			cnt++
		}
	}
	return cnt, nil
}

// ExcludedNonEmpty 报告是否存在被排除且仍持有份的节点（调用方持锁）。
func (c *Cluster) ExcludedNonEmpty() bool {
	for _, nid := range c.order {
		if n := c.nodes[nid]; n.exclude && n.copyCnt > 0 {
			return true
		}
	}
	return false
}

// UsedOverHigh 报告节点是否 used×100 > H×total（调用方持锁）。
func (c *Cluster) UsedOverHigh(nid string) (bool, error) {
	n, err := c.nodeLocked(nid)
	if err != nil {
		return false, err
	}
	return n.used*100 > int64(c.H)*n.total, nil
}

// Copy 描述节点上的一份。
type Copy struct {
	Index   string
	Shard   int
	Primary bool
	Size    int64
}

// CopiesOn 返回节点上的全部份（顺序不定，调用方自行排序）。
func (c *Cluster) CopiesOn(nid string) ([]Copy, error) {
	n, err := c.nodeLocked(nid)
	if err != nil {
		return nil, err
	}
	out := make([]Copy, 0, n.copyCnt)
	for name, set := range n.copies {
		size := c.idx[name].size
		prim := ""
		for s := range set {
			prim = c.idx[name].shards[s].primary
			out = append(out, Copy{Index: name, Shard: s, Primary: prim == nid, Size: size})
		}
	}
	return out, nil
}
