// Package median 维护一个支持加入与撤回的整数多重集，
// 并以两个对顶堆增量维护当前中位数（中间偏下）。
package median

import "errors"

// 互不相同、可区分的错误类别。
var (
	// ErrEmpty 对空多重集查询中位数。
	ErrEmpty = errors.New("median: multiset is empty")
	// ErrInvalidArgument 参数非法（如非整数、nil 批次等）。
	ErrInvalidArgument = errors.New("median: invalid argument")
	// ErrNotFound 撤回一个当前不存在的值（有效计数为零）。
	ErrNotFound = errors.New("median: value not present")
	// ErrLimitExceeded 有效元素个数超过上限。
	ErrLimitExceeded = errors.New("median: size limit exceeded")
)

// Tracker 是支持加入/撤回的增量中位数维护器。
// 一个 Tracker 不应被拷贝；其方法可被多个 goroutine 并发调用。
type Tracker struct {
	// 由实现填充。
}

// New 创建一个有效元素个数上限为 limit 的维护器；limit 必须为正。
func New(limit int) (*Tracker, error) {
	return nil, ErrInvalidArgument
}

// Add 加入一个整数。
func (t *Tracker) Add(value int) error {
	return ErrInvalidArgument
}

// Remove 撤回一个当前存在的整数；只撤回一个副本。
func (t *Tracker) Remove(value int) error {
	return ErrNotFound
}

// AddBatch 整批加入；任一项非法则整批拒绝，状态不变。
func (t *Tracker) AddBatch(values []int) error {
	return ErrInvalidArgument
}

// RemoveBatch 整批撤回；任一项不存在或非法则整批拒绝，状态不变。
func (t *Tracker) RemoveBatch(values []int) error {
	return ErrInvalidArgument
}

// Median 返回当前中位数（多重集升序后中间偏下位置）。
func (t *Tracker) Median() (int, error) {
	return 0, ErrEmpty
}

// Len 返回当前有效元素个数。
func (t *Tracker) Len() int {
	return 0
}

// Check 自检内部不变量并与朴素中位数比对。
func (t *Tracker) Check() error {
	return nil
}
