package reconcile

import (
	"math"
	"sync"
)

// Replica 是一份物化视图副本：uint64 键 -> uint64 值，并在写入时增量
// 维护一棵按扇出递归等分的区间哈希树。
//
// 键空间形状由 fanout（每层扇出 F）与 depth（层级数 D）确定，且必须满足
// keyspace == F^D；最底层每个叶子恰好对应一个键（区间宽度 1），因此
// 区间左闭右开划分可以精确到单个键。
//
// 三种“没有值”的状态可区分：
//   - 空区间：整段区间没有任何已写入键，区间哈希恒为 fnvEmpty；
//   - 键不存在：叶子对应键从未写入（或已删除），哈希为 fnvAbsent；
//   - 值为 0 的键：键已写入且值为 0，哈希为 leafHash(0)。
type Replica struct {
	name     string
	id       uint64
	fanout   int
	depth    int
	keyspace uint64
	maxKeys  int

	mu     sync.RWMutex
	values map[uint64]uint64
	// hashes 为完全树的平面数组：层 d（0=根 … D=叶）的节点偏移
	// layerOffset[d]，层内节点按区间左端点升序排列。
	hashes      []uint64
	layerOffset []int
	layerCount  []int

	// nonEmpty[d][idx] 标记层 d 的节点是否拥有“非空后代”，即其任一
	// 叶子键存在。没有非空后代的节点哈希直接取 fnvEmpty，从而让
	// “空区间”与“键不存在”得到不同摘要。
	nonEmpty [][]bool
}

var nextReplicaID = struct {
	sync.Mutex
	n uint64
}{}

// Config 描述一个副本的形状与容量约束。
type Config struct {
	// Fanout 为每层的等分份数，必须 >= 2。
	Fanout int
	// Depth 为层级数（不含叶子之上的额外层），必须 >= 1。
	Depth int
	// MaxKeys 为允许同时存在的最大键数，必须 >= 0；0 表示不允许任何键。
	MaxKeys int
	// Name 仅用于日志展示，可为空。
	Name string
}

// NewReplica 创建指定形状的副本。F^D 必须在 uint64 范围内且等于
// keyspace；任何参数非法都返回带 ErrInvalidParam 的 OpError。
func NewReplica(cfg Config) (*Replica, error) {
	const op = "NewReplica"
	if cfg.Fanout < 2 {
		return nil, newError(ErrInvalidParam, op, "fanout must be >= 2, got %d", cfg.Fanout)
	}
	if cfg.Depth < 1 {
		return nil, newError(ErrInvalidParam, op, "depth must be >= 1, got %d", cfg.Depth)
	}
	if cfg.MaxKeys < 0 {
		return nil, newError(ErrInvalidParam, op, "maxKeys must be >= 0, got %d", cfg.MaxKeys)
	}

	// 逐级相乘并做溢出检查，得到 keyspace = F^D。
	var keyspace uint64 = 1
	for i := 0; i < cfg.Depth; i++ {
		if keyspace > math.MaxUint64/uint64(cfg.Fanout) {
			return nil, newError(ErrInvalidParam, op,
				"fanout^depth overflows uint64 (fanout=%d, depth=%d)", cfg.Fanout, cfg.Depth)
		}
		keyspace *= uint64(cfg.Fanout)
	}

	layerCount := make([]int, cfg.Depth+1)
	layerOffset := make([]int, cfg.Depth+1)
	totalNodes := 0
	count := 1
	for d := 0; d <= cfg.Depth; d++ {
		layerCount[d] = count
		layerOffset[d] = totalNodes
		totalNodes += count
		// 下一层数量 *= F；叶子层（d==D）之后不再使用，跳过溢出检查。
		if d < cfg.Depth {
			count *= cfg.Fanout
		}
	}

	nonEmpty := make([][]bool, cfg.Depth+1)
	for d := 0; d <= cfg.Depth; d++ {
		nonEmpty[d] = make([]bool, layerCount[d])
	}

	hashes := make([]uint64, totalNodes)
	// 初始状态：所有叶子键缺失（fnvAbsent），所有内部节点为空区间（fnvEmpty）。
	for d := 0; d < cfg.Depth; d++ {
		for idx := 0; idx < layerCount[d]; idx++ {
			hashes[layerOffset[d]+idx] = fnvEmpty
		}
	}
	for idx := 0; idx < layerCount[cfg.Depth]; idx++ {
		hashes[layerOffset[cfg.Depth]+idx] = fnvAbsent
	}

	nextReplicaID.Lock()
	nextReplicaID.n++
	id := nextReplicaID.n
	nextReplicaID.Unlock()

	name := cfg.Name
	if name == "" {
		name = "replica-" + uitoa(id)
	}

	return &Replica{
		name:        name,
		id:          id,
		fanout:      cfg.Fanout,
		depth:       cfg.Depth,
		keyspace:    keyspace,
		maxKeys:     cfg.MaxKeys,
		values:      make(map[uint64]uint64),
		hashes:      hashes,
		layerOffset: layerOffset,
		layerCount:  layerCount,
		nonEmpty:    nonEmpty,
	}, nil
}

// Name 返回副本名称（用于日志）。
func (r *Replica) Name() string { return r.name }

// Fanout 返回每层等分份数。
func (r *Replica) Fanout() int { return r.fanout }

// Depth 返回层级数。
func (r *Replica) Depth() int { return r.depth }

// Keyspace 返回键空间大小 F^D；合法键为 [0, Keyspace())。
func (r *Replica) Keyspace() uint64 { return r.keyspace }

// MaxKeys 返回创建时声明的最大键数。
func (r *Replica) MaxKeys() int { return r.maxKeys }

// Len 返回当前已存在的键数量（值为 0 的键同样计数）。
func (r *Replica) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.values)
}

// Get 返回键值。exists 为 false 表示键不存在（与值为 0 可区分）。
func (r *Replica) Get(key uint64) (value uint64, exists bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[key]
	return v, ok
}

// RootHash 返回根区间 [0, keyspace) 的组合哈希。
func (r *Replica) RootHash() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hashes[r.layerOffset[0]]
}

// HashAt 返回层 d 上第 idx 个区间（按左端点升序）的哈希。供对账与测试使用。
func (r *Replica) HashAt(depth, idx int) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if depth < 0 || depth > r.depth || idx < 0 || idx >= r.layerCount[depth] {
		return 0
	}
	return r.hashes[r.layerOffset[depth]+idx]
}

// Put 写入或覆盖一个键。键越界返回 ErrKeyOutOfRange；新键导致数量超过
// MaxKeys 返回 ErrTooManyKeys。被拒绝时不改变任何键值与区间哈希。
func (r *Replica) Put(key, value uint64) error {
	const op = "Put"
	if key >= r.keyspace {
		return newError(ErrKeyOutOfRange, op,
			"key %d out of range [0,%d)", key, r.keyspace)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.values[key]; !exists && len(r.values) >= r.maxKeys {
		return newError(ErrTooManyKeys, op,
			"cannot insert key %d: already at maxKeys %d", key, r.maxKeys)
	}
	r.values[key] = value
	r.updateLocked(key, true)
	return nil
}

// Delete 删除一个键。键不存在时为空操作且不报错（幂等）。键越界返回
// ErrKeyOutOfRange，且不改变任何状态。
func (r *Replica) Delete(key uint64) error {
	const op = "Delete"
	if key >= r.keyspace {
		return newError(ErrKeyOutOfRange, op,
			"key %d out of range [0,%d)", key, r.keyspace)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.values[key]; !exists {
		return nil
	}
	delete(r.values, key)
	r.updateLocked(key, false)
	return nil
}

// updateLocked 在调用方已持有写锁时，沿 key 对应的叶子到根路径增量
// 重算哈希。present 表示该叶子键在本次操作后是否存在。
func (r *Replica) updateLocked(key uint64, present bool) {
	leafIdx := int(key)
	r.nonEmpty[r.depth][leafIdx] = present
	if present {
		r.hashes[r.layerOffset[r.depth]+leafIdx] = leafHash(r.values[key])
	} else {
		r.hashes[r.layerOffset[r.depth]+leafIdx] = fnvAbsent
	}

	idx := leafIdx
	for d := r.depth - 1; d >= 0; d-- {
		parentIdx := idx / r.fanout
		nonEmpty := false
		children := make([]uint64, r.fanout)
		start := parentIdx * r.fanout
		off := r.layerOffset[d+1]
		for c := 0; c < r.fanout; c++ {
			ci := start + c
			children[c] = r.hashes[off+ci]
			if r.nonEmpty[d+1][ci] {
				nonEmpty = true
			}
		}
		r.nonEmpty[d][parentIdx] = nonEmpty
		if nonEmpty {
			r.hashes[r.layerOffset[d]+parentIdx] = combineChildren(children)
		} else {
			r.hashes[r.layerOffset[d]+parentIdx] = fnvEmpty
		}
		idx = parentIdx
	}
}

// nodeAt 在调用方持锁时返回层 d 第 idx 个节点的哈希与非空标记。
func (r *Replica) nodeAt(d, idx int) (uint64, bool) {
	return r.hashes[r.layerOffset[d]+idx], r.nonEmpty[d][idx]
}

func uitoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
