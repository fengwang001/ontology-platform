// Package materializer 提供全或无（all-or-nothing）的原子批次键值物化器。
//
// 一个批次由多条按序排列的事件组成；每条事件是对某个键的写入或删除，
// 并携带一个前置期望（为空表示要求键当前不存在，非空表示要求当前值相等）。
// 批次先在真实视图叠加批内前序事件的临时视图上逐条预演，全部通过后才
// 一次性替换可见视图，因此下游读到的永远是某个完整批次边界上的状态。
package materializer

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// Op 表示一条事件的操作类型。
type Op int

const (
	// Put 写入（或覆盖）一个键。
	Put Op = iota + 1
	// Delete 删除一个键。
	Delete
)

// Event 是批次中的一条事件：对 Key 执行 Op，并要求前置期望成立。
// Expect 为 nil 表示要求该键当前不存在；非 nil 表示要求当前值与其相等。
type Event struct {
	Key    string
	Op     Op
	Value  string
	Expect *string
}

// 三类互不相同、可用 errors.Is 判定的错误。
var (
	// ErrEmptyBatch 表示提交了空批次。
	ErrEmptyBatch = errors.New("materializer: empty batch is rejected")
	// ErrEmptyKey 表示批次中存在空键事件。
	ErrEmptyKey = errors.New("materializer: empty key is rejected")
	// ErrExpectationFailed 表示某条事件的前置期望未满足。
	ErrExpectationFailed = errors.New("materializer: precondition expectation failed")
)

// FailureError 报告导致批次被拒绝的第一条失败事件（从 0 开始的批内下标）。
type FailureError struct {
	// Index 是第一条失败事件在批次中的下标。
	Index int
	// Event 是第一条失败事件的内容。
	Event Event
	// Err 是三类可判定哨兵错误之一：ErrEmptyBatch、ErrEmptyKey、ErrExpectationFailed。
	Err error
	// have 是失败时该键的实际值；present 表示键是否存在，用于说明判定依据。
	have    string
	present bool
}

func (e *FailureError) Error() string {
	switch {
	case errors.Is(e.Err, ErrEmptyBatch):
		return "materializer: batch rejected: empty batch"
	case errors.Is(e.Err, ErrEmptyKey):
		return fmt.Sprintf("materializer: batch rejected at event %d: empty key", e.Index)
	case errors.Is(e.Err, ErrExpectationFailed):
		if e.Event.Expect == nil {
			actual := "<absent>"
			if e.present {
				actual = fmt.Sprintf("%q", e.have)
			}
			return fmt.Sprintf("materializer: batch rejected at event %d key %q: "+
				"expected key absent, actual %s", e.Index, e.Event.Key, actual)
		}
		actual := "<absent>"
		if e.present {
			actual = fmt.Sprintf("%q", e.have)
		}
		return fmt.Sprintf("materializer: batch rejected at event %d key %q: "+
			"expected %s, actual %s", e.Index, e.Event.Key, *e.Event.Expect, actual)
	default:
		return "materializer: batch rejected"
	}
}

func (e *FailureError) Unwrap() error { return e.Err }

// Materializer 是并发安全的原子批次物化器。
type Materializer struct {
	// applyMu 串行化批次应用；读取路径不持锁，只通过 view 做原子快照。
	applyMu sync.Mutex
	view    atomic.Pointer[map[string]string]
	logger  *log.Logger
}

// New 创建一个空的物化器。
func New(logger *log.Logger) *Materializer {
	m := &Materializer{logger: logger}
	initial := map[string]string{}
	m.view.Store(&initial)
	return m
}

// Apply 按批内顺序预演 batch；全部通过则原子生效，否则拒绝且不留痕迹。
//
// 预演在“真实视图叠加本批前序事件”的临时视图上进行：同键先前的 Put
// 会覆盖真实值，先前的 Delete 会使键消失。遇到第一条非法输入或第一条
// 前置期望不满足的事件即停止，整批拒绝；只有全部事件通过，才一次性把
// 新视图替换为可见视图。返回的错误恒为 *FailureError，可用 errors.Is
// 判定其哨兵错误类别。
func (m *Materializer) Apply(batch []Event) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	m.logf("apply begin: batch size=%d events=%s", len(batch), formatBatch(batch))

	if len(batch) == 0 {
		err := &FailureError{Index: -1, Err: ErrEmptyBatch}
		m.logf("apply reject: %s | state unchanged", err)
		return err
	}

	base := *m.view.Load()
	// staged 是真实视图的可变副本，叠加批内前序事件的效果；
	// 预演成功之前它对任何读取者都不可见。
	staged := make(map[string]string, len(base)+len(batch))
	for k, v := range base {
		staged[k] = v
	}

	for i, event := range batch {
		if event.Key == "" {
			err := &FailureError{Index: i, Event: event, Err: ErrEmptyKey}
			m.logf("apply reject: %s | state unchanged", err)
			return err
		}
		have, present := staged[event.Key]
		if event.Expect == nil {
			if present {
				err := &FailureError{Index: i, Event: event, Err: ErrExpectationFailed,
					have: have, present: true}
				m.logf("apply reject: %s | state unchanged", err)
				return err
			}
		} else if !present || have != *event.Expect {
			err := &FailureError{Index: i, Event: event, Err: ErrExpectationFailed,
				have: have, present: present}
			m.logf("apply reject: %s | state unchanged", err)
			return err
		}

		switch event.Op {
		case Put:
			staged[event.Key] = event.Value
		case Delete:
			delete(staged, event.Key)
		default:
			panic(fmt.Sprintf("materializer: event %d has unknown op %d", i, event.Op))
		}
		m.logf("event %d ok: key=%q op=%s expect=%s -> staged value=%q",
			i, event.Key, opName(event.Op), formatExpect(event.Expect), stagedValue(staged, event.Key))
	}

	// 全量预演通过：一次性原子切换可见视图。切换前后读取者都只能看到
	// 完整的旧批次边界或完整的新批次边界，不存在半批次中间态。
	committed := staged
	m.view.Store(&committed)
	m.logf("apply commit: %d events applied atomically, view=%s", len(batch), formatMap(committed))
	return nil
}

// Get 读取某个键的当前值；ok 为 false 表示键不存在。
func (m *Materializer) Get(key string) (value string, ok bool) {
	v, ok := (*m.view.Load())[key]
	return v, ok
}

// Snapshot 返回当前完整视图的深拷贝。
func (m *Materializer) Snapshot() map[string]string {
	src := *m.view.Load()
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (m *Materializer) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}
