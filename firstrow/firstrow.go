// Package firstrow 在变更流（插入/撤回）上为每个键保留排序键最小的存活行，
// 并在首条发生变化时输出“先撤回旧首条、再写入新首条”的确定性日志条目。
package firstrow

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
)

// Op 表示变更流中的操作类型。
type Op int

const (
	// Insert 表示插入一行存活数据。
	Insert Op = iota
	// Retract 表示撤回一行此前插入且仍存活的数据。
	Retract
)

func (op Op) String() string {
	switch op {
	case Insert:
		return "insert"
	case Retract:
		return "retract"
	default:
		return "unknown"
	}
}

// Row 是变更流中的一行。
type Row struct {
	// Key 是去重分组键。
	Key string
	// ID 是同一键下存活行的唯一标识，也用于排序键并列时打破平局。
	ID string
	// Time 是可负的时间字段，排序键的第一比较项。
	Time int64
}

// Change 是批处理中的一条变更。
type Change struct {
	Op  Op
	Row Row
}

// Entry 是首条变化时输出的一条日志条目。
type Entry struct {
	Op  Op
	Row Row
}

// Reason 是拒绝一条变更（及其所在批）的可区分原因。
type Reason string

const (
	// ReasonEmptyID 表示行标识为空。
	ReasonEmptyID Reason = "empty id"
	// ReasonEmptyKey 表示行键为空。
	ReasonEmptyKey Reason = "empty key"
	// ReasonDuplicateID 表示插入了一个同键下已经存活的标识。
	ReasonDuplicateID Reason = "duplicate live id"
	// ReasonUnknownID 表示撤回了一个同键下并不存活的标识。
	ReasonUnknownID Reason = "retract of non-live id"
	// ReasonLimitExceeded 表示应用该插入后存活行数会超过上限。
	ReasonLimitExceeded Reason = "live row limit exceeded"
)

// RejectError 描述导致整批被拒绝的第一条非法变更。
type RejectError struct {
	// Index 是非法变更在输入批中的下标（从 0 开始）。
	Index  int
	Reason Reason
	Change Change
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("firstrow: batch rejected at change %d (%s): %s row id=%q",
		e.Index, e.Reason, e.Change.Op, e.Change.Row.ID)
}

// Options 配置 Dedup。
type Options struct {
	// MaxLiveRows 是所有键合计存活行数的硬上限，必须为正。
	MaxLiveRows int
	// Logger 用于打印输入、输出条目与判定依据；为空时不打印。
	Logger *slog.Logger
}

// Dedup 是保留首条去重组件。零值不可用，必须通过 New 创建。
type Dedup struct {
	mu          sync.RWMutex
	keys        map[string]*keyState
	total       int
	maxLiveRows int
	logger      *slog.Logger
}

// keyState 是单个键的存活行集合及其当前首条。
type keyState struct {
	rows map[string]Row
	head Row
}

// less 报告 a 的排序键是否严格小于 b：先比较可负的时间字段，再按标识字典序。
func less(a, b Row) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	return a.ID < b.ID
}

// New 创建一个 Dedup。
func New(opts Options) *Dedup {
	if opts.MaxLiveRows <= 0 {
		panic("firstrow: Options.MaxLiveRows must be positive")
	}
	return &Dedup{
		keys:        make(map[string]*keyState),
		maxLiveRows: opts.MaxLiveRows,
		logger:      opts.Logger,
	}
}

// NewTextLogger 返回一个写入 w 的文本 logger，其中去除了时间戳属性，
// 便于让相同输入序列产生字节级一致、可重复比对的日志。
func NewTextLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}))
}

// Apply 原子地应用一批变更：全部合法时才更新存活行并返回输出条目；
// 任一变更非法时整批拒绝，存活行与已产生的输出均不改变。
func (d *Dedup) Apply(changes []Change) ([]Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// working 惰性复制被触及的键状态，保证验证失败时真实状态原样保留。
	working := make(map[string]*keyState)
	simTotal := d.total
	var outputs []Entry

	state := func(key string) *keyState {
		if st, ok := working[key]; ok {
			return st
		}
		if st, ok := d.keys[key]; ok {
			cp := &keyState{rows: make(map[string]Row, len(st.rows)), head: st.head}
			for id, row := range st.rows {
				cp.rows[id] = row
			}
			working[key] = cp
			return cp
		}
		st := &keyState{rows: make(map[string]Row)}
		working[key] = st
		return st
	}

	for idx, change := range changes {
		row := change.Row
		d.logInput(idx, len(changes), change)

		// 字段校验优先级：键 -> 标识，保证拒绝原因确定可复现。
		if row.Key == "" {
			return d.reject(idx, change, ReasonEmptyKey)
		}
		if row.ID == "" {
			return d.reject(idx, change, ReasonEmptyID)
		}

		st := state(row.Key)
		switch change.Op {
		case Insert:
			if _, live := st.rows[row.ID]; live {
				return d.reject(idx, change, ReasonDuplicateID)
			}
			if simTotal+1 > d.maxLiveRows {
				return d.reject(idx, change, ReasonLimitExceeded)
			}

			oldHead, hadHead := st.rows[st.head.ID], len(st.rows) > 0
			st.rows[row.ID] = row
			simTotal++

			if !hadHead {
				st.head = row
				outputs = append(outputs, Entry{Op: Insert, Row: row})
				d.logEmit(idx, Insert, row, "first live row becomes head")
				continue
			}
			if less(row, oldHead) {
				outputs = append(outputs,
					Entry{Op: Retract, Row: oldHead},
					Entry{Op: Insert, Row: row},
				)
				st.head = row
				d.logEmit(idx, Retract, oldHead, "retract previous head before new head")
				d.logEmit(idx, Insert, row, "smaller sort key becomes head")
			} else {
				d.logDecision(idx, change, "accepted; head unchanged, no output")
			}

		case Retract:
			live, exists := st.rows[row.ID]
			if !exists {
				return d.reject(idx, change, ReasonUnknownID)
			}

			oldHead := st.head
			delete(st.rows, row.ID)
			simTotal--

			if row.ID != oldHead.ID {
				d.logDecision(idx, change, "accepted; retracted non-head row, head unchanged, no output")
				continue
			}

			outputs = append(outputs, Entry{Op: Retract, Row: live})
			d.logEmit(idx, Retract, live, "retracted current head")

			if len(st.rows) == 0 {
				st.head = Row{}
				d.logDecision(idx, change, "no live row remains for key")
				continue
			}
			next := pickHead(st.rows)
			st.head = next
			outputs = append(outputs, Entry{Op: Insert, Row: next})
			d.logEmit(idx, Insert, next, "next smallest sort key promoted to head")

		default:
			return d.reject(idx, change, Reason("unknown op"))
		}
	}

	// 全部合法：提交工作副本。
	for key, st := range working {
		if len(st.rows) == 0 {
			delete(d.keys, key)
		} else {
			d.keys[key] = st
		}
	}
	d.total = simTotal
	if d.logger != nil {
		d.logger.Info("batch committed", slog.Int("changes", len(changes)), slog.Int("entries", len(outputs)))
	}
	return outputs, nil
}

// Snapshot 返回逐键一致的当前首条快照，可在写入并发进行时安全读取。
func (d *Dedup) Snapshot() map[string]Row {
	d.mu.RLock()
	defer d.mu.RUnlock()
	snap := make(map[string]Row, len(d.keys))
	for key, st := range d.keys {
		snap[key] = st.head
	}
	return snap
}

// LiveRows 返回当前存活行总数。
func (d *Dedup) LiveRows() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.total
}

// pickHead 返回 rows 中排序键最小的行，结果确定且与 map 迭代顺序无关。
func pickHead(rows map[string]Row) Row {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	head := rows[ids[0]]
	for _, id := range ids[1:] {
		if candidate := rows[id]; less(candidate, head) {
			head = candidate
		}
	}
	return head
}

func (d *Dedup) reject(idx int, change Change, reason Reason) ([]Entry, error) {
	if d.logger != nil {
		d.logger.Error("change rejected; batch dropped, live rows and emitted log untouched",
			slog.Int("index", idx),
			slog.String("op", change.Op.String()),
			slog.String("key", change.Row.Key),
			slog.String("id", change.Row.ID),
			slog.Int64("time", change.Row.Time),
			slog.String("reason", string(reason)),
		)
	}
	return nil, &RejectError{Index: idx, Reason: reason, Change: change}
}

func (d *Dedup) logInput(idx, total int, change Change) {
	if d.logger == nil {
		return
	}
	d.logger.Info("input change",
		slog.Int("index", idx),
		slog.Int("batch_size", total),
		slog.String("op", change.Op.String()),
		slog.String("key", change.Row.Key),
		slog.String("id", change.Row.ID),
		slog.Int64("time", change.Row.Time),
	)
}

func (d *Dedup) logEmit(idx int, op Op, row Row, why string) {
	if d.logger == nil {
		return
	}
	d.logger.Info("output entry",
		slog.Int("index", idx),
		slog.String("op", op.String()),
		slog.String("key", row.Key),
		slog.String("id", row.ID),
		slog.Int64("time", row.Time),
		slog.String("because", why),
	)
}

func (d *Dedup) logDecision(idx int, change Change, why string) {
	if d.logger == nil {
		return
	}
	d.logger.Info("decision",
		slog.Int("index", idx),
		slog.String("op", change.Op.String()),
		slog.String("key", change.Row.Key),
		slog.String("id", change.Row.ID),
		slog.String("because", why),
	)
}

// AsRejectError 从 Apply 返回的错误中提取拒绝详情。
func AsRejectError(err error) (*RejectError, bool) {
	var reject *RejectError
	if errors.As(err, &reject) {
		return reject, true
	}
	return nil, false
}
