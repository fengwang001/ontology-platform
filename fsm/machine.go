package fsm

import (
	"fmt"
	"sync"
)

// observerBuffer 是每个观察者通道的缓冲容量。
// 缓冲满时新的状态通知会被丢弃，Fire 永远不会因观察者而阻塞。
const observerBuffer = 16

// Machine 是一台会话协议状态机，可安全地被多个 goroutine 并发使用。
type Machine struct {
	mu        sync.Mutex
	state     State
	terminals map[State]bool
	table     map[State]map[Event]State
	entries   map[State][]func() error
	exits     map[State][]func()
	log       []Transition
	observers []chan State
}

// New 构造一台状态机：initial 为初始状态，terminals 为终态集合，
// table 为转移表。
//
// 构造期校验：
//   - 转移表中同一个 (From, Event) 出现两次时返回错误；
//   - initial 必须出现在表中某个转移的 From 或 To 中，否则返回错误；
//   - 终态不被任何转移引用是合法的，不算错误。
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	tbl := make(map[State]map[Event]State, len(table))
	referenced := make(map[State]bool, 2*len(table))
	for _, tr := range table {
		row, ok := tbl[tr.From]
		if !ok {
			row = make(map[Event]State)
			tbl[tr.From] = row
		}
		if _, dup := row[tr.Event]; dup {
			return nil, fmt.Errorf("fsm: duplicate transition for (%q, %q)", tr.From, tr.Event)
		}
		row[tr.Event] = tr.To
		referenced[tr.From] = true
		referenced[tr.To] = true
	}
	if !referenced[initial] {
		return nil, fmt.Errorf("fsm: initial state %q not referenced by any transition", initial)
	}
	terms := make(map[State]bool, len(terminals))
	for _, s := range terminals {
		terms[s] = true
	}
	return &Machine{
		state:     initial,
		terminals: terms,
		table:     tbl,
		entries:   make(map[State][]func() error),
		exits:     make(map[State][]func()),
	}, nil
}

// OnEntry 注册进入状态 s 时执行的动作，可返回 error。
// 同一状态多次注册时按注册顺序全部执行。
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[s] = append(m.entries[s], f)
}

// OnExit 注册离开状态 s 时执行的动作。
// 同一状态多次注册时按注册顺序全部执行。
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exits[s] = append(m.exits[s], f)
}

// State 返回机器当前所处的状态。
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Log 按发生顺序返回已成功完成的转移。
// 返回的是内部日志的副本，调用方修改不影响机器内部状态。
func (m *Machine) Log() []Transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transition, len(m.log))
	copy(out, m.log)
	return out
}

// Observe 订阅状态变更，可多次调用，每次返回一个独立的通道。
//
// 每次成功迁移（包括自转移）都会向所有观察者通道发送迁移后的
// 新状态。通道带缓冲，Fire 永远不会被慢的观察者阻塞：缓冲满时
// 本次通知被丢弃。因此每个观察者收到的序列是“全部成功迁移的
// To 状态序列”的一个保序子序列（可能因丢弃而不完整），序列中
// 元素的相对顺序与 Log() 中转移的顺序严格一致。
func (m *Machine) Observe() <-chan State {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan State, observerBuffer)
	m.observers = append(m.observers, ch)
	return ch
}
