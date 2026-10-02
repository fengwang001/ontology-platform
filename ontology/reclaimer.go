package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 非法输入对应的可判定错误，互不相同，可用 errors.Is 判定。
var (
	// ErrNonPositiveReplicas 副本数非正。
	ErrNonPositiveReplicas = errors.New("replica count must be positive")
	// ErrReplicaOutOfRange 副本编号越界。
	ErrReplicaOutOfRange = errors.New("replica id out of range")
	// ErrNegativeComponent 向量含负分量。
	ErrNegativeComponent = errors.New("vector contains negative component")
	// ErrEmptyKey 键为空。
	ErrEmptyKey = errors.New("key is empty")
	// ErrVectorLengthMismatch 向量长度与副本数不一致。
	ErrVectorLengthMismatch = errors.New("vector length mismatch")
)

// Entry 是每个键的当前赢家条目。
type Entry struct {
	Vector    Vector // 赢家版本向量
	Value     string // 值
	Tombstone bool   // 是否墓碑
}

// Event 是一次写入或删除事件。
type Event struct {
	Replica   int    // 产生该事件的副本编号
	Key       string // 键
	Vector    Vector // 事件携带的版本向量
	Value     string // 写入值（删除时忽略）
	Tombstone bool   // true 表示删除，产生墓碑
}

// View 是回收器某一时刻的只读快照。
type View struct {
	Entries   map[string]Entry // 每个键的当前赢家条目
	Clocks    []Vector         // 各副本已见过的最高向量
	Stable    Vector           // 稳定向量
	Reclaimed []string         // 已回收的键列表
}

// Reclaimer 是基于版本向量的因果稳定墓碑回收器。
type Reclaimer struct {
	mu           sync.RWMutex
	replicaCount int
	clocks       []Vector
	entries      map[string]Entry
	reclaimed    []string
	// reclaimedVectors 记录已回收键的墓碑向量，作为判定水位，
	// 防止回收后迟到的旧事件复活已删键。
	reclaimedVectors map[string]Vector
}

// NewReclaimer 创建回收器；replicaCount 非正时返回 ErrNonPositiveReplicas。
func NewReclaimer(replicaCount int) (*Reclaimer, error) {
	if replicaCount <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrNonPositiveReplicas, replicaCount)
	}
	clocks := make([]Vector, replicaCount)
	for i := range clocks {
		clocks[i] = make(Vector, replicaCount)
	}
	return &Reclaimer{
		replicaCount:     replicaCount,
		clocks:           clocks,
		entries:          make(map[string]Entry),
		reclaimedVectors: make(map[string]Vector),
	}, nil
}

// validate 校验单条事件，返回可判定的非法输入错误。
func (r *Reclaimer) validate(e Event) error {
	if e.Replica < 0 || e.Replica >= r.replicaCount {
		return fmt.Errorf("%w: replica %d, replica count %d", ErrReplicaOutOfRange, e.Replica, r.replicaCount)
	}
	if e.Key == "" {
		return fmt.Errorf("%w: replica %d", ErrEmptyKey, e.Replica)
	}
	if len(e.Vector) != r.replicaCount {
		return fmt.Errorf("%w: key %q vector length %d, replica count %d", ErrVectorLengthMismatch, e.Key, len(e.Vector), r.replicaCount)
	}
	for i, c := range e.Vector {
		if c < 0 {
			return fmt.Errorf("%w: key %q component %d is %d", ErrNegativeComponent, e.Key, i, c)
		}
	}
	return nil
}

// Apply 批量应用事件。任一条非法则整批不生效，状态完全不变。
func (r *Reclaimer) Apply(events []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 先整体校验：任一非法则整批拒绝，存储、时钟、稳定向量与已回收列表均不变。
	for _, e := range events {
		if err := r.validate(e); err != nil {
			return err
		}
	}
	for _, e := range events {
		// 基线向量：当前赢家向量；若键已回收，则为已回收墓碑向量。
		var baseline Vector
		if winner, ok := r.entries[e.Key]; ok {
			baseline = winner.Vector
		} else if reclaimed, ok := r.reclaimedVectors[e.Key]; ok {
			baseline = reclaimed
		}
		// 仅当事件向量字典序大于基线向量时覆盖；相等或更小一律忽略。
		if baseline == nil || Compare(e.Vector, baseline) > 0 {
			r.entries[e.Key] = Entry{
				Vector:    e.Vector.Clone(),
				Value:     e.Value,
				Tombstone: e.Tombstone,
			}
			delete(r.reclaimedVectors, e.Key)
		}
		// 无论事件是否被忽略，副本时钟都照常取最大：
		// 该副本至少产出了或追平了这个向量。
		r.clocks[e.Replica] = Max(r.clocks[e.Replica], e.Vector)
	}
	return nil
}

// stableLocked 计算稳定向量，调用方需持有读锁或写锁。
func (r *Reclaimer) stableLocked() Vector {
	stable := r.clocks[0].Clone()
	for _, clock := range r.clocks[1:] {
		for i := range stable {
			if clock[i] < stable[i] {
				stable[i] = clock[i]
			}
		}
	}
	return stable
}

// StableVector 返回稳定向量（各副本时钟逐分量取最小值）。
func (r *Reclaimer) StableVector() Vector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stableLocked()
}

// Reclaim 回收所有墓碑且向量逐分量不超过稳定向量的条目，
// 返回被移除的键列表（按字典序）。
func (r *Reclaimer) Reclaim() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	stable := r.stableLocked()
	var removed []string
	for key, entry := range r.entries {
		if entry.Tombstone && LessEqual(entry.Vector, stable) {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	for _, key := range removed {
		r.reclaimedVectors[key] = r.entries[key].Vector.Clone()
		delete(r.entries, key)
	}
	r.reclaimed = append(r.reclaimed, removed...)
	return removed
}

// View 返回当前状态的只读快照，可并发调用。
func (r *Reclaimer) View() View {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make(map[string]Entry, len(r.entries))
	for key, entry := range r.entries {
		entries[key] = Entry{
			Vector:    entry.Vector.Clone(),
			Value:     entry.Value,
			Tombstone: entry.Tombstone,
		}
	}
	clocks := make([]Vector, len(r.clocks))
	for i, clock := range r.clocks {
		clocks[i] = clock.Clone()
	}
	reclaimed := make([]string, len(r.reclaimed))
	copy(reclaimed, r.reclaimed)
	return View{
		Entries:   entries,
		Clocks:    clocks,
		Stable:    r.stableLocked(),
		Reclaimed: reclaimed,
	}
}

// SelfCheck 校验内部不变量，可并发调用；违反时返回非 nil 错误。
func (r *Reclaimer) SelfCheck() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stable := r.stableLocked()
	ceiling := make(Vector, r.replicaCount)
	for i, clock := range r.clocks {
		if len(clock) != r.replicaCount {
			return fmt.Errorf("self-check: clock %d length %d, want %d", i, len(clock), r.replicaCount)
		}
		for j, c := range clock {
			if c < 0 {
				return fmt.Errorf("self-check: clock %d component %d negative", i, j)
			}
			if c > ceiling[j] {
				ceiling[j] = c
			}
		}
		if !LessEqual(stable, clock) {
			return fmt.Errorf("self-check: stable vector %v exceeds clock %d %v", stable, i, clock)
		}
	}
	for key, entry := range r.entries {
		if key == "" {
			return errors.New("self-check: empty key in entries")
		}
		if len(entry.Vector) != r.replicaCount {
			return fmt.Errorf("self-check: entry %q vector length %d, want %d", key, len(entry.Vector), r.replicaCount)
		}
		if !LessEqual(entry.Vector, ceiling) {
			return fmt.Errorf("self-check: entry %q vector %v exceeds max clock %v", key, entry.Vector, ceiling)
		}
	}
	seen := make(map[string]bool, len(r.reclaimed))
	for _, key := range r.reclaimed {
		if seen[key] {
			return fmt.Errorf("self-check: key %q reclaimed twice", key)
		}
		seen[key] = true
		if _, ok := r.entries[key]; ok {
			return fmt.Errorf("self-check: reclaimed key %q still in entries", key)
		}
	}
	return nil
}
