package reconcile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 精确判定。
var (
	// ErrNilReplica 表示在空副本指针上调用了方法，或对账任一端为 nil。
	ErrNilReplica = errors.New("reconcile: replica is nil")
	// ErrInvalidKeySpace 表示键空间大小为 0。
	ErrInvalidKeySpace = errors.New("reconcile: key space must be greater than zero")
	// ErrInvalidFanout 表示扇出小于 2，无法递归等分。
	ErrInvalidFanout = errors.New("reconcile: fanout must be at least 2")
	// ErrInvalidMaxKeys 表示最大键数为负。
	ErrInvalidMaxKeys = errors.New("reconcile: max keys must not be negative")
	// ErrNilValue 表示写入值为 nil；零值请显式使用空切片 []byte{}。
	ErrNilValue = errors.New("reconcile: value must not be nil (use []byte{} for a zero-length value)")
	// ErrKeyOutOfRange 表示键不在 [0, KeySpace) 内。
	ErrKeyOutOfRange = errors.New("reconcile: key out of range")
	// ErrTooManyKeys 表示写入新键会超过 MaxKeys 上限。
	ErrTooManyKeys = errors.New("reconcile: too many keys")
	// ErrShapeMismatch 表示两个副本的键空间或扇出不同，不能对账。
	ErrShapeMismatch = errors.New("reconcile: replicas have different shapes")
)

// Options 描述副本的形状与容量。
type Options struct {
	// KeySpace 是键的总数，合法键为 [0, KeySpace)。
	KeySpace uint64
	// Fanout 是每次递归等分时的子区间数，必须 >= 2。
	Fanout int
	// MaxKeys 限制可同时存在的键数；0 表示不允许任何键，
	// 如需不限请使用 math.MaxInt。
	MaxKeys int
	// Logger 接收操作与对账日志；nil 时写入标准错误。
	Logger io.Writer
}

// Replica 是一份基于区间哈希树的键值副本。
//
// 哈希树按层扁平存储：第 0 层是根区间，最后一层是每个键一个叶子。
// 区间一律左闭右开 [base, end)，父区间在每一层被等分为 Fanout 个子区间，
// 键空间不能整除时最后一个区间截断到 KeySpace。
type Replica struct {
	mu       sync.RWMutex
	keySpace uint64
	fanout   int
	maxKeys  int
	depth    int      // 根到叶子的层数（叶子层下标）
	levels   [][]uint64 // levels[0] 根，levels[depth] 叶子
	data     map[uint64][]byte
	logger   io.Writer
}

// NewReplica 创建一个空副本，并立即构建“全部键不存在”的哈希树。
// 参数非法时返回对应错误且不会分配任何副本状态。
func NewReplica(opts Options) (*Replica, error) {
	if opts.KeySpace == 0 {
		return nil, ErrInvalidKeySpace
	}
	if opts.Fanout < 2 {
		return nil, ErrInvalidFanout
	}
	if opts.MaxKeys < 0 {
		return nil, ErrInvalidMaxKeys
	}

	depth := 0
	width := uint64(1)
	for width < opts.KeySpace {
		width *= uint64(opts.Fanout)
		depth++
	}

	levels := make([][]uint64, depth+1)
	// 叶子层：每个键槽一个“不存在”叶子哈希。
	leaves := make([]uint64, opts.KeySpace)
	for key := uint64(0); key < opts.KeySpace; key++ {
		leaves[key] = leafHash(key, nil, false)
	}
	levels[depth] = leaves
	// 自底向上组合。
	for level := depth - 1; level >= 0; level-- {
		childCount := len(levels[level+1])
		nodeCount := (childCount + opts.Fanout - 1) / opts.Fanout
		nodes := make([]uint64, nodeCount)
		childWidth := uint64(1)
		for step := 0; step < depth-level-1; step++ {
			childWidth *= uint64(opts.Fanout)
		}
		for idx := 0; idx < nodeCount; idx++ {
			start := idx * opts.Fanout
			end := start + opts.Fanout
			if end > childCount {
				end = childCount
			}
			nodes[idx] = combineChildren(level, uint64(idx)*childWidth*uint64(opts.Fanout),
				levels[level+1][start:end])
		}
		levels[level] = nodes
	}

	logger := opts.Logger
	if logger == nil {
		logger = os.Stderr
	}

	r := &Replica{
		keySpace: opts.KeySpace,
		fanout:   opts.Fanout,
		maxKeys:  opts.MaxKeys,
		depth:    depth,
		levels:   levels,
		data:     make(map[uint64][]byte),
		logger:   logger,
	}
	r.logf("new replica keySpace=%d fanout=%d maxKeys=%d depth=%d rootHash=%016x",
		opts.KeySpace, opts.Fanout, opts.MaxKeys, depth, r.levels[0][0])
	return r, nil
}

// Put 写入或更新一个键。
// value 不能为 nil：零长（零值）键请传 []byte{}。
// 键越界、值为 nil 或新增键超过上限都会被拒绝，且状态保持不变。
func (r *Replica) Put(key uint64, value []byte) error {
	if r == nil {
		return ErrNilReplica
	}
	if value == nil {
		r.logf("put rejected key=%d reason=%q", key, ErrNilValue)
		return ErrNilValue
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key >= r.keySpace {
		r.logf("put rejected key=%d reason=%q", key, ErrKeyOutOfRange)
		return ErrKeyOutOfRange
	}
	if _, exists := r.data[key]; !exists && len(r.data) >= r.maxKeys {
		r.logf("put rejected key=%d reason=%q len=%d max=%d",
			key, ErrTooManyKeys, len(r.data), r.maxKeys)
		return ErrTooManyKeys
	}

	stored := make([]byte, len(value))
	copy(stored, value)
	r.data[key] = stored
	r.refreshToRoot(key, leafHash(key, stored, true))
	r.logf("put accepted key=%d valueLen=%d keys=%d rootHash=%016x",
		key, len(stored), len(r.data), r.levels[0][0])
	return nil
}

// Delete 删除一个键。删除不存在的键是空操作，不视为错误。
func (r *Replica) Delete(key uint64) error {
	if r == nil {
		return ErrNilReplica
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key >= r.keySpace {
		r.logf("delete rejected key=%d reason=%q", key, ErrKeyOutOfRange)
		return ErrKeyOutOfRange
	}
	if _, exists := r.data[key]; !exists {
		r.logf("delete noop key=%d (already absent)", key)
		return nil
	}
	delete(r.data, key)
	r.refreshToRoot(key, leafHash(key, nil, false))
	r.logf("delete accepted key=%d keys=%d rootHash=%016x",
		key, len(r.data), r.levels[0][0])
	return nil
}

// Get 读取一个键。exists 为 false 时 value 为 nil；
// 存在零长键时 value 是非 nil 的空切片，借此区分“不存在”和“零值”。
// 返回的值是防御性拷贝，调用方可自由修改。
func (r *Replica) Get(key uint64) (value []byte, exists bool, err error) {
	if r == nil {
		return nil, false, ErrNilReplica
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if key >= r.keySpace {
		return nil, false, ErrKeyOutOfRange
	}
	stored, exists := r.data[key]
	if !exists {
		return nil, false, nil
	}
	out := make([]byte, len(stored))
	copy(out, stored)
	return out, true, nil
}

// Len 返回当前存在的键的数量。
func (r *Replica) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.data)
}

// RootHash 返回根区间 [0, KeySpace) 的组合哈希。
func (r *Replica) RootHash() uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.levels[0][0]
}

// Shape 返回键空间、扇出与树深；形状相同的两个副本才能对账。
func (r *Replica) Shape() (keySpace uint64, fanout, depth int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.keySpace, r.fanout, r.depth
}

// refreshToRoot 用新的叶子哈希沿祖先链增量重算哈希，
// 只触碰该键所在路径上的节点。
func (r *Replica) refreshToRoot(key, newLeaf uint64) {
	r.levels[r.depth][key] = newLeaf
	idx := int(key)
	for level := r.depth - 1; level >= 0; level-- {
		idx /= r.fanout
		start := idx * r.fanout
		end := start + r.fanout
		if end > len(r.levels[level+1]) {
			end = len(r.levels[level+1])
		}
		childWidth := uint64(1)
		for step := 0; step < r.depth-level-1; step++ {
			childWidth *= uint64(r.fanout)
		}
		r.levels[level][idx] = combineChildren(
			level, uint64(idx)*childWidth*uint64(r.fanout),
			r.levels[level+1][start:end])
	}
}

func (r *Replica) logf(format string, args ...any) {
	fmt.Fprintf(r.logger, "reconcile: "+format+"\n", args...)
}
