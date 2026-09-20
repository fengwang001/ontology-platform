package fsm

import "fmt"

// New 创建一台状态机：initial 为初始状态，terminals 为终态集合，
// table 为转移表。返回错误的情形：
//   - table 中同一个 (From, Event) 出现多次；
//   - initial 从未在任何转移的 From 或 To 中出现。
//
// 终态只需要在 terminals 中给出即可；它可以不出现在任何转移里，
// 这种终态不可达，但不构成错误。
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	transitions := make(map[stateKey]State, len(table))
	appears := false
	for _, t := range table {
		key := stateKey{from: t.From, event: t.Event}
		if _, dup := transitions[key]; dup {
			return nil, fmt.Errorf(
				"fsm: duplicate transition for state %q event %q", t.From, t.Event)
		}
		transitions[key] = t.To
		if t.From == initial || t.To == initial {
			appears = true
		}
	}
	if !appears {
		return nil, fmt.Errorf(
			"fsm: initial state %q does not appear in the transition table", initial)
	}

	terminalSet := make(map[State]struct{}, len(terminals))
	for _, s := range terminals {
		terminalSet[s] = struct{}{}
	}

	return &Machine{
		current:   initial,
		terminals: terminalSet,
		table:     transitions,
		entries:   make(map[State][]entryFunc),
		exits:     make(map[State][]exitFunc),
		observers: make(map[chan State]struct{}),
	}, nil
}
