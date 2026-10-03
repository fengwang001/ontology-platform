// Package history 保存每个工作流的追加式事件日志。
//
// 事件只有两种：S(name) 步骤事件与 M(pid) 补丁标记事件，
// name、pid 均为非空字节串。日志按工作流隔离，整体在内存中
// 持久化，Append 是唯一的写入口，且整批原子生效。
package history

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

// MaxEvents 是单个工作流允许保存的事件总数上限。
const MaxEvents = 100000

// 拒绝原因，可用 errors.Is 区分。
var (
	// ErrInvalid 表示参数非法：空的 wf、name 或 pid。
	ErrInvalid = errors.New("history: invalid argument")
	// ErrConflict 表示 expect 不等于当前日志长度（并发写竞争）。
	ErrConflict = errors.New("history: expect conflicts with current length")
	// ErrCapacity 表示追加后将超过单工作流事件上限。
	ErrCapacity = errors.New("history: per-workflow capacity exceeded")
)

// Kind 区分事件类型。
type Kind byte

const (
	// Step 是步骤事件 S(name)。
	Step Kind = iota
	// Marker 是补丁标记事件 M(pid)。
	Marker
)

// Event 是一条历史事件。Kind 为 Step 时 Data 是步骤名，
// Kind 为 Marker 时 Data 是补丁 pid。
type Event struct {
	Kind Kind
	Data []byte
}

// S 构造步骤事件 S(name)。
func S(name []byte) Event { return Event{Kind: Step, Data: name} }

// M 构造补丁标记事件 M(pid)。
func M(pid []byte) Event { return Event{Kind: Marker, Data: pid} }

// Equal 报告两个事件是否完全相同。
func (e Event) Equal(o Event) bool {
	return e.Kind == o.Kind && bytes.Equal(e.Data, o.Data)
}

func (e Event) String() string {
	switch e.Kind {
	case Step:
		return fmt.Sprintf("S(%s)", e.Data)
	case Marker:
		return fmt.Sprintf("M(%s)", e.Data)
	}
	return fmt.Sprintf("?(kind=%d,%q)", e.Kind, e.Data)
}

// Store 是按工作流隔离的追加式事件日志，并发安全。
type Store struct {
	mu   sync.Mutex
	logs map[string][]Event
}

// NewStore 返回空日志仓库。
func NewStore() *Store {
	return &Store{logs: make(map[string][]Event)}
}

// Append 把 events 整批追加到 wf 的日志末尾。
//
// 拒绝次序：参数非法（ErrInvalid）→ expect 与当前长度不符
// （ErrConflict）→ 超过上限（ErrCapacity）。任一拒绝都整批不写。
// 空批次是成功的无操作（仍校验 wf 与 expect）。
func (s *Store) Append(wf []byte, expect int, events []Event) error {
	if len(wf) == 0 {
		return fmt.Errorf("%w: empty workflow id", ErrInvalid)
	}
	for _, ev := range events {
		if len(ev.Data) == 0 {
			return fmt.Errorf("%w: empty name/pid in %v", ErrInvalid, ev)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.logs[string(wf)]
	if expect != len(cur) {
		return fmt.Errorf("%w: expect=%d current=%d", ErrConflict, expect, len(cur))
	}
	if len(cur)+len(events) > MaxEvents {
		return fmt.Errorf("%w: %d+%d > %d", ErrCapacity, len(cur), len(events), MaxEvents)
	}
	if len(events) == 0 {
		return nil
	}
	next := make([]Event, len(cur), len(cur)+len(events))
	copy(next, cur)
	for _, ev := range events {
		next = append(next, Event{Kind: ev.Kind, Data: bytes.Clone(ev.Data)})
	}
	s.logs[string(wf)] = next
	return nil
}

// Snapshot 返回 wf 当前全部事件的深拷贝；修改拷贝不影响日志。
func (s *Store) Snapshot(wf []byte) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.logs[string(wf)]
	out := make([]Event, len(cur))
	for i, ev := range cur {
		out[i] = Event{Kind: ev.Kind, Data: bytes.Clone(ev.Data)}
	}
	return out
}

// Len 返回 wf 当前的事件条数。
func (s *Store) Len(wf []byte) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logs[string(wf)])
}
