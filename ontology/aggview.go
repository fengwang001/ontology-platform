// Package ontology 提供带过滤条件的分组聚合视图增量维护组件。
//
// 一个组出现在视图中，当且仅当该组的行数与求和都达到阈值（含等于）。
// 每次成功的 Apply 只输出受影响组的净变化：
//   - enter：组新进入视图，仅写入新值；
//   - leave：组离开视图（行数降为零或不再满足阈值），仅撤回旧值；
//   - change：组前后都在视图中，先撤回旧值再写入新值。
//
// 前后都不在视图中的组不产生任何输出。被拒绝的批是原子的：
// 聚合状态、下游视图与已产生的日志都不会改变。
package ontology

import (
	"sort"
	"sync"
)

// Row 表示输入表中的一行。
type Row struct {
	ID    string
	Group string
	Value int
}

// Change 表示一批变更中的单条操作。Deleted 为 true 表示按 Row.ID 删除。
type Change struct {
	Deleted bool
	Row     Row
}

// Aggregate 是某个组当前的聚合结果。
type Aggregate struct {
	Group string
	Count int
	Sum   int
}

// EntryKind 描述一条视图日志条目的种类。
type EntryKind string

const (
	// EntryRetract 撤回该组在视图中的旧值。
	EntryRetract EntryKind = "RETRACT"
	// EntryUpsert 写入该组在视图中的新值。
	EntryUpsert EntryKind = "UPSERT"
)

// Presence 描述一个组在一次变更前后是否处于视图中。
type Presence string

const (
	// PresenceEnter 表示组新进入视图：只写新值。
	PresenceEnter Presence = "enter"
	// PresenceLeave 表示组离开视图：只撤回旧值。
	PresenceLeave Presence = "leave"
	// PresenceChange 表示组前后都在视图中：先撤回旧值再写入新值。
	PresenceChange Presence = "change"
)

// Entry 是输出给下游的一条日志条目。
type Entry struct {
	Kind EntryKind
	Agg  Aggregate
}

// GroupLog 是单个受影响组的判定记录：判定依据与按顺序的输出条目。
type GroupLog struct {
	Group    string
	Before   Aggregate
	After    Aggregate
	WasIn    bool
	IsIn     bool
	Decision Presence
	Entries  []Entry
}

// BatchLog 是一次 Apply 调用的完整日志：输入、输出条目与判定依据。
type BatchLog struct {
	Changes []Change
	Groups  []GroupLog
	Entries []Entry
}

// Logger 接收每次成功 Apply 的判定日志。
type Logger interface {
	LogBatch(BatchLog)
}

type groupState struct {
	count int
	sum   int
}

// Config 描述维护器的过滤条件与组数上限。
type Config struct {
	// MinCount 是组进入视图所需的最小行数（含等于），必须 >= 1。
	MinCount int
	// MinSum 是组进入视图所需的最小求和（含等于）。
	MinSum int
	// MaxGroups 是非空组数量上限（含等于），0 表示不限制。
	MaxGroups int
}

// Maintainer 增量维护满足过滤条件的分组聚合视图。
// 零值不可用，必须通过 NewMaintainer 创建。
type Maintainer struct {
	mu        sync.RWMutex
	minCount  int
	minSum    int
	maxGroups int
	rows      map[string]Row
	groups    map[string]*groupState
	logger    Logger
}

// NewMaintainer 创建一个空的维护器。
func NewMaintainer(cfg Config, logger Logger) (*Maintainer, error) {
	if cfg.MinCount < 1 {
		return nil, &configError{msg: "ontology: MinCount must be >= 1"}
	}
	if cfg.MaxGroups < 0 {
		return nil, &configError{msg: "ontology: MaxGroups must be >= 0"}
	}
	return &Maintainer{
		minCount:  cfg.MinCount,
		minSum:    cfg.MinSum,
		maxGroups: cfg.MaxGroups,
		rows:      make(map[string]Row),
		groups:    make(map[string]*groupState),
		logger:    logger,
	}, nil
}

// Apply 原子地应用一批变更，返回该批产生的净变化日志条目（按组名排序、
// 每组内先撤回后写入）。任何非法输入都会以 *RejectError 拒绝整批，
// 且不改变聚合、下游视图或已产生的日志。
func (m *Maintainer) Apply(changes []Change) ([]Entry, *BatchLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	type stagedRow struct {
		row     Row
		deleted bool
	}
	pending := make(map[string]stagedRow, len(changes))
	affected := make(map[string]struct{}, len(changes))

	// 第一阶段：校验并在暂存区模拟整批。任何错误都直接返回，
	// 此时尚未触碰 rows / groups，因此拒绝天然是原子的。
	for i, change := range changes {
		row := change.Row
		if row.ID == "" {
			return nil, nil, &RejectError{Err: ErrEmptyRowID, Index: i, RowID: row.ID, Group: row.Group}
		}
		if !change.Deleted && row.Group == "" {
			return nil, nil, &RejectError{Err: ErrEmptyGroup, Index: i, RowID: row.ID, Group: row.Group}
		}

		current, exists := m.rows[row.ID]
		if staged, ok := pending[row.ID]; ok {
			current, exists = staged.row, !staged.deleted
		}

		if change.Deleted {
			if !exists {
				return nil, nil, &RejectError{Err: ErrDeleteMissing, Index: i, RowID: row.ID, Group: row.Group}
			}
			affected[current.Group] = struct{}{}
			pending[row.ID] = stagedRow{row: Row{ID: row.ID}, deleted: true}
			continue
		}

		if exists {
			return nil, nil, &RejectError{Err: ErrDuplicateInsert, Index: i, RowID: row.ID, Group: row.Group}
		}
		affected[row.Group] = struct{}{}
		pending[row.ID] = stagedRow{row: row}
	}

	// 第二阶段：计算每个受影响组提交前后的聚合。
	groupNames := make([]string, 0, len(affected))
	for name := range affected {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	type projected struct {
		before Aggregate
		after  Aggregate
		wasIn  bool
		isIn   bool
	}
	results := make(map[string]projected, len(groupNames))

	for _, name := range groupNames {
		before := Aggregate{Group: name}
		if old := m.groups[name]; old != nil {
			before.Count = old.count
			before.Sum = old.sum
		}

		after := before
		for id, pr := range pending {
			if current, ok := m.rows[id]; ok && current.Group == name {
				after.Count--
				after.Sum -= current.Value
			}
			if !pr.deleted && pr.row.Group == name {
				after.Count++
				after.Sum += pr.row.Value
			}
		}

		results[name] = projected{
			before: before,
			after:  after,
			wasIn:  m.qualifies(before),
			isIn:   m.qualifies(after),
		}
	}

	// 第三阶段：校验组数上限。提交后仍然存在（非空）的组数不得超限。
	if m.maxGroups > 0 {
		total := 0
		for name := range affected {
			if results[name].after.Count > 0 {
				total++
			}
		}
		for name, gs := range m.groups {
			if _, touched := affected[name]; !touched && gs.count > 0 {
				total++
			}
		}
		if total > m.maxGroups {
			return nil, nil, &RejectError{
				Err:     ErrTooManyGroups,
				Limit:   m.maxGroups,
				Current: total,
			}
		}
	}

	// 第四阶段：提交暂存变更到真实状态。
	for id, pr := range pending {
		if current, ok := m.rows[id]; ok {
			gs := m.groups[current.Group]
			gs.count--
			gs.sum -= current.Value
			if gs.count == 0 {
				delete(m.groups, current.Group)
			}
			delete(m.rows, id)
		}
		if !pr.deleted {
			row := pr.row
			gs := m.groups[row.Group]
			if gs == nil {
				gs = &groupState{}
				m.groups[row.Group] = gs
			}
			gs.count++
			gs.sum += row.Value
			m.rows[id] = row
		}
	}

	// 第五阶段：按判定结果生成确定性的净变化日志。
	batchLog := &BatchLog{Changes: append([]Change(nil), changes...)}
	var entries []Entry
	for _, name := range groupNames {
		r := results[name]
		glog := GroupLog{
			Group:  name,
			Before: r.before,
			After:  r.after,
			WasIn:  r.wasIn,
			IsIn:   r.isIn,
		}
		switch {
		case !r.wasIn && r.isIn:
			glog.Decision = PresenceEnter
			glog.Entries = []Entry{{Kind: EntryUpsert, Agg: r.after}}
		case r.wasIn && !r.isIn:
			glog.Decision = PresenceLeave
			glog.Entries = []Entry{{Kind: EntryRetract, Agg: r.before}}
		case r.wasIn && r.isIn:
			glog.Decision = PresenceChange
			glog.Entries = []Entry{
				{Kind: EntryRetract, Agg: r.before},
				{Kind: EntryUpsert, Agg: r.after},
			}
		default:
			// 前后都不在视图中：不输出。
		}
		if glog.Decision != "" {
			entries = append(entries, glog.Entries...)
		}
		batchLog.Groups = append(batchLog.Groups, glog)
	}
	batchLog.Entries = entries

	if m.logger != nil {
		m.logger.LogBatch(*batchLog)
	}
	return entries, batchLog, nil
}

// Snapshot 返回当前视图中所有满足过滤条件的组聚合副本，按组名排序。
// 并发读取期间每个被读到的组聚合都满足过滤条件。
func (m *Maintainer) Snapshot() []Aggregate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Aggregate, 0, len(m.groups))
	for name, gs := range m.groups {
		agg := Aggregate{Group: name, Count: gs.count, Sum: gs.sum}
		if m.qualifies(agg) {
			out = append(out, agg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// qualifies 判定一个聚合是否满足过滤条件（行数与求和均达到阈值，含等于）。
func (m *Maintainer) qualifies(a Aggregate) bool {
	return a.Count >= m.minCount && a.Sum >= m.minSum
}

// SetLogger 设置判定日志输出目标，传 nil 表示关闭日志。
func (m *Maintainer) SetLogger(logger Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}
