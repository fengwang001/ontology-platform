// Package group 维护每个并发组内「一个占位者 + 一个 Pending」的结构。
package group

// State 是运行的生命周期状态。
type State int

const (
	Waiting State = iota
	Running
	Cancelling
	Pending
	Succeeded
	Failed
	Cancelled
	Superseded
)

// Run 是一条运行记录。
type Run struct {
	ID        int
	Group     []byte
	State     State
	protected bool
	queued    bool // 占位者是否位于全局等待队列中
}

func (s State) String() string {
	switch s {
	case Waiting:
		return "Waiting"
	case Running:
		return "Running"
	case Cancelling:
		return "Cancelling"
	case Pending:
		return "Pending"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Cancelled"
	case Superseded:
		return "Superseded"
	default:
		return "Unknown"
	}
}

// IsTerminal 报告状态是否为终态。
func (s State) IsTerminal() bool {
	switch s {
	case Succeeded, Failed, Cancelled, Superseded:
		return true
	}
	return false
}

// Protected 返回该运行是否受 Submit 顶替保护（仅对已开始的运行有意义）。
func (r *Run) Protected() bool { return r.protected }

// Queued 报告占位者当前是否位于等待队列中。
func (r *Run) Queued() bool { return r.queued }

// SetQueued 设置队列标记。
func (r *Run) SetQueued(q bool) { r.queued = q }

// Table 保存所有组的占位者与 Pending（空组名也在此表）。
type Table struct {
	groups map[string]*entry
}

type entry struct {
	holder  *Run // 占位者：Waiting / Running / Cancelling
	pending *Run // 组内待定
}

// NewTable 创建空表。
func NewTable() *Table { return &Table{groups: make(map[string]*entry)} }

// Entry 返回某个组当前的占位者与 Pending，不存在返回 nil。
func (t *Table) Entry(g []byte) (holder, pending *Run) {
	e := t.groups[string(g)]
	if e == nil {
		return nil, nil
	}
	return e.holder, e.pending
}

// SetHolder 设置组的占位者；占位者与 Pending 皆空时删除该组。
func (t *Table) SetHolder(g []byte, holder *Run) {
	key := string(g)
	e := t.groups[key]
	if e == nil {
		if holder == nil {
			return
		}
		t.groups[key] = &entry{holder: holder}
		return
	}
	e.holder = holder
	if e.holder == nil && e.pending == nil {
		delete(t.groups, key)
	}
}

// SetPending 设置组的 Pending；占位者与 Pending 皆空时删除该组。
func (t *Table) SetPending(g []byte, pending *Run) {
	key := string(g)
	e := t.groups[key]
	if e == nil {
		if pending == nil {
			return
		}
		t.groups[key] = &entry{pending: pending}
		return
	}
	e.pending = pending
	if e.holder == nil && e.pending == nil {
		delete(t.groups, key)
	}
}

// Groups 返回当前非空组数（含空组名）。
func (t *Table) Groups() int { return len(t.groups) }

// NewRun 构造一条运行记录（未导出字段仅由同模块装配）。
func NewRun(id int, g []byte, protected bool) *Run {
	return &Run{ID: id, Group: g, protected: protected}
}
