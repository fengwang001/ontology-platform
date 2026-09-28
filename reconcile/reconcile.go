package reconcile

import (
	"log"
	"sort"
	"sync"
)

// Logger 是对账组件使用的最小日志接口，标准库 *log.Logger 天然满足。
type Logger interface {
	Printf(format string, args ...any)
}

var (
	logMu  sync.RWMutex
	logger Logger = log.Default()
)

// SetLogger 设置组件日志记录器；传 nil 表示关闭日志。
func SetLogger(l Logger) {
	logMu.Lock()
	defer logMu.Unlock()
	logger = l
}

func logf(format string, args ...any) {
	logMu.RLock()
	l := logger
	logMu.RUnlock()
	if l != nil {
		l.Printf(format, args...)
	}
}

// Comparison 记录对账过程中实际执行的一次区间哈希比较。
// 同一对副本在相同数据下反复对账会产生完全相同的序列：按层从上到下、
// 每层内按区间左端点从小到大。
type Comparison struct {
	Depth    int    // 0 表示根区间
	Left     uint64 // 区间左端点（含）
	Right    uint64 // 区间右端点（不含）
	HashA    uint64 // 副本 a 在该区间的哈希
	HashB    uint64 // 副本 b 在该区间的哈希
	Equal    bool   // 两个哈希是否相等
	Drilled  bool   // 哈希不等且为内部区间，是否继续向下钻取
	IsLeaf   bool   // 是否为单键叶子区间
}

// DiffEntry 是一个被精确定位的差异键。
type DiffEntry struct {
	Key       uint64
	ExistsA   bool
	ValueA    uint64
	ExistsB   bool
	ValueB    uint64
}

// Result 是一次对账的完整结果。
type Result struct {
	Diffs       []DiffEntry  // 按 Key 升序，去重
	Comparisons []Comparison // 实际执行的比较序列，确定顺序
}

// snapshot 是单个副本在某一时刻的只读视图。构造快照时持有读锁，复制出
// 哈希树与键值；后续下钻全程基于快照，保证结果逐字段一致。
type snapshot struct {
	fanout      int
	depth       int
	keyspace    uint64
	values      map[uint64]uint64
	hashes      []uint64
	layerOffset []int
	layerCount  []int
	nonEmpty    [][]bool
}

func (r *Replica) snapshotLocked() snapshot {
	hashes := make([]uint64, len(r.hashes))
	copy(hashes, r.hashes)
	nonEmpty := make([][]bool, len(r.nonEmpty))
	for d := range r.nonEmpty {
		nonEmpty[d] = append([]bool(nil), r.nonEmpty[d]...)
	}
	values := make(map[uint64]uint64, len(r.values))
	for k, v := range r.values {
		values[k] = v
	}
	return snapshot{
		fanout:      r.fanout,
		depth:       r.depth,
		keyspace:    r.keyspace,
		values:      values,
		hashes:      hashes,
		layerOffset: r.layerOffset,
		layerCount:  r.layerCount,
		nonEmpty:    nonEmpty,
	}
}

func (s snapshot) hash(d, idx int) (uint64, bool) {
	return s.hashes[s.layerOffset[d]+idx], s.nonEmpty[d][idx]
}

// frame 标识下钻队列中的一个区间节点。
type frame struct {
	depth int
	idx   int
}

// intervalBounds 返回层 d 第 idx 个区间的左闭右开边界。
// 层 d 共有 F^(D-d) 个等宽区间，宽度为 F^d。
func intervalBounds(depth, idx, fanout, treeDepth int, keyspace uint64) (uint64, uint64) {
	width := uint64(1)
	for i := 0; i < depth; i++ {
		width *= uint64(fanout)
	}
	left := uint64(idx) * width
	right := left + width
	if right > keyspace {
		right = keyspace
	}
	return left, right
}

// Reconcile 对两个形状相同的副本做区间哈希对账：从根区间开始，仅对哈希
// 不一致的区间按扇出向下钻取，直到单键叶子，叶子不一致即记为差异键。
//
// 两个副本的 fanout/depth/keyspace 必须完全一致，否则返回带
// ErrShapeMismatch 的 OpError，且不读取/修改任何数据。
//
// 结果基于两份加锁快照计算，支持与并发写入同时进行；同一对副本在相同
// 数据下反复对账得到完全相同的差异键集合与比较序列。
func Reconcile(a, b *Replica) (*Result, error) {
	const op = "Reconcile"
	if a == nil || b == nil {
		return nil, newError(ErrInvalidParam, op, "both replicas must be non-nil")
	}
	if a == b {
		// 同一实例与自身对账：形状必然相同，差异为空，只需一次根比较。
		return reconcileSelf(a), nil
	}
	if a.fanout != b.fanout || a.depth != b.depth || a.keyspace != b.keyspace {
		return nil, newError(ErrShapeMismatch, op,
			"replica shapes differ: %s(fanout=%d,depth=%d,keyspace=%d) vs %s(fanout=%d,depth=%d,keyspace=%d)",
			a.name, a.fanout, a.depth, a.keyspace,
			b.name, b.fanout, b.depth, b.keyspace)
	}

	// 固定加锁顺序（按创建时分配的唯一 id 升序），避免与其他并发对账
	// 形成死锁。
	first, second := a, b
	if first.id > second.id {
		first, second = second, first
	}
	first.mu.RLock()
	second.mu.RLock()
	sa := a.snapshotLocked()
	sb := b.snapshotLocked()
	second.mu.RUnlock()
	first.mu.RUnlock()

	res := &Result{}
	logf("reconcile input: a=%s b=%s fanout=%d depth=%d keyspace=%d keysA=%d keysB=%d",
		a.name, b.name, sa.fanout, sa.depth, sa.keyspace, len(sa.values), len(sb.values))

	queue := []frame{{depth: 0, idx: 0}}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		ha, _ := sa.hash(node.depth, node.idx)
		hb, _ := sb.hash(node.depth, node.idx)
		left, right := intervalBounds(node.depth, node.idx, sa.fanout, sa.depth, sa.keyspace)
		equal := ha == hb
		cmp := Comparison{
			Depth:  node.depth,
			Left:   left,
			Right:  right,
			HashA:   ha,
			HashB:   hb,
			Equal:  equal,
			IsLeaf: node.depth == sa.depth,
		}

		if equal {
			// 哈希一致：区间内容（在哈希摘要意义下）一致，整体剪枝，不下钻。
			res.Comparisons = append(res.Comparisons, cmp)
			continue
		}

		if cmp.IsLeaf {
			// 叶子区间宽度为 1，不一致的唯一可能就是该键本身不同。
			key := left
			va, ea := sa.values[key]
			vb, eb := sb.values[key]
			res.Diffs = append(res.Diffs, DiffEntry{
				Key:     key,
				ExistsA: ea,
				ValueA:  va,
				ExistsB: eb,
				ValueB:  vb,
			})
			res.Comparisons = append(res.Comparisons, cmp)
			logf("reconcile diff key=%d: a(exists=%v,value=%d) b(exists=%v,value=%d) basis=leaf-hash-mismatch",
				key, ea, va, eb, vb)
			continue
		}

		// 内部区间哈希不一致：按扇出等分，全部子区间（左端点升序）入队，
		// 下一轮只比较这些子区间；哈希一致的子区间会在出队时被剪枝。
		cmp.Drilled = true
		res.Comparisons = append(res.Comparisons, cmp)
		start := node.idx * sa.fanout
		logf("reconcile drill depth=%d interval=[%d,%d) basis=hash-mismatch children=[%d,%d)",
			node.depth, left, right, start, start+sa.fanout)
		for c := 0; c < sa.fanout; c++ {
			queue = append(queue, frame{depth: node.depth + 1, idx: start + c})
		}
	}

	sort.Slice(res.Diffs, func(i, j int) bool { return res.Diffs[i].Key < res.Diffs[j].Key })
	logf("reconcile done: comparisons=%d diffs=%d diffKeys=%v",
		len(res.Comparisons), len(res.Diffs), diffKeys(res.Diffs))
	return res, nil
}

func reconcileSelf(r *Replica) *Result {
	r.mu.RLock()
	h := r.hashes[r.layerOffset[0]]
	r.mu.RUnlock()
	logf("reconcile input: a=b=%s fanout=%d depth=%d keyspace=%d (same instance)",
		r.name, r.fanout, r.depth, r.keyspace)
	return &Result{
		Diffs: nil,
		Comparisons: []Comparison{{
			Depth: 0,
			Left:  0,
			Right: r.keyspace,
			HashA: h,
			HashB: h,
			Equal: true,
		}},
	}
}

func diffKeys(diffs []DiffEntry) []uint64 {
	keys := make([]uint64, len(diffs))
	for i, d := range diffs {
		keys[i] = d.Key
	}
	return keys
}
