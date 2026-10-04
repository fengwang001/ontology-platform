// Package task 管理补货任务记录与全局序号，不包含补货业务规则。
package task

import "errors"

var (
	// ErrState 任务已完成或已取消，不能重复 Confirm/Cancel。
	ErrState = errors.New("task: task not open")
	// ErrOver actual 超过任务量。
	ErrOver = errors.New("task: actual exceeds task qty")
	// ErrNotFound 任务号不存在。
	ErrNotFound = errors.New("task: not found")
)

// Kind 任务种类。
type Kind int

const (
	Normal Kind = iota
	Urgent
)

// Status 任务状态。
type Status int

const (
	Open Status = iota
	Done
	Cancelled
)

// Task 为一条补货任务。
type Task struct {
	ID     int64
	Loc    string
	SKU    string
	Qty    int64
	Kind   Kind
	Status Status
}

// Registry 为任务登记表，序号只增、被拒绝操作不占号。
type Registry struct {
	tasks   map[int64]*Task
	openQty map[string]int64 // 每 SKU 未完成任务量之和
	seq     int64
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	return &Registry{
		tasks:   make(map[int64]*Task),
		openQty: make(map[string]int64),
	}
}

// Create 分配新任务号并登记（状态恒为未完成）。
func (r *Registry) Create(loc, sku string, qty int64, k Kind) *Task {
	r.seq++
	t := &Task{ID: r.seq, Loc: loc, SKU: sku, Qty: qty, Kind: k, Status: Open}
	r.tasks[t.ID] = t
	r.openQty[sku] += qty
	return t
}

// Get 按任务号查询。
func (r *Registry) Get(id int64) (*Task, bool) {
	t, ok := r.tasks[id]
	return t, ok
}

// SetKind 修改任务种类（Demand 升级用）。
func (r *Registry) SetKind(t *Task, k Kind) { t.Kind = k }

// Complete 将任务置为完成。
func (r *Registry) Complete(t *Task) {
	t.Status = Done
	r.openQty[t.SKU] -= t.Qty
}

// Cancel 将任务置为取消。
func (r *Registry) Cancel(t *Task) {
	t.Status = Cancelled
	r.openQty[t.SKU] -= t.Qty
}

// OpenQty 返回某 SKU 全部未完成任务量之和。
func (r *Registry) OpenQty(sku string) int64 { return r.openQty[sku] }

// Snapshot 返回全部任务（拷贝切片，顺序由调用方排）。
func (r *Registry) Snapshot() []*Task {
	out := make([]*Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		out = append(out, t)
	}
	return out
}

// Seq 返回已分配的最大任务号。
func (r *Registry) Seq() int64 { return r.seq }
