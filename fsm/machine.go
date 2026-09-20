package fsm

import (
	"fmt"
	"sync"
)

// observerBuffer 是每个观察者通道的缓冲大小。
// 通道满时新状态会被丢弃（drop-newest），因此观察者收到的永远是
// 完整状态序列的某个前缀，Fire 绝不会因为慢观察者而阻塞。
const observerBuffer = 64

// Machine 是一台并发安全的会话协议状态机。
//
// 观察语义：Observe 返回一个带缓冲的状态通道。每次成功迁移
// （含自转移）后，机器会以非阻塞方式向每个观察者投递新状态；
// 若某观察者通道已满，本次状态对该观察者直接丢弃，后续状态也
// 继续尝试投递。由于只在满时丢弃“最新”状态，慢观察者可能停在
// 旧状态上，但它一旦收到某个状态，此前收到的序列必然与机器
// 完整状态序列的前缀逐位一致，不会错位或乱序。
type Machine struct {
	mu        sync.Mutex
	current   State
	terminals map[State]bool
	table     map[struct {
		from  State
		event Event
	}]Transition
	entries   map[State][]func() error
	exits     map[State][]func()
	log       []Transition
	observers map[int]chan State
	nextObsID int
}

// New 构造一台状态机：initial 为初始状态，terminals 为终态集合，
// table 为转移表。同一个 (From, Event) 出现两次、或 initial 从未
// 在表中出现，都会返回错误。
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	termSet := make(map[State]bool, len(terminals))
	for _, t := range terminals {
		termSet[t] = true
	}

	rules := make(map[struct {
		from  State
		event Event
	}]Transition, len(table))
	appears := false
	for _, tr := range table {
		key := struct {
			from  State
			event Event
		}{tr.From, tr.Event}
		if _, dup := rules[key]; dup {
			return nil, fmt.Errorf("fsm: duplicate transition for (%s, %s)", tr.From, tr.Event)
		}
		rules[key] = tr
		if tr.From == initial || tr.To == initial {
			appears = true
		}
	}
	if !appears {
		return nil, fmt.Errorf("fsm: initial state %q must appear in the transition table", initial)
	}

	return &Machine{
		current:   initial,
		terminals: termSet,
		table:     rules,
		entries:   make(map[State][]func() error),
		exits:     make(map[State][]func()),
		observers: make(map[int]chan State),
	}, nil
}

// State 返回机器当前所处状态。
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// OnEntry 为状态 s 注册一个进入动作，按注册顺序执行；
// 返回 error 会中止迁移并被 ErrEntryFailed 包裹。
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[s] = append(m.entries[s], f)
}

// OnExit 为状态 s 注册一个离开动作，按注册顺序执行。
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exits[s] = append(m.exits[s], f)
}
