// Package txn 定义单条 CDC 事件的类型、合法性判定与单个事务的生命周期。
// 不依赖其他任何包。
package txn

import "errors"

// Kind 是事件类型。
type Kind int

const (
	Begin Kind = iota
	Row
	Commit
	Rollback
)

// 四类可判定的哨兵错误，互不相同。
var (
	ErrInvalidEvent   = errors.New("invalid event")
	ErrDuplicateBegin = errors.New("duplicate begin")
	ErrUnknownTx      = errors.New("unknown transaction")
	ErrBufferFull     = errors.New("buffer full")
)

// Event 是一条 CDC 事件。
type Event struct {
	Kind Kind
	Tx   int64
	Data string
}

// Validate 校验事件本身是否合法：Kind 必须是四者之一且 Tx > 0。
func (e Event) Validate() error {
	if e.Kind < Begin || e.Kind > Rollback || e.Tx <= 0 {
		return ErrInvalidEvent
	}
	return nil
}

// Txn 是输出单位：一个已提交事务及其全部行（按到达顺序）。
type Txn struct {
	Tx   int64
	Rows []string
}

// Status 是事务生命周期状态。
type Status int

const (
	Active Status = iota
	Committed
	RolledBack
)

// T 是单个事务的状态与行缓冲。
type T struct {
	status Status
	rows   []string
}

// New 创建一个进行中事务。
func New() *T { return &T{status: Active} }

// Status 返回当前状态。
func (t *T) Status() Status { return t.status }

// Len 返回当前缓冲行数。
func (t *T) Len() int { return len(t.rows) }

// AddRow 追加一行；调用方须已确认事务进行中且总账未满。
func (t *T) AddRow(d string) { t.rows = append(t.rows, d) }

// Commit 结束事务并返回全部缓冲行；缓冲随即释放。
func (t *T) Commit() []string {
	rows := t.rows
	t.rows = nil
	t.status = Committed
	return rows
}

// Rollback 丢弃全部缓冲行并结束事务，返回被丢弃的行数。
func (t *T) Rollback() int {
	n := len(t.rows)
	t.rows = nil
	t.status = RolledBack
	return n
}
