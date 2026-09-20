package fsm

import (
	"errors"
	"fmt"
	"sync"
)

// observerBuffer 是每个观察者通道的缓冲容量。
// 缓冲满时新状态会被丢弃，详见 Observe 的注释。
const observerBuffer = 16

// Machine 是一台会话协议状态机。
// 零值不可用，请通过 New 构造。
type Machine struct {
	mu        sync.Mutex
	state     State
	terminals map[State]struct{}
	table     map[transitionKey]State
	entry     map[State][]func() error
	exit      map[State][]func()
	log       []Transition
	observers []chan State
}

// New 构造一台状态机。
//
// initial 为初始状态，terminals 为终态集合，table 为转移表。
// 构造期校验：
//   - 转移表中同一个 (From, Event) 出现两次，返回错误；
//   - initial 必须出现在表里的某个 From 或 To 中，否则返回错误；
//   - 引用了未在任何转移中出现的终态不算错误。
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	m := &Machine{
		state:     initial,
		terminals: make(map[State]struct{}, len(terminals)),
		table:     make(map[transitionKey]State, len(table)),
		entry:     make(map[State][]func() error),
		exit:      make(map[State][]func()),
	}
	for _, s := range terminals {
		m.terminals[s] = struct{}{}
	}
	referenced := false
	for _, t := range table {
		k := transitionKey{from: t.From, event: t.Event}
		if _, dup := m.table[k]; dup {
			return nil, fmt.Errorf("fsm: duplicate transition for (%q, %q)", t.From, t.Event)
		}
		m.table[k] = t.To
		if t.From == initial || t.To == initial {
			referenced = true
		}
	}
	if !referenced {
		return nil, errors.New("fsm: initial state is not referenced by any transition")
	}
	return m, nil
}

// OnEntry 注册进入状态 s 时执行的动作。
// 同一状态上多次注册按注册顺序全部执行；任一动作返回 error 即视为 entry 失败。
// 自转移与终态吸收不触发 exit，但进入终态时 entry 照常执行。
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entry[s] = append(m.entry[s], f)
}

// OnExit 注册离开状态 s 时执行的动作。
// 同一状态上多次注册按注册顺序全部执行。
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exit[s] = append(m.exit[s], f)
}

// State 返回机器当前所处的状态。
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// isTerminal 报告 s 是否为终态。调用方须持有 m.mu。
func (m *Machine) isTerminal(s State) bool {
	_, ok := m.terminals[s]
	return ok
}
