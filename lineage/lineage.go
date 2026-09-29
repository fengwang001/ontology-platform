// Package lineage 提供数据血缘追踪能力：记录对象派生时的 输入→输出 血缘，
// 支持上游（谁产生我）与下游（我产生谁）双向追溯，并保证血缘始终指向正确版本。
package lineage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

// ObjectRef 唯一引用一个对象的某个具体版本。
type ObjectRef struct {
	ID      string
	Version int64
}

func (r ObjectRef) valid() bool { return r.ID != "" && r.Version > 0 }

// Record 是一条 输入→输出 派生血缘记录。
type Record struct {
	Operation string
	Inputs    []ObjectRef
	Outputs   []ObjectRef
}

// 可区分的拒绝原因。被拒绝的操作不会改变任何血缘状态。
var (
	// ErrInvalidRecord 派生操作缺少血缘记录（输入/输出为空等）。
	ErrInvalidRecord = errors.New("lineage: invalid derivation record")
	// ErrNoUpstream 查询上游时不存在任何生产血缘。
	ErrNoUpstream = errors.New("lineage: no upstream lineage")
	// ErrNoDownstream 查询下游时不存在任何消费血缘。
	ErrNoDownstream = errors.New("lineage: no downstream lineage")
	// ErrStaleVersion 血缘指向已过期版本（对象已变更产生新版本）。
	ErrStaleVersion = errors.New("lineage: lineage points to a stale version")
	// ErrUnknownVersion 查询或派生引用了从未注册/产生过的对象版本。
	ErrUnknownVersion = errors.New("lineage: unknown object version")
)

// storedEdge 是一条已持久化的血缘边。
type storedEdge struct {
	operation string
	from      ObjectRef
	to        ObjectRef
	// invalid 为 true 表示该边引用的某个版本已过期（对象发生了变更）。
	invalid bool
}

// trackerState 保存血缘图的全部可变状态。
type trackerState struct {
	// current[id] 是该对象当前（最新）版本；条目存在即代表版本已知。
	current map[string]int64
	// sources 记录由外部注册而非派生产生的对象 ID。
	sources map[string]bool
	// edges 是全部已记录的 输入→输出 血缘边，按记录顺序追加。
	edges []storedEdge
}

// Tracker 是并发安全的数据血缘追踪组件。
type Tracker struct {
	mu     sync.RWMutex
	state  trackerState
	logger *slog.Logger
}

// NewTracker 创建一个空的血缘追踪器，日志写入 slog 默认输出。
func NewTracker() *Tracker {
	return NewTrackerWithLogger(os.Stderr)
}

// NewTrackerWithLogger 创建使用指定日志输出的追踪器（w 为 nil 时丢弃日志）。
func NewTrackerWithLogger(w io.Writer) *Tracker {
	var handler slog.Handler
	if w == nil {
		handler = slog.NewTextHandler(io.Discard, nil)
	} else {
		handler = slog.NewTextHandler(w, nil)
	}
	return &Tracker{
		state: trackerState{
			current: make(map[string]int64),
			sources: make(map[string]bool),
		},
		logger: slog.New(handler),
	}
}

// RegisterSource 注册一个外部来源对象的初始版本。
// 注册同一对象的更新版本会推进当前版本，并使指向旧版本的血缘标记失效。
func (t *Tracker) RegisterSource(ctx context.Context, ref ObjectRef) error {
	if !ref.valid() {
		return ErrInvalidRecord
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	cur, known := t.state.current[ref.ID]
	if known {
		switch {
		case ref.Version == cur:
			return nil
		case ref.Version < cur, ref.Version != cur+1:
			t.logger.LogAttrs(ctx, slog.LevelWarn, "register-source rejected",
				slog.String("reason", "stale_version"),
				slog.String("id", ref.ID),
				slog.Int64("requested", ref.Version),
				slog.Int64("current", cur))
			return ErrStaleVersion
		}
	}

	t.state.current[ref.ID] = ref.Version
	t.state.sources[ref.ID] = true
	invalidated := t.invalidateLocked(ref.ID, ref.Version)
	t.logger.LogAttrs(ctx, slog.LevelInfo, "source registered",
		slog.String("id", ref.ID),
		slog.Int64("version", ref.Version),
		slog.Int("invalidated_edges", invalidated))
	return nil
}

// invalidateLocked 将所有引用 id 但不指向 newVersion 的血缘边标记失效，返回失效边数。
// 调用方必须持有写锁。newVersion 为 0（对象首次出现）时不做任何处理。
func (t *Tracker) invalidateLocked(id string, newVersion int64) int {
	if newVersion == 0 {
		return 0
	}
	count := 0
	for i := range t.state.edges {
		e := &t.state.edges[i]
		if e.invalid {
			continue
		}
		if (e.from.ID == id && e.from.Version != newVersion) ||
			(e.to.ID == id && e.to.Version != newVersion) {
			e.invalid = true
			count++
		}
	}
	return count
}

// RecordDerivation 在派生操作发生时原子地记录 输入→输出 血缘。
// 任一前置条件不满足则整体拒绝，血缘状态保持不变。
func (t *Tracker) RecordDerivation(ctx context.Context, rec Record) error {
	if err := validateRecord(rec); err != nil {
		t.logger.LogAttrs(ctx, slog.LevelWarn, "derivation rejected: invalid record",
			slog.String("reason", "invalid_record"),
			slog.String("operation", rec.Operation))
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	inputSet := make(map[string]bool, len(rec.Inputs))
	for _, in := range rec.Inputs {
		if inputSet[in.ID] {
			t.reject(ctx, "duplicated input", "invalid_record", rec.Operation, in.ID)
			return ErrInvalidRecord
		}
		inputSet[in.ID] = true
	}

	// 校验输入：必须引用每个输入对象的当前版本，否则血缘会指向过期版本。
	for _, in := range rec.Inputs {
		cur, known := t.state.current[in.ID]
		if !known || in.Version > cur {
			t.reject(ctx, "unknown input version", "unknown_version", rec.Operation, in.ID)
			return ErrUnknownVersion
		}
		if in.Version < cur {
			t.reject(ctx, "stale input version", "stale_version", rec.Operation, in.ID)
			return ErrStaleVersion
		}
	}

	// 校验输出：同一对象不得既是输入又是输出；新版本必须严格接续当前版本；
	// 全新对象必须从版本 1 开始。
	outputSet := make(map[string]ObjectRef, len(rec.Outputs))
	for _, out := range rec.Outputs {
		if _, dup := outputSet[out.ID]; dup {
			t.reject(ctx, "duplicated output", "invalid_record", rec.Operation, out.ID)
			return ErrInvalidRecord
		}
		outputSet[out.ID] = out
		if inputSet[out.ID] {
			t.reject(ctx, "id used as both input and output", "invalid_record", rec.Operation, out.ID)
			return ErrInvalidRecord
		}
		cur, known := t.state.current[out.ID]
		if known {
			if out.Version != cur+1 {
				t.reject(ctx, "stale or gapped output version", "stale_version", rec.Operation, out.ID)
				return ErrStaleVersion
			}
			continue
		}
		if out.Version != 1 {
			t.reject(ctx, "new output must start at v1", "unknown_version", rec.Operation, out.ID)
			return ErrUnknownVersion
		}
	}

	// 全部校验通过后才变更状态：先追加血缘边，再推进版本。
	for _, in := range rec.Inputs {
		for _, out := range rec.Outputs {
			t.state.edges = append(t.state.edges, storedEdge{
				operation: rec.Operation,
				from:      in,
				to:        out,
			})
		}
	}
	for _, out := range rec.Outputs {
		t.state.current[out.ID] = out.Version
		t.state.sources[out.ID] = false
		if n := t.invalidateLocked(out.ID, out.Version); n > 0 {
			t.logger.LogAttrs(ctx, slog.LevelInfo, "lineage edges invalidated by new output version",
				slog.String("id", out.ID),
				slog.Int64("version", out.Version),
				slog.Int("count", n))
		}
	}

	t.logger.LogAttrs(ctx, slog.LevelInfo, "derivation recorded",
		slog.String("operation", rec.Operation),
		slog.Any("inputs", rec.Inputs),
		slog.Any("outputs", rec.Outputs))
	return nil
}

func (t *Tracker) reject(ctx context.Context, msg, reason, operation, id string) {
	t.logger.LogAttrs(ctx, slog.LevelWarn, "derivation rejected: "+msg,
		slog.String("reason", reason),
		slog.String("operation", operation),
		slog.String("id", id))
}

// validateRecord 校验血缘记录本身：派生操作必须携带完整的输入与输出。
func validateRecord(rec Record) error {
	if rec.Operation == "" || len(rec.Inputs) == 0 || len(rec.Outputs) == 0 {
		return ErrInvalidRecord
	}
	for _, in := range rec.Inputs {
		if !in.valid() {
			return ErrInvalidRecord
		}
	}
	for _, out := range rec.Outputs {
		if !out.valid() {
			return ErrInvalidRecord
		}
	}
	return nil
}
