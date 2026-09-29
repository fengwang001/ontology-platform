// Package ontology 提供按事件时间仲裁的最后写入胜出（LWW）物化视图。
//
// 仲裁规则：
//   - 每个键维护当前已生效记录的事件时间（即使键被删除也保留事件时间）。
//   - 事件时间严格大于当前事件时间时生效；相等时先到者胜，后续一律忽略。
//   - 生效时若键存在旧值，先输出撤回，再输出建立（写入或删除标记）。
//   - 值为 nil 表示删除；非 nil（含空字符串）表示写入，空值是与“不存在”不同的状态。
package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// 可区分的批量拒绝原因。
var (
	// ErrInvalidArgument 表示事件时间非法、批次为 nil 等参数错误。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrEmptyKey 表示批次中存在空键。
	ErrEmptyKey = errors.New("ontology: empty key")
	// ErrTooManyKeys 表示批次生效后不同键的数量会超过构造时给定的上限。
	ErrTooManyKeys = errors.New("ontology: too many distinct keys")
)

// Event 是一条写入或删除事件。Value 为 nil 表示删除；非 nil 表示写入。
type Event struct {
	Key       string
	EventTime int64
	Value     *string
}

// Record 是某个键当前物化的记录。
type Record struct {
	Exists    bool
	Value     string
	EventTime int64
}

// ChangeKind 标记一条变更日志的类型。
type ChangeKind int

const (
	// ChangeRetract 撤回键此前存在的值。
	ChangeRetract ChangeKind = iota + 1
	// ChangeEstablish 建立新值。
	ChangeEstablish
	// ChangeTombstone 建立“不存在”标记（删除生效）。
	ChangeTombstone
)

// Change 是一条撤回或建立变更。序列号在单个视图内单调递增、可复现。
type Change struct {
	Seq            int64
	Kind           ChangeKind
	Key            string
	OldValue       string
	OldEventTime   int64
	NewValue       string
	NewEventTime   int64
	CauseEventTime int64
}

// ViewSnapshot 是某一时刻视图状态与统计的一致性快照。
type ViewSnapshot struct {
	Records       map[string]Record
	DroppedEvents int64
	AppliedEvents int64
	ChangeCount   int64
	KeyCount      int
}

// MaterializedView 是按事件时间仲裁的键值物化视图。
type MaterializedView struct {
	mu  sync.RWMutex
	pub atomic.Pointer[publishedView]

	maxKeys int
	state   map[string]*entry
	log     []Change
	dropped int64
	applied int64
}

// publishedView 是一次 Apply 完成后原子发布的不可变快照。
// 所有读路径无锁加载同一指针，因此并发读者逐字段看到相同版本。
type publishedView struct {
	records map[string]Record
	log     []Change
	dropped int64
	applied int64
	live    int
}

// entry 保存单个键的内部状态。seen 为 false 表示该键从未见过任何事件；
// 已删除的键 seen=true 且 exists=false，其事件时间继续参与迟到仲裁。
type entry struct {
	seen      bool
	exists    bool
	value     string
	eventTime int64
}

// NewMaterializedView 创建一个键数上限为 maxKeys 的视图。
func NewMaterializedView(maxKeys int) (*MaterializedView, error) {
	if maxKeys <= 0 {
		return nil, fmt.Errorf("%w: maxKeys must be positive, got %d", ErrInvalidArgument, maxKeys)
	}
	return &MaterializedView{
		maxKeys: maxKeys,
		state:   make(map[string]*entry),
	}, nil
}

// publishLocked 在写锁内构造新的不可变快照并原子发布。
func (v *MaterializedView) publishLocked() {
	records := make(map[string]Record, len(v.state))
	live := 0
	for key, cur := range v.state {
		if cur.exists {
			live++
			records[key] = Record{Exists: true, Value: cur.value, EventTime: cur.eventTime}
		}
	}
	logCopy := append([]Change(nil), v.log...)
	v.pub.Store(&publishedView{
		records: records,
		log:     logCopy,
		dropped: v.dropped,
		applied: v.applied,
		live:    live,
	})
}

// load 返回当前已发布快照；视图刚构造尚未写入时返回空快照。
func (v *MaterializedView) load() *publishedView {
	if p := v.pub.Load(); p != nil {
		return p
	}
	return &publishedView{records: map[string]Record{}, log: []Change{}}
}

// Apply 原子地应用一个批次：任一条事件非法则整批不生效。
func (v *MaterializedView) Apply(events []Event) ([]Change, error) {
	if events == nil {
		return nil, fmt.Errorf("%w: events batch must not be nil", ErrInvalidArgument)
	}
	for i := range events {
		if events[i].EventTime < 0 {
			return nil, fmt.Errorf("%w: event at index %d has negative event time %d",
				ErrInvalidArgument, i, events[i].EventTime)
		}
		if events[i].Key == "" {
			return nil, fmt.Errorf("%w: event at index %d with event time %d",
				ErrEmptyKey, i, events[i].EventTime)
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	// 干跑：以当前状态为起点按顺序模拟整批，校验生效路径上的键数上限。
	// 键数超限与空键、非法参数一样整体拒绝，提交时状态不变。
	sim := make(map[string]*entry, len(events))
	live := v.liveKeysLocked()
	for i := range events {
		ev := events[i]
		cur := sim[ev.Key]
		if cur == nil {
			if base := v.state[ev.Key]; base != nil {
				cp := *base
				cur = &cp
			} else {
				cur = &entry{}
			}
			sim[ev.Key] = cur
		}
		if cur.seen && ev.EventTime <= cur.eventTime {
			continue
		}
		if cur.exists {
			live--
		}
		cur.seen = true
		cur.eventTime = ev.EventTime
		cur.exists = ev.Value != nil
		if ev.Value != nil {
			cur.value = *ev.Value
			live++
		}
		if live > v.maxKeys {
			return nil, fmt.Errorf("%w: event at index %d (key %q) would raise live key count to %d, limit %d",
				ErrTooManyKeys, i, ev.Key, live, v.maxKeys)
		}
	}

	changes := make([]Change, 0)
	seq := int64(len(v.log))
	for i := range events {
		ev := events[i]
		cur := v.state[ev.Key]
		if cur != nil && cur.seen && ev.EventTime <= cur.eventTime {
			// 迟到事件或同事件时间的后到者：忽略并计数，不改变值与事件时间。
			v.dropped++
			continue
		}

		v.applied++
		if cur == nil {
			cur = &entry{}
			v.state[ev.Key] = cur
		}
		if cur.exists {
			seq++
			changes = append(changes, Change{
				Seq:            seq,
				Kind:           ChangeRetract,
				Key:            ev.Key,
				OldValue:       cur.value,
				OldEventTime:   cur.eventTime,
				CauseEventTime: ev.EventTime,
			})
		}

		cur.seen = true
		cur.eventTime = ev.EventTime
		if ev.Value != nil {
			cur.exists = true
			cur.value = *ev.Value
			seq++
			changes = append(changes, Change{
				Seq:            seq,
				Kind:           ChangeEstablish,
				Key:            ev.Key,
				NewValue:       *ev.Value,
				NewEventTime:   ev.EventTime,
				CauseEventTime: ev.EventTime,
			})
		} else {
			cur.exists = false
			cur.value = ""
			seq++
			changes = append(changes, Change{
				Seq:            seq,
				Kind:           ChangeTombstone,
				Key:            ev.Key,
				NewEventTime:   ev.EventTime,
				CauseEventTime: ev.EventTime,
			})
		}
	}
	v.log = append(v.log, changes...)
	v.publishLocked()

	out := append([]Change(nil), changes...)
	return out, nil
}

// Get 查询单个键的当前物化记录。
func (v *MaterializedView) Get(key string) (Record, bool) {
	r, ok := v.load().records[key]
	return r, ok
}

// Snapshot 返回当前所有存在的键的一致性拷贝。
func (v *MaterializedView) Snapshot() map[string]Record {
	src := v.load().records
	out := make(map[string]Record, len(src))
	for key, r := range src {
		out[key] = r
	}
	return out
}

// View 返回视图与统计的一致性快照。
func (v *MaterializedView) View() ViewSnapshot {
	p := v.load()
	records := make(map[string]Record, len(p.records))
	for key, r := range p.records {
		records[key] = r
	}
	return ViewSnapshot{
		Records:       records,
		DroppedEvents: p.dropped,
		AppliedEvents: p.applied,
		ChangeCount:   int64(len(p.log)),
		KeyCount:      p.live,
	}
}

// DroppedCount 返回因迟到或同事件时间仲裁而被忽略的事件数。
func (v *MaterializedView) DroppedCount() int64 {
	return v.load().dropped
}

// Changes 返回变更日志的完整拷贝。
func (v *MaterializedView) Changes() []Change {
	return v.ChangesSince(0)
}

// ChangesSince 返回序列号大于 sinceSeq 的变更日志拷贝。
func (v *MaterializedView) ChangesSince(sinceSeq int64) []Change {
	log := v.load().log
	idx := sort.Search(len(log), func(i int) bool { return log[i].Seq > sinceSeq })
	if idx >= len(log) {
		return []Change{}
	}
	return append([]Change(nil), log[idx:]...)
}

// liveKeysLocked 返回当前存在的键数；调用方需持有写锁。
func (v *MaterializedView) liveKeysLocked() int {
	live := 0
	for _, cur := range v.state {
		if cur.exists {
			live++
		}
	}
	return live
}
