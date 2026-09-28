package ontology

import (
	"log/slog"
	"sync"
)

// DefaultMaxVersions 是未显式配置时的版本数上限。
const DefaultMaxVersions = 10000

// Event 是一条待解码的历史事件：它在 Version 版本写入，载荷为 Values，
// 值的顺序与该版本列顺序一一对应。
type Event struct {
	Version int
	Values  []string
}

// Option 配置注册表。
type Option func(*Registry)

// WithLogger 注入结构化日志记录器；为 nil 时回退到 slog.Default()。
func WithLogger(logger *slog.Logger) Option {
	return func(r *Registry) {
		r.logger = logger
	}
}

// WithMaxVersions 限制最多保留的版本数；必须 >= 1。
// 演进会使版本数超过该上限时，该次演进被拒绝（KindVersionLimitExceeded），
// 且不改变任何状态。
func WithMaxVersions(n int) Option {
	return func(r *Registry) {
		r.maxVersions = n
	}
}

// Registry 维护版本化表结构，支持并发演进与并发解码。
//
// 并发策略：演进只追加不可变版本快照（写锁），解码在读锁下钉住一致快照后
// 在锁外计算。每个解码结果完整基于钉住时的最新版本；即使解码期间发生演进，
// 本次结果也不会混用两个版本的结构，且同一输入反复计算结果完全相同。
type Registry struct {
	mu sync.RWMutex

	// versions 是已发布的不可变结构快照，按版本号顺序存放，零号元素对应版本 1。
	versions []Schema
	// nextID 是下一个可分配的列标识，单调递增、永不复用。
	nextID ColumnID
	// maxVersions 是版本数上限。
	maxVersions int
	// logger 记录输入、结果与判定依据。
	logger *slog.Logger
}

// NewRegistry 以初始列创建注册表，初始结构为版本 1。
// 初始列必须至少一列、列名非空且不重名，否则返回 KindInvalidSchema，
// 此时不会得到任何注册表实例。
func NewRegistry(columns []ColumnSpec, opts ...Option) (*Registry, error) {
	r := &Registry{
		maxVersions: DefaultMaxVersions,
		nextID:      1,
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	if r.maxVersions < 1 {
		return nil, newError(KindInvalidSchema, "new_registry", 0,
			"max versions must be >= 1, got %d", r.maxVersions)
	}
	if len(columns) == 0 {
		return nil, newError(KindInvalidSchema, "new_registry", 0,
			"initial schema must contain at least one column")
	}
	seen := make(map[string]struct{}, len(columns))
	initial := make([]Column, 0, len(columns))
	for i, spec := range columns {
		if spec.Name == "" {
			return nil, newError(KindInvalidSchema, "new_registry", 1,
				"column at position %d has empty name", i)
		}
		if _, dup := seen[spec.Name]; dup {
			return nil, newError(KindInvalidSchema, "new_registry", 1,
				"duplicate column name %q in initial schema", spec.Name)
		}
		seen[spec.Name] = struct{}{}
		initial = append(initial, Column{
			ID:      r.nextID,
			Name:    spec.Name,
			Default: spec.Default,
		})
		r.nextID++
	}
	r.versions = append(r.versions, newSchema(1, initial))
	r.logger.Info("registry created",
		slog.Int("schema_version", 1),
		slog.Int("column_count", len(initial)),
		slog.Int("max_versions", r.maxVersions))
	return r, nil
}

// AddColumn 在当前最新结构末尾新增一列并发布新版本。
// 列名必须非空且不与现存列重名（曾被删除的列名可以重新使用，
// 但新列会分配到全新的、从未使用过的标识）。
func (r *Registry) AddColumn(name, defaultValue string) (Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	latest := r.versions[len(r.versions)-1]
	if name == "" {
		return Schema{}, newError(KindInvalidEvolution, "add_column", latest.version,
			"new column name must not be empty")
	}
	if _, exists := latest.columnByName(name); exists {
		return Schema{}, newError(KindInvalidEvolution, "add_column", latest.version,
			"column name %q already exists in latest schema", name)
	}
	if err := r.checkVersionLimit("add_column", latest.version); err != nil {
		return Schema{}, err
	}

	cols := latest.cloneColumns()
	id := r.nextID
	r.nextID++
	cols = append(cols, Column{ID: id, Name: name, Default: defaultValue})
	next := r.publishLocked(cols)
	r.logger.Info("evolution applied",
		slog.String("op", "add_column"),
		slog.Int64("column_id", int64(id)),
		slog.String("column_name", name),
		slog.Int("new_version", next.version))
	return next, nil
}

// DropColumn 删除指定标识的列并发布新版本。
// 标识必须存在于当前最新结构中，且删除后至少保留一列；
// 被删除列的标识永不复用。
func (r *Registry) DropColumn(id ColumnID) (Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	latest := r.versions[len(r.versions)-1]
	if _, exists := latest.columnByID(id); !exists {
		return Schema{}, newError(KindInvalidEvolution, "drop_column", latest.version,
			"column id %d does not exist in latest schema", id)
	}
	if latest.width() <= 1 {
		return Schema{}, newError(KindInvalidEvolution, "drop_column", latest.version,
			"cannot drop column id %d: table must keep at least one column", id)
	}
	if err := r.checkVersionLimit("drop_column", latest.version); err != nil {
		return Schema{}, err
	}

	cols := make([]Column, 0, latest.width()-1)
	for _, c := range latest.Columns() {
		if c.ID != id {
			cols = append(cols, c)
		}
	}
	next := r.publishLocked(cols)
	r.logger.Info("evolution applied",
		slog.String("op", "drop_column"),
		slog.Int64("column_id", int64(id)),
		slog.Int("new_version", next.version))
	return next, nil
}

// RenameColumn 只修改指定列的名字并发布新版本，标识、位置与默认值均不变。
// 新名字必须非空且不与其他现存列冲突。若新名字与当前名字相同，
// 视为无变化，直接返回当前结构、不发布新版本。
func (r *Registry) RenameColumn(id ColumnID, newName string) (Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	latest := r.versions[len(r.versions)-1]
	col, exists := latest.columnByID(id)
	if !exists {
		return Schema{}, newError(KindInvalidEvolution, "rename_column", latest.version,
			"column id %d does not exist in latest schema", id)
	}
	if newName == "" {
		return Schema{}, newError(KindInvalidEvolution, "rename_column", latest.version,
			"new name for column id %d must not be empty", id)
	}
	if other, conflict := latest.columnByName(newName); conflict && other.ID != id {
		return Schema{}, newError(KindInvalidEvolution, "rename_column", latest.version,
			"cannot rename column id %d to %q: name already used by column id %d",
			id, newName, other.ID)
	}
	if col.Name == newName {
		return latest, nil
	}
	if err := r.checkVersionLimit("rename_column", latest.version); err != nil {
		return Schema{}, err
	}

	cols := latest.cloneColumns()
	pos := latest.indexByID(id)
	cols[pos].Name = newName
	next := r.publishLocked(cols)
	r.logger.Info("evolution applied",
		slog.String("op", "rename_column"),
		slog.Int64("column_id", int64(id)),
		slog.String("old_name", col.Name),
		slog.String("new_name", newName),
		slog.Int("new_version", next.version))
	return next, nil
}

// Latest 返回当前最新结构快照。
func (r *Registry) Latest() Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.versions[len(r.versions)-1]
}

// SchemaAt 返回指定版本（1 起）的结构快照；不存在时 ok 为 false。
func (r *Registry) SchemaAt(version int) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if version < 1 || version > len(r.versions) {
		return Schema{}, false
	}
	return r.versions[version-1], true
}

// VersionCount 返回当前已发布的版本数。
func (r *Registry) VersionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.versions)
}

// Decode 把在 version 版本写入的事件值解码为基于当前最新结构的一行。
//
// 取值规则只看列的稳定标识：事件值按其写入版本的列位置对齐到标识，
// 再投影到当前结构。当前结构中存在而事件结构里没有的列（晚于该事件新增），
// 取当前结构默认值并标记 FromDefault；已删除的列不出现在结果中。
// 空串是事件中的合法值，不等同于缺列：只有“事件所在版本根本没有该列”
// 才会落到默认值。
func (r *Registry) Decode(version int, values []string) (Row, error) {
	latest, eventSchema, err := r.snapshotsForDecode(version, len(values))
	if err != nil {
		r.logger.Warn("decode rejected",
			slog.String("reason", string(err.Kind)),
			slog.Int("input_version", version),
			slog.Int("value_count", len(values)),
			slog.String("detail", err.Message))
		return Row{}, err
	}

	row := projectRow(latest, eventSchema, values)
	r.logger.Info("decode accepted",
		slog.Int("input_version", version),
		slog.Any("input_values", values),
		slog.Int("result_schema_version", row.SchemaVersion),
		slog.Any("result_cells", row.cells),
		slog.String("basis",
			"values aligned to column ids by input-version positions; "+
				"columns absent from input version filled from current defaults"))
	return row, nil
}

// DecodeBatch 批量解码。所有事件先统一校验：任一事件版本不存在或值个数不符，
// 整批失败且不返回任何部分结果。校验与结果构造基于同一次钉住的最新结构，
// 因此返回的每一行都基于同一个当前版本。
func (r *Registry) DecodeBatch(events []Event) ([]Row, error) {
	r.mu.RLock()
	latest := r.versions[len(r.versions)-1]
	schemas := make([]Schema, len(events))
	for i, ev := range events {
		s, ok := r.schemaAtLocked(ev.Version)
		if !ok {
			err := newError(KindVersionNotFound, "decode_batch", ev.Version,
				"event at batch index %d references unknown version; latest version is %d",
				i, latest.version)
			r.mu.RUnlock()
			r.logger.Warn("decode batch rejected",
				slog.String("reason", string(err.Kind)),
				slog.Int("batch_index", i),
				slog.Int("input_version", ev.Version),
				slog.Int("event_count", len(events)))
			return nil, err
		}
		if len(ev.Values) != s.width() {
			err := newError(KindValueCountMismatch, "decode_batch", ev.Version,
				"event at batch index %d has %d values but version %d has %d columns",
				i, len(ev.Values), ev.Version, s.width())
			r.mu.RUnlock()
			r.logger.Warn("decode batch rejected",
				slog.String("reason", string(err.Kind)),
				slog.Int("batch_index", i),
				slog.Int("input_version", ev.Version),
				slog.Int("value_count", len(ev.Values)),
				slog.Int("expected_count", s.width()))
			return nil, err
		}
		schemas[i] = s
	}
	r.mu.RUnlock()

	rows := make([]Row, len(events))
	for i, ev := range events {
		rows[i] = projectRow(latest, schemas[i], ev.Values)
	}
	r.logger.Info("decode batch accepted",
		slog.Int("event_count", len(events)),
		slog.Int("result_schema_version", latest.version))
	return rows, nil
}

// checkVersionLimit 在演进追加前检查版本上限。调用方必须持有写锁。
func (r *Registry) checkVersionLimit(op string, currentVersion int) error {
	if len(r.versions) >= r.maxVersions {
		return newError(KindVersionLimitExceeded, op, currentVersion,
			"version limit %d reached; evolution rejected without publishing a new version",
			r.maxVersions)
	}
	return nil
}

// publishLocked 以新列切片发布下一个版本快照并返回它。调用方必须持有写锁。
func (r *Registry) publishLocked(cols []Column) Schema {
	next := newSchema(len(r.versions)+1, cols)
	r.versions = append(r.versions, next)
	return next
}

// schemaAtLocked 是 SchemaAt 的锁内版本。调用方必须持有读锁或写锁。
func (r *Registry) schemaAtLocked(version int) (Schema, bool) {
	if version < 1 || version > len(r.versions) {
		return Schema{}, false
	}
	return r.versions[version-1], true
}

// snapshotsForDecode 在读锁下钉住“当前最新结构”和“事件写入版本结构”两个
// 不可变快照，并完成版本存在性与值个数校验。锁内只做快照与校验，
// 行的构造在锁外基于不可变快照完成。
func (r *Registry) snapshotsForDecode(version, valueCount int) (latest, event Schema, err *Error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	latest = r.versions[len(r.versions)-1]
	s, ok := r.schemaAtLocked(version)
	if !ok {
		return Schema{}, Schema{}, newError(KindVersionNotFound, "decode", version,
			"version %d does not exist; latest version is %d", version, latest.version)
	}
	if valueCount != s.width() {
		return Schema{}, Schema{}, newError(KindValueCountMismatch, "decode", version,
			"event has %d values but version %d has %d columns",
			valueCount, version, s.width())
	}
	return latest, s, nil
}

// projectRow 把事件值投影到当前结构，构造结果行。纯函数、无锁：
// 入参 schema 均为不可变快照，因此可并发调用，且相同输入必得相同输出。
func projectRow(latest, eventSchema Schema, values []string) Row {
	// 事件值按“写入版本的列位置”对齐到稳定标识。
	present := make(map[ColumnID]string, eventSchema.width())
	for pos, c := range eventSchema.columns {
		present[c.ID] = values[pos]
	}

	cells := make([]Cell, 0, latest.width())
	for _, c := range latest.columns {
		if v, ok := present[c.ID]; ok {
			// 来自事件载荷；空串同样走此分支，与缺列严格区分。
			cells = append(cells, Cell{ID: c.ID, Name: c.Name, Value: v, FromDefault: false})
			continue
		}
		// 事件写入版本中没有该标识（在该事件之后才新增），取当前默认值。
		cells = append(cells, Cell{ID: c.ID, Name: c.Name, Value: c.Default, FromDefault: true})
	}
	return newRow(latest.version, cells)
}

// columnByName 按名字返回列（包内辅助）。
func (s Schema) columnByName(name string) (Column, bool) {
	for _, c := range s.columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}
