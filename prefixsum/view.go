package prefixsum

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 输入边界。落在闭区间之外的键属于“键越界”，
// 落在闭区间之外的值属于“非法参数”。
const (
	MinKey   int64 = -1_000_000
	MaxKey   int64 = 1_000_000
	MinValue int64 = -1_000_000_000
	MaxValue int64 = 1_000_000_000

	// DefaultLimit 是推荐的默认容量上限。
	DefaultLimit = 1 << 20
	// HardMaxLimit 是允许配置的最大键数。
	HardMaxLimit = 1 << 24
)

// 互不相同、可区分的错误类别，可用 errors.Is 判定。
var (
	// ErrInvalidArgument：非法参数（nil 接收者、maxKeys 非法、值越界、批量参数非法）。
	ErrInvalidArgument = errors.New("prefixsum: invalid argument")
	// ErrKeyOutOfRange：键越界（不在 [MinKey, MaxKey] 内）。
	ErrKeyOutOfRange = errors.New("prefixsum: key out of range")
	// ErrKeyNotFound：键不存在（删除或查询一个未写入的键）。
	ErrKeyNotFound = errors.New("prefixsum: key not found")
	// ErrTooManyKeys：键数超限（插入新键会使键数超过容量）。
	ErrTooManyKeys = errors.New("prefixsum: too many keys")
	// ErrSumOverflow：前缀和溢出（提交后会有和值超出 int64）。
	ErrSumOverflow = errors.New("prefixsum: prefix sum overflow")
	// ErrInconsistent：自检发现内部状态不一致。
	ErrInconsistent = errors.New("prefixsum: inconsistent view state")
)

// entry 是一个存在键的有序节点：键、值与该键的前缀和。
type entry struct {
	key int64
	val int64
	sum int64
}

// View 是有序键上的增量前缀和视图。
//
// 前缀和 sum(k) = 所有存在且不大于 k 的键值之和，只对存在键有定义。
// 所有方法均可被多个执行体并发调用：读操作共享读锁，写操作互斥；
// 任何被拒绝的操作都在暂存副本上完成校验，不会留下状态痕迹。
type View struct {
	mu      sync.RWMutex
	maxKeys int
	entries []entry // 始终按键严格升序
}

// New 创建容量为 maxKeys 的空视图。
// maxKeys 必须落在 [1, HardMaxLimit] 内，否则返回 ErrInvalidArgument。
func New(maxKeys int) (*View, error) {
	if maxKeys < 1 || maxKeys > HardMaxLimit {
		return nil, fmt.Errorf("%w: maxKeys must be within [1, %d], got %d",
			ErrInvalidArgument, HardMaxLimit, maxKeys)
	}
	return &View{maxKeys: maxKeys, entries: make([]entry, 0)}, nil
}

// Put 插入键或修改已有键的值，返回操作后前缀和发生变化的键个数：
//   - 新插入键一律计 1（即使值为 0）；
//   - 改值时统计前缀和实际改变的存在键数（delta != 0 时含该键自身及其后受影响键）；
//   - 写入相同值（delta == 0）时返回 0。
//
// 键越界、值越界、容量超限或前缀和溢出都会整体拒绝，状态不变。
func (v *View) Put(key, value int64) (int, error) {
	if v == nil {
		return 0, fmt.Errorf("%w: nil view", ErrInvalidArgument)
	}
	if err := validateKey(key); err != nil {
		return 0, err
	}
	if err := validateValue(value); err != nil {
		return 0, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	idx := searchKey(v.entries, key)
	if idx < len(v.entries) && v.entries[idx].key == key {
		delta := value - v.entries[idx].val
		if delta == 0 {
			return 0, nil
		}
		staged := cloneEntries(v.entries)
		if !shiftSums(staged, idx, delta) {
			return 0, fmt.Errorf("%w: updating key %d by %d overflows int64",
				ErrSumOverflow, key, delta)
		}
		staged[idx].val = value
		affected := 0
		for j := idx; j < len(staged); j++ {
			if staged[j].sum != v.entries[j].sum {
				affected++
			}
		}
		v.entries = staged
		return affected, nil
	}

	if len(v.entries) >= v.maxKeys {
		return 0, fmt.Errorf("%w: cannot insert key %d: limit %d reached",
			ErrTooManyKeys, key, v.maxKeys)
	}
	staged := cloneEntries(v.entries)
	newSum := value
	if idx > 0 {
		s, ok := add64(staged[idx-1].sum, value)
		if !ok {
			return 0, fmt.Errorf("%w: inserting key %d overflows int64", ErrSumOverflow, key)
		}
		newSum = s
	}
	staged = slicesInsert(staged, idx, entry{key: key, val: value, sum: newSum})
	if !recomputeFrom(staged, idx+1) {
		return 0, fmt.Errorf("%w: inserting key %d overflows int64", ErrSumOverflow, key)
	}
	v.entries = staged
	return 1, nil
}

// Delete 删除一个存在的键。被删键不计入受影响键数；
// 返回其余存在键中前缀和发生变化的键数。
// 键越界返回 ErrKeyOutOfRange，键不存在返回 ErrKeyNotFound。
func (v *View) Delete(key int64) (int, error) {
	if v == nil {
		return 0, fmt.Errorf("%w: nil view", ErrInvalidArgument)
	}
	if err := validateKey(key); err != nil {
		return 0, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	idx := searchKey(v.entries, key)
	if idx >= len(v.entries) || v.entries[idx].key != key {
		return 0, fmt.Errorf("%w: key %d does not exist", ErrKeyNotFound, key)
	}

	staged := cloneEntries(v.entries)
	oldVal := staged[idx].val
	staged = append(staged[:idx], staged[idx+1:]...)
	if !shiftSums(staged, idx, -oldVal) {
		return 0, fmt.Errorf("%w: deleting key %d overflows int64", ErrSumOverflow, key)
	}
	affected := 0
	for j := idx; j < len(staged); j++ {
		if staged[j].sum != v.entries[j+1].sum {
			affected++
		}
	}
	v.entries = staged
	return affected, nil
}

// PrefixSum 返回存在键 key 的前缀和：所有存在且不大于 key 的键值之和。
// 键越界返回 ErrKeyOutOfRange；键不存在返回 ErrKeyNotFound。
func (v *View) PrefixSum(key int64) (int64, error) {
	if v == nil {
		return 0, fmt.Errorf("%w: nil view", ErrInvalidArgument)
	}
	if err := validateKey(key); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	idx := searchKey(v.entries, key)
	if idx >= len(v.entries) || v.entries[idx].key != key {
		return 0, fmt.Errorf("%w: key %d does not exist", ErrKeyNotFound, key)
	}
	return v.entries[idx].sum, nil
}

// Len 返回当前存在键的数量。
func (v *View) Len() int {
	if v == nil {
		return 0
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.entries)
}

// Snapshot 返回所有存在键按升序排列的键、值与前缀和副本。
// 返回切片归调用方所有，后续写操作不影响其内容。
func (v *View) Snapshot() (keys, values, sums []int64) {
	if v == nil {
		return nil, nil, nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	n := len(v.entries)
	keys = make([]int64, n)
	values = make([]int64, n)
	sums = make([]int64, n)
	for i, e := range v.entries {
		keys[i], values[i], sums[i] = e.key, e.val, e.sum
	}
	return keys, values, sums
}

// PutMany 原子地应用一批写入：全部成功或全部拒绝（失败不留痕）。
// keys 与 values 必须等长、批内键不得重复、每个键值都必须合法；
// 提交后键总数不得超过容量，且所有前缀和不得溢出。
// 返回提交前后前缀和发生变化（含新插入键）的键总数。
func (v *View) PutMany(keys, values []int64) (int, error) {
	if v == nil {
		return 0, fmt.Errorf("%w: nil view", ErrInvalidArgument)
	}
	if len(keys) != len(values) {
		return 0, fmt.Errorf("%w: keys and values must have equal length, got %d and %d",
			ErrInvalidArgument, len(keys), len(values))
	}
	for i := range keys {
		if err := validateKey(keys[i]); err != nil {
			return 0, err
		}
		if err := validateValue(values[i]); err != nil {
			return 0, err
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	pending := make(map[int64]int64, len(keys))
	for i, k := range keys {
		if _, dup := pending[k]; dup {
			return 0, fmt.Errorf("%w: duplicate key %d in batch", ErrInvalidArgument, k)
		}
		pending[k] = values[i]
	}

	staged := cloneEntries(v.entries)
	for k, val := range pending {
		idx := searchKey(staged, k)
		if idx < len(staged) && staged[idx].key == k {
			staged[idx].val = val
		} else {
			if len(staged) >= v.maxKeys {
				return 0, fmt.Errorf("%w: batch insert of key %d exceeds limit %d",
					ErrTooManyKeys, k, v.maxKeys)
			}
			staged = slicesInsert(staged, idx, entry{key: k, val: val})
		}
	}
	if !rebuildSums(staged) {
		return 0, fmt.Errorf("%w: batch commit overflows int64", ErrSumOverflow)
	}
	affected := countChanged(v.entries, staged)
	v.entries = staged
	return affected, nil
}

// Verify 自检：校验键严格升序且前缀和与朴素重算一致。只读不改，可并发调用。
func (v *View) Verify() error {
	if v == nil {
		return fmt.Errorf("%w: nil view", ErrInvalidArgument)
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return verifyEntries(v.entries)
}

// ---- 内部辅助 ----

func validateKey(k int64) error {
	if k < MinKey || k > MaxKey {
		return fmt.Errorf("%w: key must be within [%d, %d], got %d",
			ErrKeyOutOfRange, MinKey, MaxKey, k)
	}
	return nil
}

func validateValue(x int64) error {
	if x < MinValue || x > MaxValue {
		return fmt.Errorf("%w: value must be within [%d, %d], got %d",
			ErrInvalidArgument, MinValue, MaxValue, x)
	}
	return nil
}

func searchKey(entries []entry, key int64) int {
	return sort.Search(len(entries), func(i int) bool { return entries[i].key >= key })
}

func cloneEntries(entries []entry) []entry {
	clone := make([]entry, len(entries), len(entries)+1)
	copy(clone, entries)
	return clone
}

func slicesInsert(entries []entry, idx int, e entry) []entry {
	entries = append(entries, entry{})
	copy(entries[idx+1:], entries[idx:])
	entries[idx] = e
	return entries
}

// add64 计算带溢出检测的 int64 加法。
func add64(a, b int64) (int64, bool) {
	if b > 0 && a > 1<<63-1-b {
		return 0, false
	}
	if b < 0 && a < -1<<63-b {
		return 0, false
	}
	return a + b, true
}

// shiftSums 给 staged[idx:] 每个前缀和加 delta，溢出时返回 false。
func shiftSums(staged []entry, idx int, delta int64) bool {
	for j := idx; j < len(staged); j++ {
		s, ok := add64(staged[j].sum, delta)
		if !ok {
			return false
		}
		staged[j].sum = s
	}
	return true
}

// recomputeFrom 从 idx 开始依据前一键的前缀和顺次重算，溢出时返回 false。
func recomputeFrom(staged []entry, idx int) bool {
	for j := idx; j < len(staged); j++ {
		s, ok := add64(staged[j-1].sum, staged[j].val)
		if !ok {
			return false
		}
		staged[j].sum = s
	}
	return true
}

// rebuildSums 朴素重算整张视图的前缀和；用于批量提交与自检基准。
func rebuildSums(staged []entry) bool {
	var acc int64
	for j := range staged {
		s, ok := add64(acc, staged[j].val)
		if !ok {
			return false
		}
		staged[j].sum = s
		acc = s
	}
	return true
}

// countChanged 对比提交前后两张有序表，统计前缀和不同的存在键数。
// 仅在新表中存在的键（新插入）一律计 1。
func countChanged(before, after []entry) int {
	old := make(map[int64]int64, len(before))
	for _, e := range before {
		old[e.key] = e.sum
	}
	affected := 0
	for _, e := range after {
		prev, existed := old[e.key]
		if !existed || prev != e.sum {
			affected++
		}
	}
	return affected
}

func verifyEntries(entries []entry) error {
	var acc int64
	for i, e := range entries {
		if i > 0 && entries[i-1].key >= e.key {
			return fmt.Errorf("%w: keys not strictly sorted at index %d", ErrInconsistent, i)
		}
		s, ok := add64(acc, e.val)
		if !ok {
			return fmt.Errorf("%w: naive sum overflow", ErrInconsistent)
		}
		if s != e.sum {
			return fmt.Errorf("%w: prefix sum mismatch at key %d: stored %d, recomputed %d",
				ErrInconsistent, e.key, e.sum, s)
		}
		acc = s
	}
	return nil
}
