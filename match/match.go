package match

import (
	"errors"

	"ontology/window"
)

var ErrConfig = errors.New("chain limit must be positive")

const MinLength = 3

type Matcher struct {
	win *window.Window
	limit int
	head  map[uint32]int
	prev  []int
	total int
	done  int
	tests int64
}

func New(win *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrConfig
	}
	return &Matcher{
		win:   win,
		limit: chainLimit,
		head:  make(map[uint32]int),
		prev:  make([]int, win.Cap()),
	}, nil
}

func (m *Matcher) Reset() {
	clear(m.head)
	clear(m.prev)
	m.total, m.done, m.tests = 0, 0, 0
}

func (m *Matcher) Candidates() int64 { return m.tests }

// Sync records n bytes just appended to the window.
func (m *Matcher) Sync(n int) {
	m.total += n
	for ; m.done+MinLength <= m.total; m.done++ {
		first := m.total - m.done
		b0, _ := m.win.Get(first)
		b1, _ := m.win.Get(first - 1)
		b2, _ := m.win.Get(first - 2)
		h := hash3(b0, b1, b2)
		pos := m.done
		m.prev[pos%len(m.prev)] = m.head[h]
		m.head[h] = pos
	}
}

// Find searches a match for pending[0] using repeated bytes from history.
func (m *Matcher) Find(pending []byte) (distance, length int) {
	if len(pending) < MinLength {
		return 0, 0
	}
	h := hash3(pending[0], pending[1], pending[2])
	cand := m.head[h]
	for n := 0; n < m.limit && cand != 0; n++ {
		m.tests++
		d := m.total - cand
		if d <= 0 || d > m.win.Cap() {
			break
		}
		l := m.compare(d, pending)
		if l > length {
			distance, length = d, l
		}
		next := m.prev[cand%len(m.prev)]
		if next == cand {
			break
		}
		cand = next
	}
	return distance, length
}

func (m *Matcher) compare(distance int, pending []byte) int {
	limit := len(pending)
	for k := 0; k < limit; k++ {
		b, ok := m.win.Get(distance - (k % distance))
		if !ok || b != pending[k] {
			return k
		}
	}
	return limit
}

func hash3(a, b, c byte) uint32 {
	return uint32(a)<<16 ^ uint32(b)<<8 ^ uint32(c)
}
