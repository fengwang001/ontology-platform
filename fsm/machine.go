package fsm

import (
	"fmt"
	"sync"
)

// observerBuf 是每个观察者通道的缓冲大小。
// 满了以后新状态会被丢弃（见 Observe 注释），Fire 永不阻塞。
const observerBuf = 64

// Machine 是一台并发安全的会话协议状态机。
//
// 观察语义：每次成功迁移（含自转移）后，目标状态会以非阻塞方式
// 发送给每个观察者。若观察者的缓冲已满，该次状态直接丢弃，
// 已缓冲和后续发送的内容相对顺序不变。因此慢观察者最终看到的
// 是“完整状态序列中按缓冲容量保留下来的一个无错位子序列”：
// 它一定以订阅时刻（当前状态之后的下一个状态）开头，且与 Log
// 中某次成功迁移一一对应——丢弃只丢整条状态，绝不重排或错位。
type Machine struct {
	mu    sync.Mutex
	state State
	terms map[State]bool
	trans map[struct {
		from  State
		event Event
	}]Transition
	entries map[State][]func() error
	exits   map[State][]func()
	log     []Transition
	subs    []chan State
}

// New 构造一台状态机。initial 为初始状态，terminals 为终态集合，
// table 为转移表。重复的 (From, Event) 或 initial 从未在表中出现
// 都会返回错误。引用了未在任何转移中出现的终态不算错误。
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	trans := make(map[struct {
		from  State
		event Event
	}]Transition, len(table))
	seen := map[State]bool{}

	for _, tr := range table {
		key := struct {
			from  State
			event Event
		}{tr.From, tr.Event}
		if _, dup := trans[key]; dup {
			return nil, fmt.Errorf("fsm: duplicate transition (%v, %v)", tr.From, tr.Event)
		}
		trans[key] = tr
		seen[tr.From] = true
		seen[tr.To] = true
	}

	if !seen[initial] {
		return nil, fmt.Errorf("fsm: initial state %q does not appear in transition table", initial)
	}

	terms := make(map[State]bool, len(terminals))
	for _, t := range terminals {
		terms[t] = true
	}

	return &Machine{
		state:   initial,
		terms:   terms,
		trans:   trans,
		entries: make(map[State][]func() error),
		exits:   make(map[State][]func()),
	}, nil
}

// State 返回机器当前所处状态。
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Log 返回已发生转移的副本，按发生顺序排列。
// 调用方修改返回切片不影响机器内部状态。
func (m *Machine) Log() []Transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transition, len(m.log))
	copy(out, m.log)
	return out
}

// isTerminal 必须在持有 m.mu 时调用。
func (m *Machine) isTerminal() bool { return m.terms[m.state] }
