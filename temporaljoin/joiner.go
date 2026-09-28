// Package temporaljoin 实现流与版本表（versioned table）之间的时态连接：
// 每个事件被连接到其事件时间那一刻生效的版本。
//
// 版本语义
//
// 每个键的版本按生效起点（EffectiveAt）升序排列，生效区间左闭右开：
// 版本 v 在 [v.EffectiveAt, next.EffectiveAt) 内生效，最后一个版本生效到正无穷。
// 墓碑（tombstone）表示该键在其区间内无值。事件的查询规则为：取生效起点不大于
// 事件时间且最大的那个版本；不存在或该版本为墓碑时输出未命中。
//
// 迟到判定
//
// 水位线（watermark）单调非降。版本变更仅当其生效起点严格晚于当前水位线时才被
// 接受，否则以“迟到的版本变更”拒绝；事件同理，事件时间不晚于水位线即迟到。
// 事件先进入有界缓冲，当水位线推进到不早于其事件时间时被连接并输出，每个被接受
// 的事件恰好输出一次，输出按（事件时间, 接受序号）升序。
//
// 全部方法可被并发调用；被拒绝的操作不会改变版本表、水位线、缓冲或已输出结果。
package temporaljoin

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
)

// RejectReason 是操作被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonEmptyKey 空键。
	ReasonEmptyKey RejectReason = "empty_key"
	// ReasonLateVersion 迟到的版本变更（生效起点不晚于当前水位线）。
	ReasonLateVersion RejectReason = "late_version_change"
	// ReasonWatermarkRegression 水位线回退。
	ReasonWatermarkRegression RejectReason = "watermark_regression"
	// ReasonBufferOverflow 待连接事件缓冲超限。
	ReasonBufferOverflow RejectReason = "buffer_overflow"
	// ReasonLateEvent 迟到事件（事件时间不晚于当前水位线）。
	ReasonLateEvent RejectReason = "late_event"
	// ReasonInvalidConfig 非法配置（如缓冲容量 <= 0）。
	ReasonInvalidConfig RejectReason = "invalid_config"
)

// JoinBasis 描述一条连接输出的判定依据。
type JoinBasis string

const (
	// BasisHit 命中一个有效值版本。
	BasisHit JoinBasis = "hit"
	// BasisTombstone 命中墓碑：区间内该键无值。
	BasisTombstone JoinBasis = "tombstone"
	// BasisNoVersion 事件时间之前不存在任何版本。
	BasisNoVersion JoinBasis = "no_version"
)

// RejectError 表示一次被拒绝的操作；Reason 可用于程序化区分原因。
// 被拒绝的操作不会改变版本表、水位线、缓冲或已输出结果。
type RejectError struct {
	Op     string
	Reason RejectReason
	Detail string
}

func (e *RejectError) Error() string {
	return "temporaljoin: " + e.Op + ": " + string(e.Reason) + ": " + e.Detail
}

// Version 是版本表中的一个版本条目。
type Version struct {
	// EffectiveAt 为生效起点（含），区间左闭右开。
	EffectiveAt int64
	// Value 为版本值；Tombstone 为 true 时无意义。
	Value []byte
	// Tombstone 标记该条目是否为墓碑（区间内该键无值）。
	Tombstone bool
}

// Event 是待连接的流事件。
type Event struct {
	Key       string
	EventTime int64
	Payload   []byte
}

// Result 是一条事件的时态连接结果。
type Result struct {
	// Seq 为事件被接受时分配的全局单调序号。
	Seq int64
	Key string
	// EventTime 为事件时间。
	EventTime int64
	Payload   []byte
	// Hit 为是否命中有效值；false 表示未命中（无版本或墓碑）。
	Hit bool
	// Value 为命中的版本值；Hit 为 false 时为 nil。
	Value []byte
	// VersionEffectiveAt 为被选中版本的生效起点；Basis 为 no_version 时为 0。
	VersionEffectiveAt int64
	// Basis 为判定依据：hit / tombstone / no_version。
	Basis JoinBasis
}

// Options 控制 Joiner 的容量与日志。
type Options struct {
	// BufferCapacity 为待连接事件缓冲的最大条目数，必须 > 0。
	BufferCapacity int
	// Logger 用于打印输入、连接结果与判定依据；为 nil 时使用 slog.Default()。
	Logger *slog.Logger
}

// bufferedEvent 是缓冲中的内部事件表示。
type bufferedEvent struct {
	seq       int64
	key       string
	eventTime int64
	payload   []byte
}

// Joiner 是并发安全的流-版本表时态连接组件。
// 单一互斥锁串行化所有状态变更：被拒绝的操作在任何状态写入之前返回。
type Joiner struct {
	mu       sync.Mutex
	table    map[string][]Version // key -> 按 EffectiveAt 升序的版本
	buffer   []bufferedEvent      // 尚未到连接时机的事件
	wm       int64                // 当前水位线
	seq      int64                // 已接受事件的全局单调序号
	capacity int
	logger   *slog.Logger
}

// NewJoiner 创建一个时态连接组件。
func NewJoiner(opts Options) (*Joiner, error) {
	if opts.BufferCapacity <= 0 {
		return nil, &RejectError{
			Op:     "new",
			Reason: ReasonInvalidConfig,
			Detail: fmt.Sprintf("buffer capacity must be positive, got %d", opts.BufferCapacity),
		}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Joiner{
		table:    make(map[string][]Version),
		wm:       math.MinInt64, // 初始水位线为 -∞：任何生效起点都晚于它
		capacity: opts.BufferCapacity,
		logger:   logger,
	}, nil
}

// PutVersion 写入（或在同一起点覆盖）一个有效值版本。
func (j *Joiner) PutVersion(key string, effectiveAt int64, value []byte) error {
	return j.putVersion(key, Version{EffectiveAt: effectiveAt, Value: copyBytes(value), Tombstone: false})
}

// PutTombstone 写入（或在同一起点覆盖）一个墓碑版本。
func (j *Joiner) PutTombstone(key string, effectiveAt int64) error {
	return j.putVersion(key, Version{EffectiveAt: effectiveAt, Tombstone: true})
}

// putVersion 是版本写入的公共路径：校验通过后才修改版本表。
func (j *Joiner) putVersion(key string, v Version) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if key == "" {
		j.reject("put_version", ReasonEmptyKey, "version key must not be empty",
			"key", key, "effective_at", v.EffectiveAt)
		return &RejectError{Op: "put_version", Reason: ReasonEmptyKey, Detail: "version key must not be empty"}
	}
	// 生效起点必须严格晚于水位线：等于水位线即为迟到（该区间已被关闭）。
	if v.EffectiveAt <= j.wm {
		detail := fmt.Sprintf("effective_at %d is not later than watermark %d", v.EffectiveAt, j.wm)
		j.reject("put_version", ReasonLateVersion, detail,
			"key", key, "effective_at", v.EffectiveAt, "watermark", j.wm)
		return &RejectError{Op: "put_version", Reason: ReasonLateVersion, Detail: detail}
	}

	versions := j.table[key]
	if i := searchVersion(versions, v.EffectiveAt); i < len(versions) && versions[i].EffectiveAt == v.EffectiveAt {
		// 同一起点覆盖。
		versions[i] = v
	} else {
		versions = append(versions, Version{})
		copy(versions[i+1:], versions[i:])
		versions[i] = v
	}
	j.table[key] = versions

	j.logger.Info("version accepted",
		"op", "put_version",
		"key", key,
		"effective_at", v.EffectiveAt,
		"tombstone", v.Tombstone,
		"basis", "effective_at > watermark",
		"watermark", j.wm,
	)
	return nil
}

// PutEvent 接受一个流事件并将其缓冲，返回分配到的全局单调序号。
// 事件在水位线推进到不早于其事件时间时被连接输出，且恰好输出一次。
func (j *Joiner) PutEvent(key string, eventTime int64, payload []byte) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if key == "" {
		j.reject("put_event", ReasonEmptyKey, "event key must not be empty",
			"key", key, "event_time", eventTime)
		return 0, &RejectError{Op: "put_event", Reason: ReasonEmptyKey, Detail: "event key must not be empty"}
	}
	if eventTime <= j.wm {
		detail := fmt.Sprintf("event_time %d is not later than watermark %d", eventTime, j.wm)
		j.reject("put_event", ReasonLateEvent, detail,
			"key", key, "event_time", eventTime, "watermark", j.wm)
		return 0, &RejectError{Op: "put_event", Reason: ReasonLateEvent, Detail: detail}
	}
	if len(j.buffer) >= j.capacity {
		detail := fmt.Sprintf("pending buffer is full (%d/%d)", len(j.buffer), j.capacity)
		j.reject("put_event", ReasonBufferOverflow, detail,
			"key", key, "event_time", eventTime, "buffer_size", len(j.buffer), "capacity", j.capacity)
		return 0, &RejectError{Op: "put_event", Reason: ReasonBufferOverflow, Detail: detail}
	}

	j.seq++
	seq := j.seq
	j.buffer = append(j.buffer, bufferedEvent{
		seq:       seq,
		key:       key,
		eventTime: eventTime,
		payload:   copyBytes(payload),
	})
	j.logger.Info("event accepted",
		"op", "put_event",
		"seq", seq,
		"key", key,
		"event_time", eventTime,
		"watermark", j.wm,
		"buffer_size", len(j.buffer),
	)
	return seq, nil
}

// AdvanceWatermark 推进水位线，并输出所有事件时间不晚于新水位线的缓冲事件，
// 输出按（事件时间, 接受序号）升序排列。
func (j *Joiner) AdvanceWatermark(wm int64) ([]Result, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if wm < j.wm {
		detail := fmt.Sprintf("new watermark %d < current watermark %d", wm, j.wm)
		j.reject("advance_watermark", ReasonWatermarkRegression, detail,
			"watermark", j.wm, "new_watermark", wm)
		return nil, &RejectError{Op: "advance_watermark", Reason: ReasonWatermarkRegression, Detail: detail}
	}
	if wm == j.wm {
		// 没有新关闭的时间区间；缓冲中不可能有 eventTime <= wm 的事件。
		return nil, nil
	}

	oldWM := j.wm
	j.wm = wm

	ready := make([]bufferedEvent, 0, len(j.buffer))
	pending := j.buffer[:0]
	for _, e := range j.buffer {
		if e.eventTime <= wm {
			ready = append(ready, e)
		} else {
			pending = append(pending, e)
		}
	}
	j.buffer = pending
	sort.SliceStable(ready, func(a, b int) bool {
		if ready[a].eventTime != ready[b].eventTime {
			return ready[a].eventTime < ready[b].eventTime
		}
		return ready[a].seq < ready[b].seq
	})

	results := make([]Result, 0, len(ready))
	for _, e := range ready {
		results = append(results, j.resolve(e, oldWM, wm))
	}

	j.logger.Info("watermark advanced",
		"op", "advance_watermark",
		"watermark", oldWM,
		"new_watermark", wm,
		"emitted", len(results),
		"buffer_size", len(j.buffer),
	)
	return results, nil
}

// resolve 对一个已到连接时机的事件执行朴素时点查询并记录判定依据。调用方持锁。
func (j *Joiner) resolve(e bufferedEvent, fromWM, toWM int64) Result {
	r := Result{
		Seq:       e.seq,
		Key:       e.key,
		EventTime: e.eventTime,
		Payload:   copyBytes(e.payload),
	}
	versions := j.table[e.key]
	idx := lookupIndex(versions, e.eventTime) // 最大的 EffectiveAt <= eventTime
	switch {
	case idx < 0:
		r.Basis = BasisNoVersion
		r.Hit = false
	case versions[idx].Tombstone:
		r.Basis = BasisTombstone
		r.Hit = false
		r.VersionEffectiveAt = versions[idx].EffectiveAt
	default:
		r.Basis = BasisHit
		r.Hit = true
		r.VersionEffectiveAt = versions[idx].EffectiveAt
		r.Value = copyBytes(versions[idx].Value)
	}
	j.logger.Info("event joined",
		"op", "join",
		"seq", r.Seq,
		"key", r.Key,
		"event_time", r.EventTime,
		"hit", r.Hit,
		"basis", r.Basis,
		"version_effective_at", r.VersionEffectiveAt,
		"joined_value", string(r.Value),
		"watermark_range", [2]int64{fromWM, toWM},
	)
	return r
}

// Watermark 返回当前水位线。
func (j *Joiner) Watermark() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.wm
}

// Pending 返回当前缓冲中等待连接的事件数。
func (j *Joiner) Pending() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.buffer)
}

// Lookup 对版本表执行一次朴素时点查询：
// 返回生效起点不大于 at 的最新版本值；无版本或墓碑时 ok 为 false。
// 该查询不改变任何状态，可用于与连接输出做一致性对照。
func (j *Joiner) Lookup(key string, at int64) (value []byte, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	versions := j.table[key]
	idx := lookupIndex(versions, at)
	if idx < 0 || versions[idx].Tombstone {
		return nil, false
	}
	return copyBytes(versions[idx].Value), true
}

// Versions 返回某个键当前已接受版本的有序快照（按生效起点升序）。
func (j *Joiner) Versions(key string) []Version {
	j.mu.Lock()
	defer j.mu.Unlock()
	versions := j.table[key]
	out := make([]Version, len(versions))
	for i, v := range versions {
		out[i] = Version{EffectiveAt: v.EffectiveAt, Tombstone: v.Tombstone, Value: copyBytes(v.Value)}
	}
	return out
}

// searchVersion 返回 versions 中第一个 EffectiveAt >= at 的下标（versions 升序）。
func searchVersion(versions []Version, at int64) int {
	return sort.Search(len(versions), func(i int) bool { return versions[i].EffectiveAt >= at })
}

// lookupIndex 返回生效起点不大于 at 的最新版本下标；不存在时返回 -1。
func lookupIndex(versions []Version, at int64) int {
	// 第一个 EffectiveAt > at，其前一个即 <= at 的最大者（左闭）。
	return sort.Search(len(versions), func(i int) bool { return versions[i].EffectiveAt > at }) - 1
}

// reject 记录一次拒绝。拒绝仅产生日志，不触碰任何状态。
func (j *Joiner) reject(op string, reason RejectReason, detail string, args ...any) {
	fields := append([]any{"op", op, "reason", reason, "detail", detail}, args...)
	j.logger.Info("operation rejected", fields...)
}

func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
