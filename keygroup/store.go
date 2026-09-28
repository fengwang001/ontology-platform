package keygroup

import (
	"fmt"
	"strings"
	"sync"
)

// Logger 是组件依赖的最小日志接口。
type Logger interface {
	Printf(format string, args ...any)
}

// GroupOwnership 描述一个键组在一次扩缩容前后的归属。
type GroupOwnership struct {
	Group    int
	OldOwner int
	NewOwner int
	Moved    bool
	KeyCount int
}

// RescaleResult 是一次扩缩容的确定性结果。
type RescaleResult struct {
	OldParallelism int
	NewParallelism int
	Groups         []GroupOwnership
	MovedGroups    []int
	MovedKeys      int
	TotalKeys      int
}

// Snapshot 是某一时刻状态的逐字段一致只读视图。
type Snapshot struct {
	Parallelism int
	NumGroups   int
	Ranges      []GroupRange
	KeyOwners   map[string]int
	Values      map[string]string
}

// Store 按键维护状态，经固定数量的键组映射到若干实例。
type Store struct {
	mu          sync.RWMutex
	numGroups   int
	parallelism int
	ranges      []GroupRange
	values      map[string]string
	keyGroup    map[string]int
	groupCount  []int
	logger      Logger
}

// NewStore 创建一个键组存储。
func NewStore(numGroups, initialParallelism int, logger Logger) (*Store, error) {
	if logger == nil {
		return nil, ErrInvalidLogger
	}
	ranges, err := AssignRanges(numGroups, initialParallelism)
	if err != nil {
		return nil, err
	}
	return &Store{
		numGroups:   numGroups,
		parallelism: initialParallelism,
		ranges:      ranges,
		values:      make(map[string]string),
		keyGroup:    make(map[string]int),
		groupCount:  make([]int, numGroups),
		logger:      logger,
	}, nil
}

// Put 写入或更新一个键的值。
func (s *Store) Put(key, value string) error {
	group, err := KeyToGroup(key, uint32(s.numGroups))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, existed := s.values[key]; !existed {
		s.groupCount[group]++
	}
	s.values[key] = value
	s.keyGroup[key] = group
	return nil
}

// Get 读取一个键的值。
func (s *Store) Get(key string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[key]
	return value, ok, nil
}

// Delete 删除一个键。
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.keyGroup[key]
	if !ok {
		return nil
	}
	delete(s.values, key)
	delete(s.keyGroup, key)
	s.groupCount[group]--
	return nil
}

// Rescale 调整并行度并计算键组迁移结果。
func (s *Store) Rescale(newParallelism int) (RescaleResult, error) {
	if newParallelism <= 0 {
		return RescaleResult{}, &InvalidParallelismError{Parallelism: newParallelism, Reason: ErrInvalidParallelism}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if newParallelism == s.parallelism {
		return RescaleResult{}, &InvalidParallelismError{Parallelism: newParallelism, Reason: ErrParallelismUnchanged}
	}
	if newParallelism > s.numGroups {
		return RescaleResult{}, &InvalidParallelismError{Parallelism: newParallelism, Reason: ErrParallelismTooLarge}
	}

	oldRanges := s.ranges
	newRanges, err := AssignRanges(s.numGroups, newParallelism)
	if err != nil {
		return RescaleResult{}, err
	}

	result := RescaleResult{
		OldParallelism: s.parallelism,
		NewParallelism: newParallelism,
		Groups:         make([]GroupOwnership, 0, s.numGroups),
		TotalKeys:      len(s.values),
	}
	for group := 0; group < s.numGroups; group++ {
		oldOwner, _ := OwnerOf(group, s.numGroups, s.parallelism)
		newOwner, _ := OwnerOf(group, s.numGroups, newParallelism)
		moved := oldOwner != newOwner
		ownership := GroupOwnership{
			Group:    group,
			OldOwner: oldOwner,
			NewOwner: newOwner,
			Moved:    moved,
			KeyCount: s.groupCount[group],
		}
		result.Groups = append(result.Groups, ownership)
		if moved {
			result.MovedGroups = append(result.MovedGroups, group)
			result.MovedKeys += s.groupCount[group]
		}
	}

	s.parallelism = newParallelism
	s.ranges = newRanges

	s.logRescale(oldRanges, newRanges, result)
	return result, nil
}

// Snapshot 返回当前状态的一致只读快照。
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ranges := make([]GroupRange, len(s.ranges))
	copy(ranges, s.ranges)
	keyOwners := make(map[string]int, len(s.values))
	values := make(map[string]string, len(s.values))
	for key, value := range s.values {
		group := s.keyGroup[key]
		owner, _ := OwnerOf(group, s.numGroups, s.parallelism)
		keyOwners[key] = owner
		values[key] = value
	}
	return Snapshot{
		Parallelism: s.parallelism,
		NumGroups:   s.numGroups,
		Ranges:      ranges,
		KeyOwners:   keyOwners,
		Values:      values,
	}
}

// Len 返回当前键的数量。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.values)
}

// GroupOf 返回键所属的键组（键组在存储生命周期内固定不变）。
func (s *Store) GroupOf(key string) (int, error) {
	if key == "" {
		return -1, ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.keyGroup[key]
	if !ok {
		return KeyToGroup(key, uint32(s.numGroups))
	}
	return group, nil
}

// NumGroups 返回固定的键组数量。
func (s *Store) NumGroups() int { return s.numGroups }

// Parallelism 返回当前并行度。
func (s *Store) Parallelism() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.parallelism
}

func (s *Store) logRescale(oldRanges, newRanges []GroupRange, result RescaleResult) {
	s.logger.Printf("[keygroup] 扩缩容输入: 键组数=%d 旧并行度=%d 新并行度=%d 当前键数=%d",
		s.numGroups, result.OldParallelism, result.NewParallelism, result.TotalKeys)
	s.logger.Printf("[keygroup] 旧区间: %s", formatRanges(oldRanges))
	s.logger.Printf("[keygroup] 新区间: %s", formatRanges(newRanges))
	for _, ownership := range result.Groups {
		verdict := "原地不动"
		if ownership.Moved {
			verdict = "整组迁移"
		}
		s.logger.Printf("[keygroup] 键组=%d 归属: 实例%d -> 实例%d 键数=%d 判定=%s 依据=逐键组比较前后归属",
			ownership.Group, ownership.OldOwner, ownership.NewOwner, ownership.KeyCount, verdict)
	}
	s.logger.Printf("[keygroup] 迁移结果: 迁移键组=%v 迁移键数=%d 判定依据=仅归属变化的键组整组迁移，键组内键值不重不丢",
		result.MovedGroups, result.MovedKeys)
}

func formatRanges(ranges []GroupRange) string {
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		parts[i] = fmt.Sprintf("实例%d=[%d,%d)", r.Instance, r.Start, r.End)
	}
	return strings.Join(parts, " ")
}
