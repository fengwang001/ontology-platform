// Package sam implements a suffix automaton (SAM) over a byte string.
package sam

import "errors"

// MaxLen is the maximum accepted input length.
const MaxLen = 1_000_000

var (
	// ErrEmpty is returned when the input string is empty.
	ErrEmpty = errors.New("sam: empty input string")
	// ErrTooLong is returned when the input string exceeds MaxLen.
	ErrTooLong = errors.New("sam: input string exceeds MaxLen")
)

type state struct {
	next     map[byte]int
	link     int
	length   int
	terminal bool
}

// Automaton is a suffix automaton. It is immutable after New returns,
// so all read methods are safe for concurrent use.
type Automaton struct {
	st      []state
	last    int
	n       int
	nstates int // total states after build; internal only, never exported
}

// New builds the SAM of s. Empty or over-long input is rejected
// before any state is created.
func New(s string) (*Automaton, error) {
	if len(s) == 0 {
		return nil, ErrEmpty
	}
	if len(s) > MaxLen {
		return nil, ErrTooLong
	}
	a := &Automaton{n: len(s)}
	a.st = append(a.st, state{next: map[byte]int{}, link: -1})
	for i := 0; i < len(s); i++ {
		a.extend(s[i])
	}
	a.nstates = len(a.st)
	return a, nil
}

// extend appends one byte to the automaton (standard SAM construction,
// with clone splitting when a transition would break the len invariant).
func (a *Automaton) extend(c byte) {
	cur := len(a.st)
	a.st = append(a.st, state{next: map[byte]int{}, length: a.st[a.last].length + 1, terminal: true})
	p := a.last
	for p != -1 {
		if _, ok := a.st[p].next[c]; ok {
			break
		}
		a.st[p].next[c] = cur
		p = a.st[p].link
	}
	if p == -1 {
		a.st[cur].link = 0
		a.last = cur
		return
	}
	q := a.st[p].next[c]
	if a.st[p].length+1 == a.st[q].length {
		a.st[cur].link = q
		a.last = cur
		return
	}
	clone := len(a.st)
	next := make(map[byte]int, len(a.st[q].next))
	for b, u := range a.st[q].next {
		next[b] = u
	}
	a.st = append(a.st, state{next: next, link: a.st[q].link, length: a.st[p].length + 1})
	for p != -1 {
		u, ok := a.st[p].next[c]
		if !ok || u != q {
			break
		}
		a.st[p].next[c] = clone
		p = a.st[p].link
	}
	a.st[q].link = clone
	a.st[cur].link = clone
	a.last = cur
}

// Root returns the root state id.
func (a *Automaton) Root() int { return 0 }

// Len returns the longest substring length represented by state v.
func (a *Automaton) Len(v int) int { return a.st[v].length }

// Link returns the suffix link of v (-1 for the root).
func (a *Automaton) Link(v int) int { return a.st[v].link }

// Terminal reports whether v ends some prefix of the input string.
func (a *Automaton) Terminal(v int) bool { return a.st[v].terminal }

// Next returns the transition target of state v on byte c.
func (a *Automaton) Next(v int, c byte) (int, bool) {
	u, ok := a.st[v].next[c]
	return u, ok
}

// ForEachState calls fn once per state, in id order.
func (a *Automaton) ForEachState(fn func(v int)) {
	for v := range a.st {
		fn(v)
	}
}

// WithinStateBound reports whether the state count stays linear:
// n+1 <= states <= 2n. It never exposes the counter itself.
func (a *Automaton) WithinStateBound() bool {
	return a.nstates >= a.n+1 && a.nstates <= 2*a.n
}
