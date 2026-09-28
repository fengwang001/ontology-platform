// Package dedup 实现基于事件时间（event time）去重窗口的组件。
//
// 设计规则：
//   - 水位线随见过的最大事件时间单调前进；
//   - 每个标识只记住它被判为新事件那条事件的时间，水位线达到
//     「记忆时间 + 有效期」时记忆立即过期清除（边界相等即过期）；
//   - 重复事件丢弃并计数，不刷新记忆；
//   - 非法参数、空标识、处理后记忆条数超限都会被拒绝，拒绝不改变任何状态。
package dedup

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// RejectReason 是输入被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonInvalidConfig 表示构造参数非法（有效期或记忆上限不为正）。
	ReasonInvalidConfig RejectReason = "invalid_config"
	// ReasonEmptyID 表示事件标识为空。
	ReasonEmptyID RejectReason = "empty_id"
	// ReasonMemoryLimit 表示接受该新事件会使记忆条数超过配置上限。
	ReasonMemoryLimit RejectReason = "memory_limit_exceeded"
)

// Config 是去重器的构造参数。
type Config struct {
	// TTL 是每条去重记忆的有效期，必须为正。
	TTL time.Duration
	// MaxEntries 是去重记忆允许保留的最大条数，必须为正。
	MaxEntries int
	// Logger 用于打印输入、新/重复判定及判定依据；为 nil 时使用 slog.Default()。
	Logger *slog.Logger
}

// Event 是一条带事件时间的输入事件。
type Event struct {
	// ID 是事件标识，去重以它为键，不允许为空。
	ID string
	// Time 是事件时间（而非处理时间）。
	Time time.Time
}

// Outcome 是 Process 对单条事件的判定结果类别。
type Outcome string

const (
	// OutcomeNew 表示事件为首次出现，已输出并记入记忆。
	OutcomeNew Outcome = "new"
	// OutcomeDuplicate 表示事件为重复，已丢弃并计入重复计数。
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeRejected 表示事件因非法原因被拒绝，状态无任何变化。
	OutcomeRejected Outcome = "rejected"
)

// Result 是一次判定或一次快照读取返回的逐字段一致状态。
type Result struct {
	// Outcome 为判定类别；快照读取时为空。
	Outcome Outcome
	// Watermark 是当前事件时间水位线。
	Watermark time.Time
	// MemoryCount 是当前保留的去重记忆条数。
	MemoryCount int
	// Emitted 是累计判定为新事件并输出的条数。
	Emitted int64
	// Duplicates 是累计丢弃的重复事件条数。
	Duplicates int64
	// RememberedAt 仅在 new/duplicate 判定时有效：该标识被记住的事件时间。
	// 重复时返回的始终是首现那条事件的时间，用于体现「重复不刷新」。
	RememberedAt time.Time
	// ExpiresAt 仅在 new/duplicate 判定时有效：RememberedAt + TTL。
	ExpiresAt time.Time
	// Late 表示该事件时间是否落后于处理前水位线（迟到但不丢弃）。
	Late bool
	// Reason 仅在 OutcomeRejected 时有效，给出可区分的拒绝原因。
	Reason RejectReason
}

// RejectError 在事件被拒绝时返回，携带可区分原因。
type RejectError struct {
	reason RejectReason
	msg    string
}

// Error 实现 error。
func (e *RejectError) Error() string { return e.msg }

// Reason 返回拒绝原因。
func (e *RejectError) Reason() RejectReason { return e.reason }

func reject(reason RejectReason, format string, args ...any) error {
	return &RejectError{reason: reason, msg: fmt.Sprintf(format, args...)}
}

// memory 是单个标识的去重记忆，只记录首现事件的时间及其过期时刻。
type memory struct {
	eventTime time.Time
	expiresAt time.Time
}

// Deduper 是事件时间去重窗口组件，可被并发使用。
type Deduper struct {
	ttl        time.Duration
	maxEntries int
	log        *slog.Logger

	mu sync.RWMutex
	// 以下字段只在持有 mu（读或写）时访问；map 的写操作必须持有写锁。
	watermark  time.Time
	memories   map[string]memory
	emitted    int64
	duplicates int64
}

// New 校验配置并构造去重器。TTL 与 MaxEntries 必须为正，否则返回
// ReasonInvalidConfig；调用方不得使用返回的 nil 去重器。
func New(cfg Config) (*Deduper, error) {
	if cfg.TTL <= 0 {
		return nil, reject(ReasonInvalidConfig, "dedup: ttl must be positive, got %s", cfg.TTL)
	}
	if cfg.MaxEntries <= 0 {
		return nil, reject(ReasonInvalidConfig, "dedup: max entries must be positive, got %d", cfg.MaxEntries)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Deduper{
		ttl:        cfg.TTL,
		maxEntries: cfg.MaxEntries,
		log:        log,
		memories:   make(map[string]memory),
	}, nil
}

// Process 处理一条事件并返回判定结果。
//
// 整个判定（水位线推进、过期清除、新/重判定、计数）在同一把写锁内完成，
// 因此要么全部生效、要么（被拒绝时）全部不生效，并发读者不会看到中间态。
// 被拒绝时返回 *RejectError，且 Result 为零值。
func (d *Deduper) Process(e Event) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. 输入校验：空标识（含纯空白）拒绝，且发生在任何状态变更之前。
	if strings.TrimSpace(e.ID) == "" {
		err := reject(ReasonEmptyID, "dedup: event id must not be empty (event time %s)", e.Time.Format(time.RFC3339Nano))
		d.log.LogAttrs(nil, slog.LevelInfo, "dedup reject",
			slog.String("id", e.ID),
			slog.Time("event_time", e.Time),
			slog.String("reason", string(ReasonEmptyID)),
		)
		return Result{}, err
	}

	oldWM := d.watermark
	// 2. 水位线随最大事件时间单调前进。
	newWM := oldWM
	if e.Time.After(newWM) {
		newWM = e.Time
	}
	late := e.Time.Before(oldWM)

	// 3. 以候选水位线预判本次将过期的记忆（边界相等即过期），但先不改 map：
	//    若随后因容量被拒绝，记忆与水位线都必须保持原样。
	var expired []string
	targetSurvives := false
	var survivedMem memory
	for id, m := range d.memories {
		if !m.expiresAt.After(newWM) {
			expired = append(expired, id)
			continue
		}
		if id == e.ID {
			targetSurvives = true
			survivedMem = m
		}
	}

	// 4. 判定新/重并提交。
	if targetSurvives {
		// 重复：丢弃、计数，记忆（含首现时间）原样保留，不刷新。
		d.watermark = newWM
		d.purge(expired)
		d.duplicates++

		r := Result{
			Outcome:      OutcomeDuplicate,
			Watermark:    d.watermark,
			MemoryCount:  len(d.memories),
			Emitted:      d.emitted,
			Duplicates:   d.duplicates,
			RememberedAt: survivedMem.eventTime,
			ExpiresAt:    survivedMem.expiresAt,
			Late:         late,
		}
		d.log.LogAttrs(nil, slog.LevelInfo, "dedup duplicate",
			slog.String("id", e.ID),
			slog.Time("event_time", e.Time),
			slog.Time("watermark", d.watermark),
			slog.Time("remembered_at", survivedMem.eventTime),
			slog.Time("expires_at", survivedMem.expiresAt),
			slog.Bool("late", late),
			slog.String("basis", "id remembered and not expired at current watermark; memory not refreshed"),
		)
		return r, nil
	}

	// 新事件：先检查接受后记忆条数是否超限；超限则拒绝，不提交任何变更
	// （水位线不推进、过期项不清除、计数不变、无输出）。
	//
	// 若新记忆的过期点已不晚于候选水位线（典型为迟到事件），它在写入的
	// 同一刻即满足过期条件、会被立即清除，因此不占用持久记忆条数。
	expiresAt := e.Time.Add(d.ttl)
	immediateExpire := !expiresAt.After(newWM)
	projected := len(d.memories) - len(expired)
	if !immediateExpire {
		projected++
	}
	if projected > d.maxEntries {
		err := reject(ReasonMemoryLimit,
			"dedup: accepting id %q would exceed max entries %d (surviving memories: %d)",
			e.ID, d.maxEntries, len(d.memories)-len(expired))
		d.log.LogAttrs(nil, slog.LevelInfo, "dedup reject",
			slog.String("id", e.ID),
			slog.Time("event_time", e.Time),
			slog.Time("watermark", d.watermark),
			slog.String("reason", string(ReasonMemoryLimit)),
			slog.Int("surviving", len(d.memories)-len(expired)),
			slog.Int("max_entries", d.maxEntries),
		)
		return Result{}, err
	}

	// 提交：推进水位线、清除过期记忆。事件照常输出（迟到也不丢弃）；
	// 记忆按首现事件时间写入，已满足过期条件的立即清除。
	d.watermark = newWM
	d.purge(expired)
	d.emitted++
	m := memory{eventTime: e.Time, expiresAt: expiresAt}
	if !immediateExpire {
		d.memories[e.ID] = m
	}

	r := Result{
		Outcome:      OutcomeNew,
		Watermark:    d.watermark,
		MemoryCount:  len(d.memories),
		Emitted:      d.emitted,
		Duplicates:   d.duplicates,
		RememberedAt: m.eventTime,
		ExpiresAt:    m.expiresAt,
		Late:         late,
	}
	d.log.LogAttrs(nil, slog.LevelInfo, "dedup new",
		slog.String("id", e.ID),
		slog.Time("event_time", e.Time),
		slog.Time("watermark", d.watermark),
		slog.Time("remembered_at", m.eventTime),
		slog.Time("expires_at", m.expiresAt),
		slog.Bool("late", late),
		slog.Bool("immediate_expire", immediateExpire),
		slog.String("basis", "id absent (or expired) at current watermark; first sighting emitted"),
	)
	return r, nil
}

// purge 删除给定标识的记忆，调用方必须已持有写锁。
func (d *Deduper) purge(ids []string) {
	for _, id := range ids {
		delete(d.memories, id)
	}
}

// Snapshot 返回同一时刻逐字段一致的只读状态。所有字段在同一把读锁内拷贝，
// 并发写入者无法让读者看到字段之间相互错位的中间态。
func (d *Deduper) Snapshot() Result {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return Result{
		Watermark:   d.watermark,
		MemoryCount: len(d.memories),
		Emitted:     d.emitted,
		Duplicates:  d.duplicates,
	}
}
