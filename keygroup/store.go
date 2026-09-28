package keygroup

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
)

// Store 按键维护状态，并按键组把状态映射到若干实例。
// 所有方法均可并发调用；读取返回深拷贝快照，逐字段一致。
type Store struct {
	mu             sync.RWMutex
	maxParallelism int                 // 键组总数，创建后固定
	parallelism    int                 // 当前并行度（实例数）
	owners         []int               // owners[g] = 键组 g 当前归属的实例下标
	buckets        []map[string]string // buckets[g] = 键组 g 内全部键值
	logger         *slog.Logger
}

// Option 自定义 Store 行为。
type Option func(*Store)

// WithLogger 注入日志器；默认丢弃日志。
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewStore 创建组件。maxParallelism 必须为正，parallelism 不得超过 maxParallelism。
func NewStore(maxParallelism, parallelism int, opts ...Option) (*Store, error) {
	if maxParallelism <= 0 {
		return nil, invalidInput("NewStore", ErrInvalidMaxParallelism,
			fmt.Sprintf("maxParallelism=%d", maxParallelism))
	}
	if parallelism <= 0 || parallelism > maxParallelism {
		return nil, invalidInput("NewStore", ErrInvalidParallelism,
			fmt.Sprintf("parallelism=%d, maxParallelism=%d", parallelism, maxParallelism))
	}
	owners, err := computeAssignment(maxParallelism, parallelism)
	if err != nil {
		return nil, err
	}
	s := &Store{
		maxParallelism: maxParallelism,
		parallelism:    parallelism,
		owners:         owners,
		buckets:        make([]map[string]string, maxParallelism),
		logger:         slog.New(slog.DiscardHandler),
	}
	for i := range s.buckets {
		s.buckets[i] = make(map[string]string)
	}
	for _, o := range opts {
		o(s)
	}
	s.logger.Info("创建键组状态组件",
		"maxParallelism", maxParallelism,
		"parallelism", parallelism,
		"owners", owners)
	return s, nil
}

// Put 写入或覆盖一个键值。空键被拒绝且不改变任何状态。
func (s *Store) Put(key, value string) error {
	group, err := KeyGroupOf(key, s.maxParallelism)
	if err != nil {
		return invalidInput("Put", ErrEmptyKey, "key=\"\"")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets[group][key] = value
	s.logger.Debug("写入键值",
		"key", key, "group", group,
		"instance", s.owners[group], "value", value)
	return nil
}

// Get 读取一个键。空键被拒绝；键不存在返回 found=false。
func (s *Store) Get(key string) (value string, found bool, err error) {
	group, err := KeyGroupOf(key, s.maxParallelism)
	if err != nil {
		return "", false, invalidInput("Get", ErrEmptyKey, "key=\"\"")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.buckets[group][key]
	return v, ok, nil
}

// Delete 删除一个键。空键被拒绝；键不存在为无操作。
func (s *Store) Delete(key string) error {
	group, err := KeyGroupOf(key, s.maxParallelism)
	if err != nil {
		return invalidInput("Delete", ErrEmptyKey, "key=\"\"")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.buckets[group], key)
	return nil
}

// Snapshot 是某一时刻全部状态的一致性深拷贝，可脱离锁安全读取。
type Snapshot struct {
	MaxParallelism int
	Parallelism    int
	Ranges         []Range             // 每个实例分得的键组区间
	Owners         []int               // 每个键组的归属实例
	Buckets        []map[string]string // 每个键组内的键值（深拷贝）
}

// Snapshot 返回当前状态的一致性快照。
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *Store) snapshotLocked() Snapshot {
	snap := Snapshot{
		MaxParallelism: s.maxParallelism,
		Parallelism:    s.parallelism,
		Ranges:         make([]Range, s.parallelism),
		Owners:         slices.Clone(s.owners),
		Buckets:        make([]map[string]string, s.maxParallelism),
	}
	for i := range snap.Ranges {
		// parallelism 已校验，此处不会出错
		r, _ := ComputeKeyGroupRange(s.maxParallelism, s.parallelism, i)
		snap.Ranges[i] = r
	}
	for g := range snap.Buckets {
		snap.Buckets[g] = maps.Clone(s.buckets[g])
	}
	return snap
}

// KeySet 返回快照中全部键值对的扁平映射，便于守恒性比对。
func (snap Snapshot) KeySet() map[string]string {
	out := make(map[string]string)
	for _, bucket := range snap.Buckets {
		maps.Copy(out, bucket)
	}
	return out
}

// TotalKeys 返回快照中的键总数。
func (snap Snapshot) TotalKeys() int {
	total := 0
	for _, bucket := range snap.Buckets {
		total += len(bucket)
	}
	return total
}
