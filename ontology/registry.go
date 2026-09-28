package ontology

import (
	"fmt"
	"sync"
)

// DefaultMaxVersions 是版本数的默认上限（含初始版本）。
const DefaultMaxVersions = 4096

// Registry 保存一张表全部历史版本的不可变结构快照，并串行化演进、
// 并发提供解码。任意时刻：
//   - 每个版本快照一经生成即不可变；
//   - 解码完整基于某一版本的事件快照与某一时刻的最新结构快照，
//     因此可与演进并发，且同一输入反复计算结果完全相同。
type Registry struct {
	mu sync.RWMutex

	// versions[v-1] 即版本 v 的不可变快照。
	versions []*Schema
	// nextID 为下一个可分配的列标识；单调递增，删除不回收，拒绝不回退。
	nextID ColumnID
	// maxVersions 为版本数上限。
	maxVersions int
	// logger 用于打印输入、解码结果与判定依据。
	logger Logger
}

// NewRegistry 创建一张表，columns 形成版本 1。
// columns 不得为空、列名不得为空且不得重名，否则返回
// ErrInvalidInitialColumns，且不会产生任何版本。
func NewRegistry(columns []ColumnSpec, opts ...Option) (*Registry, error) {
	if len(columns) == 0 {
		return nil, newError(ErrInvalidInitialColumns, "initial columns must not be empty")
	}
	nameSet := make(map[string]struct{}, len(columns))
	defs := make([]ColumnDef, 0, len(columns))
	for i, c := range columns {
		if c.Name == "" {
			return nil, newError(ErrInvalidInitialColumns, "column #%d has empty name", i+1)
		}
		if _, dup := nameSet[c.Name]; dup {
			return nil, newError(ErrInvalidInitialColumns, "duplicate initial column name %q", c.Name)
		}
		nameSet[c.Name] = struct{}{}
		defs = append(defs, ColumnDef{ID: ColumnID(i + 1), Name: c.Name, Default: c.Default})
	}

	r := &Registry{
		nextID:      ColumnID(len(columns)) + 1,
		maxVersions: DefaultMaxVersions,
		logger:      &stdLogger{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	snap := &Schema{Version: 1, Columns: defs, index: buildIndex(defs)}
	r.versions = append(r.versions, snap)
	r.logger.Logf("ontology: create table version=1 columns=%s", formatColumns(defs))
	return r, nil
}

// Evolve 校验 changes 并原子生成下一个版本。
// 任一操作非法则整体拒绝（ErrInvalidChange / ErrTooManyVersions），
// 版本列表与标识分配保持不变。
//
// 一批操作按顺序应用到同一工作副本，因此“新增后随即改名”合法，
// 而“重复删除同一列”非法。空批次不产生新版本。
func (r *Registry) Evolve(changes []Change) (Schema, error) {
	if len(changes) == 0 {
		return Schema{}, newError(ErrInvalidChange, "no changes supplied")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.versions) >= r.maxVersions {
		err := newError(ErrTooManyVersions, "version count %d reached limit %d", len(r.versions), r.maxVersions)
		r.logger.Logf("ontology: evolve rejected input=%s reason=%s", formatChanges(changes), err)
		return Schema{}, err
	}

	current := r.versions[len(r.versions)-1]
	// 在深拷贝上校验与应用；只有全部成功才提交，保证拒绝不留痕迹。
	working := cloneColumns(current.Columns)
	nameSet := make(map[string]struct{}, len(working))
	for _, c := range working {
		nameSet[c.Name] = struct{}{}
	}
	// pendingID 为本批预分配的标识；提交前不写回 r.nextID。
	pendingID := r.nextID

	for i, ch := range changes {
		switch ch.Kind {
		case ChangeAdd:
			if ch.Name == "" {
				return Schema{}, r.rejectChange(changes, i, "add column with empty name")
			}
			if _, dup := nameSet[ch.Name]; dup {
				return Schema{}, r.rejectChange(changes, i, "column name %q already exists", ch.Name)
			}
			working = append(working, ColumnDef{ID: pendingID, Name: ch.Name, Default: ch.Default})
			nameSet[ch.Name] = struct{}{}
			pendingID++
		case ChangeDrop:
			idx := indexOfID(working, ch.ID)
			if idx < 0 {
				return Schema{}, r.rejectChange(changes, i, "drop non-existent or already dropped column id=%d", ch.ID)
			}
			delete(nameSet, working[idx].Name)
			working = append(working[:idx], working[idx+1:]...)
		case ChangeRename:
			idx := indexOfID(working, ch.ID)
			if idx < 0 {
				return Schema{}, r.rejectChange(changes, i, "rename non-existent column id=%d", ch.ID)
			}
			if ch.Name == "" {
				return Schema{}, r.rejectChange(changes, i, "rename column id=%d to empty name", ch.ID)
			}
			if ch.Name != working[idx].Name {
				if _, dup := nameSet[ch.Name]; dup {
					return Schema{}, r.rejectChange(changes, i, "rename column id=%d to duplicate name %q", ch.ID, ch.Name)
				}
			}
			delete(nameSet, working[idx].Name)
			working[idx].Name = ch.Name
			nameSet[ch.Name] = struct{}{}
		default:
			return Schema{}, r.rejectChange(changes, i, "unknown change kind %d", ch.Kind)
		}
	}

	// 全部校验通过：提交新版本并推进标识分配。
	snap := &Schema{Version: len(r.versions) + 1, Columns: working, index: buildIndex(working)}
	r.versions = append(r.versions, snap)
	r.nextID = pendingID
	r.logger.Logf("ontology: evolve accepted input=%s new_version=%d columns=%s",
		formatChanges(changes), snap.Version, formatColumns(working))
	return snapshotValue(snap), nil
}

// rejectChange 记录拒绝日志并返回错误；调用方尚未提交任何状态。
func (r *Registry) rejectChange(changes []Change, index int, detail string, args ...any) error {
	err := newError(ErrInvalidChange, "change #%d %s", index+1, fmt.Sprintf(detail, args...))
	r.logger.Logf("ontology: evolve rejected input=%s reason=%s", formatChanges(changes), err)
	return err
}

// Current 返回当前最新版本的结构快照（不可变）。
func (r *Registry) Current() Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return snapshotValue(r.versions[len(r.versions)-1])
}

// SchemaAt 返回指定版本的结构快照；版本不存在返回 ErrVersionNotFound。
func (r *Registry) SchemaAt(version int) (Schema, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap, err := r.snapshotAtLocked(version)
	if err != nil {
		return Schema{}, err
	}
	return snapshotValue(snap), nil
}

// snapshotAtLocked 返回版本对应的只读快照指针（不拷贝），供同锁内解码使用。
func (r *Registry) snapshotAtLocked(version int) (*Schema, error) {
	if version < 1 || version > len(r.versions) {
		return nil, newError(ErrVersionNotFound, "version %d not found (current=%d)", version, len(r.versions))
	}
	return r.versions[version-1], nil
}

// Decode 把历史事件按列的稳定标识解码为当前最新结构下的一行。
//   - 事件版本不存在 -> ErrVersionNotFound；
//   - 值个数与该版本列数不符 -> ErrValueCountMismatch。
func (r *Registry) Decode(event Event) (Row, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	row, err := r.decodeLocked(event, r.versions[len(r.versions)-1])
	if err != nil {
		r.logger.Logf("ontology: decode rejected input=%s reason=%s", formatEvent(event), err)
		return Row{}, err
	}
	r.logger.Logf("ontology: decode accepted input=%s current_version=%d result=%s basis=%s",
		formatEvent(event), row.SchemaVersion, formatRow(row), formatBasis(row))
	return row, nil
}

// DecodeBatch 批量解码；任一事件被拒则整批失败，不返回部分结果。
// 整批在同一最新结构快照下解码，保证每个结果都完整基于同一版本结构。
func (r *Registry) DecodeBatch(events []Event) ([]Row, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current := r.versions[len(r.versions)-1]
	rows := make([]Row, 0, len(events))
	for i, ev := range events {
		row, err := r.decodeLocked(ev, current)
		if err != nil {
			code := ErrInvalidArgument
			msg := err.Error()
			if de, ok := err.(*DecodeError); ok {
				code = de.Code
				msg = de.Message
			}
			wrapped := newError(code, "batch item #%d (version=%d): %s", i+1, ev.Version, msg)
			r.logger.Logf("ontology: decode-batch rejected size=%d item=#%d input=%s reason=%s",
				len(events), i+1, formatEvent(ev), wrapped)
			return nil, wrapped
		}
		rows = append(rows, row)
	}
	r.logger.Logf("ontology: decode-batch accepted size=%d current_version=%d results=%s",
		len(events), current.Version, formatRows(rows))
	return rows, nil
}

// decodeLocked 必须在读锁内调用，基于固定的 event 快照与 current 快照解码。
func (r *Registry) decodeLocked(event Event, current *Schema) (Row, error) {
	if event.Version < 1 || event.Version > len(r.versions) {
		return Row{}, newError(ErrVersionNotFound, "event version %d not found (current=%d)", event.Version, len(r.versions))
	}
	eventSnap := r.versions[event.Version-1]
	if len(event.Values) != len(eventSnap.Columns) {
		return Row{}, newError(ErrValueCountMismatch,
			"event version %d has %d values but schema defines %d columns",
			event.Version, len(event.Values), len(eventSnap.Columns))
	}

	row := newRow(current)
	for _, col := range current.Columns {
		if idx, ok := eventSnap.index[col.ID]; ok {
			// 按稳定标识定位事件中的位置；空串同样是来自事件的合法值。
			row.set(col.ID, event.Values[idx], SourceEvent)
		} else {
			// 事件写入时该列尚不存在：取当前结构的默认值。
			row.set(col.ID, col.Default, SourceDefault)
		}
	}
	return row, nil
}

// VersionCount 返回当前版本总数。
func (r *Registry) VersionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.versions)
}

// ---- 内部辅助 ----

// buildIndex 构建 ID -> 列下标 的不可变索引。
func buildIndex(cols []ColumnDef) map[ColumnID]int {
	idx := make(map[ColumnID]int, len(cols))
	for i, c := range cols {
		idx[c.ID] = i
	}
	return idx
}

// cloneColumns 深拷贝列切片，保证旧快照不被演进修改。
func cloneColumns(cols []ColumnDef) []ColumnDef {
	out := make([]ColumnDef, len(cols))
	copy(out, cols)
	return out
}

// indexOfID 返回 ID 在 cols 中的下标，不存在返回 -1。
func indexOfID(cols []ColumnDef, id ColumnID) int {
	for i, c := range cols {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// snapshotValue 返回快照对外的值拷贝。Columns 深拷贝，调用方无法通过
// 返回值污染内部不可变快照；index 为包内只读 map，可安全共享。
func snapshotValue(s *Schema) Schema {
	return Schema{Version: s.Version, Columns: cloneColumns(s.Columns), index: s.index}
}
