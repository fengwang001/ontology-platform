package prefixsum

import "sync"

// Config 描述视图的合法边界与容量上限。
type Config struct {
	MinKey  int64
	MaxKey  int64
	MaxKeys int
}

// Entry 是快照中的一个存在键及其值与前缀和。
type Entry struct {
	Key       int64
	Value     int64
	PrefixSum int64
}

// View 是有序键上的增量前缀和视图。
// 零值不可用，必须通过 New 创建。
type View struct {
	mu      sync.RWMutex
	minKey  int64
	maxKey  int64
	maxKeys int
	root    *node
	rng     uint64
}

// New 创建边界与容量受限的空视图。
func New(cfg Config) (*View, error) {
	if cfg.MinKey > cfg.MaxKey {
		return nil, errInvalid("MinKey must not be greater than MaxKey")
	}
	if cfg.MaxKeys <= 0 {
		return nil, errInvalid("MaxKeys must be positive")
	}
	return &View{
		minKey:  cfg.MinKey,
		maxKey:  cfg.MaxKey,
		maxKeys: cfg.MaxKeys,
		rng:     0x9E3779B97F4A7C15,
	}, nil
}

func (v *View) checkKey(key int64) error {
	if key < v.minKey || key > v.maxKey {
		return errKeyRange("key is outside [MinKey, MaxKey]")
	}
	return nil
}

// newPrio 返回分裂混序生成的伪随机优先级（值越小越靠近根）。
// 调用方必须持有写锁。
func (v *View) newPrio() uint64 {
	v.rng += 0x9E3779B97F4A7C15
	z := v.rng
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Put 插入或修改键值，返回操作后前缀和发生变化的存在键个数。
// 新插入键一律计一；被拒绝时状态完全不变。
func (v *View) Put(key, value int64) (affected int, err error) {
	if v == nil {
		return 0, errInvalid("view is nil")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkKey(key); err != nil {
		return 0, err
	}
	existed := find(v.root, key) != nil
	if !existed && v.size() >= v.maxKeys {
		return 0, errTooMany("key count would exceed MaxKeys")
	}
	var oldVal int64
	if old := find(v.root, key); old != nil {
		oldVal = old.val
	}
	newRoot, _, err := v.insertOrReplace(v.root, key, value)
	if err != nil {
		// 函数式更新失败时旧根原样保留，失败不留痕。
		return 0, err
	}
	oldRoot := v.root
	v.root = newRoot
	switch {
	case !existed:
		// 新插入键一律计一；其余受影响者为所有更大的存在键。
		affected = 1 + countGE(oldRoot, key)
	case oldVal != value:
		// 改值：该键与所有更大的存在键前缀和平移 value-old.val。
		affected = countGE(oldRoot, key)
	}
	return affected, nil
}

// Delete 删除存在键，返回操作后前缀和发生变化的存在键个数（被删键不计）。
func (v *View) Delete(key int64) (affected int, err error) {
	if v == nil {
		return 0, errInvalid("view is nil")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkKey(key); err != nil {
		return 0, err
	}
	if find(v.root, key) == nil {
		return 0, errNotFound(key)
	}
	// 先统计严格更大的存在键：删除仅平移这些键的前缀和。
	affected = countGE(v.root, key) - 1
	newRoot, removed, err := v.erase(v.root, key)
	if err != nil {
		return 0, err
	}
	if !removed {
		return 0, errNotFound(key)
	}
	v.root = newRoot
	return affected, nil
}

// PrefixSum 返回存在键的前缀和；键不存在返回 ReasonKeyNotFound。
func (v *View) PrefixSum(key int64) (sum int64, err error) {
	if v == nil {
		return 0, errInvalid("view is nil")
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if err := v.checkKey(key); err != nil {
		return 0, err
	}
	if find(v.root, key) == nil {
		return 0, errNotFound(key)
	}
	return prefixSum(v.root, key), nil
}

// Snapshot 返回按键升序、逐键前缀和与朴素重算一致的只读副本。
func (v *View) Snapshot() []Entry {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return flatten(v.root, make([]Entry, 0, v.size()))
}

// Len 返回当前存在键数量。
func (v *View) Len() int {
	if v == nil {
		return 0
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.size()
}

func (v *View) size() int { return sizeOf(v.root) }

// Bounds 返回视图配置的最小键、最大键与容量上限。
func (v *View) Bounds() (minKey, maxKey int64, maxKeys int) {
	return v.minKey, v.maxKey, v.maxKeys
}

// SelfCheck 与朴素重算逐键比对，发现不一致时返回 ReasonInvalidArgument 错误。
func (v *View) SelfCheck() error {
	if v == nil {
		return errInvalid("view is nil")
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	entries := flatten(v.root, make([]Entry, 0, v.size()))
	if len(entries) != v.size() {
		return errInvalid("self-check: size mismatch")
	}

	var running int64
	var prev int64
	for i, e := range entries {
		if i > 0 && e.Key <= prev {
			return errInvalid("self-check: keys not strictly ascending")
		}
		if e.Key < v.minKey || e.Key > v.maxKey {
			return errInvalid("self-check: key outside bounds")
		}
		sum, ok := addChecked(running, e.Value)
		if !ok {
			return errOverflow("self-check: naive prefix sum overflow")
		}
		running = sum
		if e.PrefixSum != running {
			return errInvalid("self-check: incremental view disagrees with naive recomputation")
		}
		if got := prefixSum(v.root, e.Key); got != running {
			return errInvalid("self-check: PrefixSum query disagrees with naive recomputation")
		}
		prev = e.Key
	}
	return nil
}
