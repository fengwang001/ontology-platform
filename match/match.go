// Package match implements a hash-chain longest-match finder over a window.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest match the compressor emits.
const MinMatch = 3

// ErrConfig is returned for invalid constructor parameters.
var ErrConfig = errors.New("match: invalid configuration")

// Matcher keeps hash chains of committed history positions. Absolute positions
// count every byte ever pushed (0-based); the window retains the last Cap.
type Matcher struct {
	win      *window.Window
	prev     []int  // ring slot -> absolute predecessor position (-1 first)
	heads    map[uint32]int
	maxChain int
	pushed   int64
	cands    int64 // total examined candidate positions (unexported counter)
}

// New builds a Matcher with its own fresh window.
func New(capacity, maxChain int) (*Matcher, error) {
	if capacity <= 0 || maxChain <= 0 {
		return nil, ErrConfig
	}
	return &Matcher{win: window.New(capacity), prev: make([]int, capacity),
		heads: map[uint32]int{}, maxChain: maxChain}, nil
}

// NewWithWindow reuses a window and indexes its current contents as a preset
// dictionary (used by parallel block compression).
func NewWithWindow(win *window.Window, maxChain int) (*Matcher, error) {
	if win == nil || maxChain <= 0 {
		return nil, ErrConfig
	}
	m := &Matcher{win: win, prev: make([]int, win.Cap()),
		heads: map[uint32]int{}, maxChain: maxChain}
	for i := 0; i+MinMatch <= win.Size(); i++ {
		m.index(i)
	}
	m.pushed = int64(win.Size())
	return m, nil
}

// Window exposes the backing window.
func (m *Matcher) Window() *window.Window { return m.win }

// Cands returns the total number of examined candidate positions.
func (m *Matcher) Cands() int64 { return m.cands }

func key(b0, b1, b2 byte) uint32 {
	return uint32(b0)<<16 | uint32(b1)<<8 | uint32(b2)
}

// byteAt returns the byte at absolute position p, reading history window or
// future bytes in fut (whose absolute start is win.Size()).
func (m *Matcher) byteAt(p int, fut []byte) byte {
	if off := int(m.pushed) - 1 - p; off >= 0 {
		return m.win.Byte(off + 1)
	}
	return fut[p-int(m.pushed)]
}

func (m *Matcher) index(abs int) {
	w := m.win
	d := int(m.pushed) - abs
	k := key(w.Byte(d), w.Byte(d-1), w.Byte(d-2))
	if head, ok := m.heads[k]; ok {
		m.prev[abs%w.Cap()] = head
	} else {
		m.prev[abs%w.Cap()] = -1
	}
	m.heads[k] = abs
}

// Push appends a committed byte and indexes positions as they gain 3 bytes.
func (m *Matcher) Push(b byte) {
	newPos := int(m.pushed)
	m.win.Push(b)
	m.pushed++
	if newPos >= MinMatch-1 {
		m.index(newPos - (MinMatch - 1))
	}
}

// Find returns the longest match for fut[0:] anchored at absolute position
// pushed. dist is 1-based (0 means no match); length counts matched bytes.
func (m *Matcher) Find(fut []byte, maxLen int) (dist, length int) {
	if len(fut) < MinMatch {
		return 0, 0
	}
	k := key(fut[0], fut[1], fut[2])
	cur, ok := m.heads[k]
	if !ok {
		return 0, 0
	}
	limit := int(m.pushed) - m.win.Cap()
	if int(m.pushed) < m.win.Cap() {
		limit = 0
	}
	pos := int(m.pushed)
	best := 0
	for c := 0; c < m.maxChain; c++ {
		m.cands++
		if cur < limit || cur < 0 || pos-cur > m.win.Cap() {
			break
		}
		d := pos - cur
		ml := len(fut)
		if maxLen < ml {
			ml = maxLen
		}
		l := 0
		for l < ml && m.byteAt(cur+l, fut) == fut[l] {
			l++
		}
		if l > best {
			dist, best = d, l
		}
		if l == ml || best >= maxLen {
			break
		}
		cur = m.prev[cur%m.win.Cap()]
}
	return dist, best
}
