package observedset

import (
	"sort"
	"sync"
)

// DefaultAddLimit 是单个副本允许生成的添加记录数上限的默认值。
const DefaultAddLimit int64 = 1 << 20

// Logger 用于输出每步输入、集合内容与判定依据的日志。
// 默认使用空实现（不产生输出）；测试中可注入自定义实现。
type Logger interface {
	Logf(format string, args ...any)
}

// Option 配置新建副本。
type Option func(*Replica)

// WithAddLimit 设置该副本自身添加记录数的上限（必须为正）。
func WithAddLimit(limit int64) Option {
	return func(r *Replica) {
		if limit > 0 {
			r.addLimit = limit
		}
	}
}

// WithLogger 注入步骤日志记录器。
func WithLogger(l Logger) Option {
	return func(r *Replica) {
		if l != nil {
			r.log = l
		}
	}
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

// Tag 是一次成功添加所产生的全局唯一标签。
// 标签由“副本编号 + 该副本内单调递增计数”组成，
// 不同副本的标签天然不相交，同一标签在全局至多被生成一次。
type Tag struct {
	ReplicaID int
	Counter   int64
}

// Replica 是一个 OR-Set（observed-remove set）副本。
//
// 状态由两部分组成：
//   - adds：添加记录，标签 -> 元素名；
//   - tombstones：墓碑，记录已被观察到删除的标签。
//
// 元素 e 在集合中，当且仅当存在标签 t 满足
// adds[t] == e 且 t 不在 tombstones 中（t 为“存活标签”）。
//
// 所有方法均可被多个 goroutine 并发调用。
type Replica struct {
	mu         sync.RWMutex
	id         int
	counter    int64
	addLimit   int64
	adds       map[Tag]string
	tombstones map[Tag]struct{}
	log        Logger
}

// NewReplica 创建一个编号为 id 的副本。编号必须非负。
func NewReplica(id int, opts ...Option) (*Replica, error) {
	if id < 0 {
		return nil, ErrInvalidReplicaID
	}
	r := &Replica{
		id:         id,
		addLimit:   DefaultAddLimit,
		adds:       make(map[Tag]string),
		tombstones: make(map[Tag]struct{}),
		log:        nopLogger{},
	}
	for _, opt := range opts {
		opt(r)
	}
	r.log.Logf("new replica id=%d addLimit=%d", r.id, r.addLimit)
	return r, nil
}

// ID 返回复本编号。
func (r *Replica) ID() int { return r.id }

// Add 添加元素，生成并返回一个全新的唯一标签。
// 任何被拒情形（空串、超限）都在状态改变之前返回，
// 标签计数不会跳号，失败不留痕。
func (r *Replica) Add(element string) (Tag, error) {
	if r == nil {
		return Tag{}, ErrNilReplica
	}
	if element == "" {
		return Tag{}, ErrEmptyElement
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counter >= r.addLimit {
		r.log.Logf("replica %d ADD %q REJECTED: add limit %d reached", r.id, element, r.addLimit)
		return Tag{}, ErrTooManyAdds
	}
	// 全部前置校验通过后才分配标签：失败不消耗计数。
	r.counter++
	tag := Tag{ReplicaID: r.id, Counter: r.counter}
	r.adds[tag] = element
	r.log.Logf("replica %d ADD %q -> tag=%s; elements=%v", r.id, element, tag, r.elementsLocked())
	return tag, nil
}

// Remove 删除元素：把该元素“当前全部存活标签”加入墓碑，
// 而不是按元素名删除（后加入者胜出的关键）。
// 元素当前不存活则整体拒绝（ErrElementNotFound），墓碑不变。
func (r *Replica) Remove(element string) error {
	if r == nil {
		return ErrNilReplica
	}
	if element == "" {
		return ErrEmptyElement
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	live := r.liveTagsLocked(element)
	if len(live) == 0 {
		r.log.Logf("replica %d REMOVE %q REJECTED: not present; elements=%v", r.id, element, r.elementsLocked())
		return ErrElementNotFound
	}
	for _, tag := range live {
		r.tombstones[tag] = struct{}{}
	}
	r.log.Logf("replica %d REMOVE %q tombstoned tags=%v; elements=%v", r.id, element, live, r.elementsLocked())
	return nil
}

// Contains 判定元素当前是否在集合中，并记录判定依据（存活标签）。
func (r *Replica) Contains(element string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	live := r.liveTagsLocked(element)
	r.log.Logf("replica %d CONTAINS %q => %v (live tags=%v)", r.id, element, len(live) > 0, live)
	return len(live) > 0
}

// Elements 返回当前集合中全部元素的有序（字典序、去重）快照。
func (r *Replica) Elements() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	els := r.elementsLocked()
	r.log.Logf("replica %d ELEMENTS => %v", r.id, els)
	return els
}

// Explain 返回元素成员身份的判定依据：其全部存活标签（按
// 副本编号、计数升序，结果可复现）。切片为空即“不在集合中”。
func (r *Replica) Explain(element string) []Tag {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	live := r.liveTagsLocked(element)
	r.log.Logf("replica %d EXPLAIN %q live tags=%v", r.id, element, live)
	return live
}

// Check 执行内部一致性自检：
//   - 本副本生成的标签计数必须恰好为 1..counter 且无重复；
//   - 每条添加记录的元素名非空。
func (r *Replica) Check() error {
	if r == nil {
		return ErrNilReplica
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[int64]bool, r.counter)
	for tag, elem := range r.adds {
		if elem == "" {
			return ErrEmptyElement
		}
		if tag.ReplicaID == r.id {
			if tag.Counter < 1 || tag.Counter > r.counter || seen[tag.Counter] {
				return ErrInvariantBroken
			}
			seen[tag.Counter] = true
		}
	}
	for c := int64(1); c <= r.counter; c++ {
		if !seen[c] {
			return ErrInvariantBroken
		}
	}
	r.log.Logf("replica %d CHECK ok: adds=%d tombstones=%d counter=%d elements=%v",
		r.id, len(r.adds), len(r.tombstones), r.counter, r.elementsLocked())
	return nil
}

// MergeInto 把 src 的添加记录与墓碑分别取并集合并进 dst：
//   - 只修改 dst，src 保持不变；
//   - dst 与 src 为同一副本是合法空操作；
//   - 先在源副本上取不可变快照、再锁目标副本，
//     因此互逆方向的合并可同时进行，不会死锁。
func MergeInto(dst, src *Replica) error {
	if dst == nil || src == nil {
		return ErrNilReplica
	}
	if dst == src {
		dst.log.Logf("replica %d MERGE self no-op; elements=%v", dst.id, dst.Elements())
		return nil
	}
	src.mu.RLock()
	adds := make(map[Tag]string, len(src.adds))
	for tag, elem := range src.adds {
		adds[tag] = elem
	}
	tombs := make(map[Tag]struct{}, len(src.tombstones))
	for tag := range src.tombstones {
		tombs[tag] = struct{}{}
	}
	src.mu.RUnlock()

	dst.mu.Lock()
	added, tombAdded := 0, 0
	for tag, elem := range adds {
		existing, ok := dst.adds[tag]
		if ok && existing != elem {
			dst.mu.Unlock()
			return ErrTagConflict
		}
		if !ok {
			added++
		}
		dst.adds[tag] = elem
	}
	for tag := range tombs {
		if _, ok := dst.tombstones[tag]; !ok {
			tombAdded++
		}
		dst.tombstones[tag] = struct{}{}
	}
	els := dst.elementsLocked()
	dst.mu.Unlock()
	dst.log.Logf("replica %d MERGE from replica %d: new adds=%d new tombs=%d; elements=%v",
		dst.id, src.id, added, tombAdded, els)
	return nil
}

// liveTagsLocked 返回元素当前全部存活标签，顺序确定可复现。
// 调用者必须持有 r.mu（读锁或写锁）。
func (r *Replica) liveTagsLocked(element string) []Tag {
	live := make([]Tag, 0)
	for tag, elem := range r.adds {
		if elem == element {
			if _, dead := r.tombstones[tag]; !dead {
				live = append(live, tag)
			}
		}
	}
	sortTags(live)
	return live
}

// elementsLocked 返回去重并按字典序排列的存活元素列表。
func (r *Replica) elementsLocked() []string {
	set := make(map[string]struct{})
	for tag, elem := range r.adds {
		if _, dead := r.tombstones[tag]; !dead {
			set[elem] = struct{}{}
		}
	}
	els := make([]string, 0, len(set))
	for elem := range set {
		els = append(els, elem)
	}
	sort.Strings(els)
	return els
}

func sortTags(tags []Tag) {
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].ReplicaID != tags[j].ReplicaID {
			return tags[i].ReplicaID < tags[j].ReplicaID
		}
		return tags[i].Counter < tags[j].Counter
	})
}

// String 让标签在日志中可读且无歧义。
func (t Tag) String() string {
	return formatTag(t.ReplicaID, t.Counter)
}
