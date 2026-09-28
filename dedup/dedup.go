// Package dedup 实现基于事件时间的去重窗口。
//
// 组件按事件标识（ID）去重：每个标识只记忆它第一次被判为新事件的事件时间；
// 去重记忆随事件时间水位线（watermark）单调推进，在水位线到达
// “记忆时间 + 有效期”时立即过期清除（含恰好落在边界上的情形）。
package dedup

import (
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Event 是进入去重窗口的事件。
type Event struct {
	// ID 为事件标识，空标识会被拒绝。
	ID string
	// Time 为事件时间（event time），零值会被拒绝。
	Time time.Time
}

// Config 为去重窗口的配置参数。
type Config struct {
	// TTL 为每条去重记忆的有效期，必须大于 0。
	TTL time.Duration
	// MaxEntries 为允许保留的记忆条数上限，必须大于 0。
	MaxEntries int
	// Logger 为可选日志器，为 nil 时使用 slog.Default()。
	Logger *slog.Logger
}

// Result 是单次 Process 的判定结果。
type Result struct {
	// Accepted 为 true 表示事件作为新事件被接受输出。
	Accepted bool
	// Duplicate 为 true 表示事件是重复事件，被丢弃。
	Duplicate bool
	// Watermark 为处理后的水位线；事件被拒绝时为未变化的当前水位线。
	Watermark time.Time
	// ExpiredIDs 为本次处理中因过期被清除的标识列表，
	// 按（过期时间, 标识）排序以保证确定性。
	ExpiredIDs []string
}

// State 是去重窗口某一时刻的一致快照。
type State struct {
	// Watermark 为当前水位线。
	Watermark time.Time
	// Memories 为标识 -> 该标识被判为新事件时的事件时间。
	Memories map[string]time.Time
	// DuplicateCount 为累计丢弃的重复事件数。
	DuplicateCount int64
	// AcceptedCount 为累计接受（输出）的新事件数。
	AcceptedCount int64
}

// Deduper 是并发安全的事件时间去重窗口。
type Deduper struct {
	mu sync.Mutex

	ttl        time.Duration
	maxEntries int
	logger     *slog.Logger

	// watermark 为已见过的最大事件时间，单调不减。
	watermark time.Time
	// memories 为标识 -> 该标识被判为新事件那条事件的事件时间。
	memories map[string]time.Time

	dupCount      int64
	acceptedCount int64
}

// New 根据配置创建去重窗口；参数非法时返回带 ErrInvalidParameter 的错误。
func New(cfg Config) (*Deduper, error) {
	if cfg.TTL <= 0 {
		return nil, newRejectError(ReasonInvalidParameter, ErrInvalidParameter)
	}
	if cfg.MaxEntries <= 0 {
		return nil, newRejectError(ReasonInvalidParameter, ErrInvalidParameter)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Deduper{
		ttl:        cfg.TTL,
		maxEntries: cfg.MaxEntries,
		logger:     logger,
		memories:   make(map[string]time.Time),
	}, nil
}

// Process 处理一个事件，返回判定结果与拒绝错误。
// 事件被接受或为重复（正常处理）时 error 为 nil；
// 事件非法（空标识、零时间、记忆超限）时返回 *RejectError，
// 且不会改变水位线、记忆、重复计数或已输出事件计数。
//
// 迟到事件（事件时间早于当前水位线）不会仅因迟到被丢弃：
// 其标识仍在记忆中则判重，记忆已过期或从未出现则作为新事件接受。
func (d *Deduper) Process(e Event) (Result, error) {
	d.logger.Info("dedup: event received", "id", e.ID, "event_time", formatTime(e.Time))

	if e.ID == "" {
		d.logger.Warn("dedup: event rejected", "reason", ReasonEmptyID)
		return d.currentResult(), newRejectError(ReasonEmptyID, ErrEmptyID)
	}
	if e.Time.IsZero() {
		d.logger.Warn("dedup: event rejected", "id", e.ID, "reason", ReasonInvalidTime)
		return d.currentResult(), newRejectError(ReasonInvalidTime, ErrInvalidTime)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// 候选水位线：随见过的最大事件时间单调前进（迟到事件不回退）。
	candidateWM := d.watermark
	if e.Time.After(candidateWM) {
		candidateWM = e.Time
	}

	// 在候选水位线下计算应过期的记忆：
	// 记忆时间 + TTL <= 水位线 即过期（恰好落在边界上也清除）。
	expired := make([]expiredEntry, 0)
	for id, mt := range d.memories {
		if !mt.Add(d.ttl).After(candidateWM) {
			expired = append(expired, expiredEntry{id: id, seenAt: mt})
		}
	}
	sortExpired(expired)

	if firstSeen, isDup := d.memories[e.ID]; isDup && !containsExpired(expired, e.ID) {
		// 重复事件：提交过期清除，但不刷新该标识的记忆时间。
		for _, ex := range expired {
			delete(d.memories, ex.id)
		}
		d.watermark = candidateWM
		d.dupCount++
		d.logger.Info("dedup: duplicate discarded",
			"id", e.ID,
			"event_time", formatTime(e.Time),
			"basis", "id already remembered",
			"first_seen", formatTime(firstSeen),
			"watermark", formatTime(candidateWM),
			"expired_count", len(expired),
		)
		return Result{
			Duplicate:  true,
			Watermark:  candidateWM,
			ExpiredIDs: expiredIDs(expired),
		}, nil
	}

	// 新事件的记忆是否能在候选水位线下存活：
	// 迟到事件的“记忆时间 + TTL”可能已不晚于水位线，即出生即过期，
	// 此时事件仍作为新事件输出，但不保留记忆、不占用记忆条数。
	retained := e.Time.Add(d.ttl).After(candidateWM)

	// 新事件：若连同过期清除后仍会超上限，则整体拒绝，不做任何变更。
	surviving := len(d.memories) - len(expired)
	if retained && surviving >= d.maxEntries {
		d.logger.Warn("dedup: event rejected",
			"id", e.ID,
			"event_time", formatTime(e.Time),
			"reason", ReasonMemoryLimit,
			"basis", "accepting would exceed MaxEntries after expiration",
			"surviving", surviving,
			"max_entries", d.maxEntries,
			"watermark_unchanged", formatTime(d.watermark),
		)
		return Result{Watermark: d.watermark}, newRejectError(ReasonMemoryLimit, ErrMemoryLimit)
	}

	// 提交：推进水位线、清除过期记忆、记住新标识（记忆时间 = 本事件时间）。
	for _, ex := range expired {
		delete(d.memories, ex.id)
	}
	d.watermark = candidateWM
	d.acceptedCount++
	if retained {
		d.memories[e.ID] = e.Time
	}
	d.logger.Info("dedup: event accepted as new",
		"id", e.ID,
		"event_time", formatTime(e.Time),
		"basis", "no live memory for id",
		"watermark", formatTime(candidateWM),
		"remembered_until", formatTime(e.Time.Add(d.ttl)),
		"retained", retained,
		"expired_count", len(expired),
	)
	return Result{
		Accepted:   true,
		Watermark:  candidateWM,
		ExpiredIDs: expiredIDs(expired),
	}, nil
}

// Snapshot 返回字段间相互一致的只读快照，可被并发调用。
// 返回的 map 为拷贝，调用方可自由读取而无需加锁。
func (d *Deduper) Snapshot() State {
	d.mu.Lock()
	defer d.mu.Unlock()

	memories := make(map[string]time.Time, len(d.memories))
	for id, t := range d.memories {
		memories[id] = t
	}
	return State{
		Watermark:      d.watermark,
		Memories:       memories,
		DuplicateCount: d.dupCount,
		AcceptedCount:  d.acceptedCount,
	}
}

// currentResult 返回不加锁也可构造的“未变化”结果（仅用于进入临界区前的拒绝路径，
// 此时不可能有其他字段需要读取，水位线取零值即可表达“未处理”）。
func (d *Deduper) currentResult() Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	return Result{Watermark: d.watermark}
}

type expiredEntry struct {
	id     string
	seenAt time.Time
}

// sortExpired 按（记忆时间, 标识）排序；TTL 为常量，记忆时间序即过期时间序，
// 以此保证输出与日志确定可复现。
func sortExpired(entries []expiredEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].seenAt.Equal(entries[j].seenAt) {
			return entries[i].id < entries[j].id
		}
		return entries[i].seenAt.Before(entries[j].seenAt)
	})
}

func containsExpired(entries []expiredEntry, id string) bool {
	for _, e := range entries {
		if e.id == id {
			return true
		}
	}
	return false
}

func expiredIDs(entries []expiredEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return ids
}

// formatTime 以 RFC3339Nano（UTC）格式输出时间，保证日志确定可复现。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
