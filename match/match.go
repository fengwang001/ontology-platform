// Package match finds longest matches in a sliding window using hash chains.
package match

import (
	"errors"

	"ontology/window"
)

// Match is a back-reference: Dist bytes back, Length bytes long.
type Match struct{ Dist, Length int }

// Matcher chains candidates of equal 3-byte hashes inside the window.
type Matcher struct {
	win      *window.Window
	chainCap int
	maxMatch int
	head     []int32 // bucket -> absolute position (-1 empty)
	prev     []int32 // slot (pos % cap) -> older candidate position
	pos      int     // absolute index of the next byte entering history
	checked  int64   // unexported candidate-examination counter

	wait       bool
	resumed    bool
	waitDist   int
	waitLen    int
	waitBudget int
}

// New constructs a matcher; windowCap and chainLimit must be positive.
func New(windowCap, chainLimit, maxMatch int) (*Matcher, error) {
	if windowCap <= 0 || chainLimit <= 0 || maxMatch < 3 {
		return nil, errors.New("match: need windowCap>0, chainLimit>0, maxMatch>=3")
	}
	w, err := window.New(windowCap)
	if err != nil {
		return nil, err
	}
	m := &Matcher{
		win:      w,
		chainCap: chainLimit,
		maxMatch: maxMatch,
		head:     make([]int32, 1<<16),
		prev:     make([]int32, windowCap),
		pos:      0,
	}
	for i := range m.head {
		m.head[i] = -1
	}
	return m, nil

}

// Window returns the owned window.
func (m *Matcher) Window() *window.Window { return m.win }

// Checked returns and resets the candidate-examination counter.
func (m *Matcher) Checked() int64 { v := m.checked; m.checked = 0; return v }

// Pos returns the number of bytes already committed into history.
func (m *Matcher) Pos() int { return m.pos }

// Preset indexes dict as prior history; only the last windowCap bytes matter.
func (m *Matcher) Preset(dict []byte) {
	if len(dict) > m.win.Cap() {
		dict = dict[len(dict)-m.win.Cap():]
	}
	m.win.Preset(dict)
	for i := 0; i+2 < len(dict); i++ {
		m.insert(dict[i:i+3], m.pos-len(dict)+i)
	}
	m.pos = len(dict)
}

func h3(p []byte) uint32 {
	return uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<16
}

// insert indexes the triple at absolute position sp (its bytes are historical).
func (m *Matcher) insert(tri []byte, sp int) {
	c := m.win.Cap()
	b := h3(tri) & uint32(len(m.head)-1)
	m.prev[((sp%c)+c)%c] = m.head[b]
	m.head[b] = int32(sp)
}

// Commit moves p[:n] into history, indexing every newly settled position whose
// triple is fully inside history (the last up to 2 positions use future bytes).
func (m *Matcher) Commit(p []byte, n int) []byte {
	if n > len(p) {
		n = len(p)
	}
	base := m.pos
	m.win.Write(p[:n])
	last := base + n - 3 // last triple fully inside the moved region
	// pos was base before write; earliest settled triple starts at base-2.
	lo := base - 2
	if lo < 0 {
		lo = 0
	}
	for sp := lo; sp <= last; sp++ {
		gi := sp - base
		var tri [3]byte
		if gi >= 0 {
			tri = [3]byte{p[gi], p[gi+1], p[gi+2]}
		} else {
			// Bytes before base: last byte from history via distances.
			tri[0], _ = m.win.At(n - gi)
			tri[1] = p[gi+1]
			tri[2] = p[gi+2]
		}
		m.insert(tri[:], sp)
	}
	m.pos += n
	m.wait = false
	return p[n:]
}

// tripleAt reads three bytes starting at distance d from the decision point:
// it may straddle the history/future boundary.
func (m *Matcher) tripleAt(d int, p []byte) [3]byte {
	var t [3]byte
	hist := m.win.Len()
	for i := 0; i < 3; i++ {
		dd := d - i
		if dd <= hist {
			t[2-i], _ = m.win.At(dd)
		} else {
			t[2-i] = p[dd-hist-1]
		}
	}
	return t
}

// Best is called with all uncommitted bytes in p (history holds earlier ones).
// It returns the best match at p's start. need=true => caller must supply a
// grown p via Best again (resume) after more data arrives; final=true forces
// a decision even when the match reaches p's end.
func (m *Matcher) Best(p []byte, final bool) (Match, bool, bool) {
	wasWait := m.wait
	if m.wait {
		// Resume: extend the suspended winner against the grown buffer.
		l := m.win.Extend(m.waitDist, m.waitLen, p, m.maxMatch)
		m.waitLen = l
		atEdge := l < m.maxMatch && m.waitDist+l >= m.win.Len()+len(p)
		if atEdge && !final {
			return Match{}, false, true
		}
		m.wait = false
		m.resumed = true
		if atEdge && final {
			return Match{m.waitDist, l}, true, false
		}
		// Winner froze inside the buffer: a candidate previously tied at
		// the edge may now be longer, so rescan the chain.
	}
	if len(p) < 3 {
		return Match{}, false, false
	}
	// Insert the position immediately before p if its triple is settled.
	if hist := m.win.Len(); hist >= 2 && !wasWait {
		var tri [3]byte
		tri[0], _ = m.win.At(hist)
		tri[1], _ = m.win.At(hist - 1)
		tri[2], _ = m.win.At(hist - 2)
		m.insert(tri[:], m.pos-1)
	}
	b := h3(p) & uint32(len(m.head)-1)
	cp := int(m.head[b])
	bestPos, bestLen := -1, 2
	for k := 0; k < m.chainCap && cp >= 0; k++ {
		d := m.pos - cp
		if d <= 0 || d > m.win.Cap() || d > m.win.Len() {
			break
		}
		m.checked++
		l := m.win.MatchLen(d, p, m.maxMatch)
		if l > bestLen {
			bestLen, bestPos = l, cp
			if l >= m.maxMatch {
				break
			}
		}
		// This candidate's triple must hash-equal; stop on stale slot.
		np := int(m.prev[((cp%m.win.Cap())+m.win.Cap())%m.win.Cap()])
		if np >= 0 && (m.pos-np <= 0 || m.pos-np > m.win.Cap()) {
			break
		}
		cp = np
	}
	if bestPos < 0 {
		return Match{}, false, false
	}
	d := m.pos - bestPos
	if !final && bestLen < m.maxMatch && d+bestLen >= m.win.Len()+len(p) {
		m.wait, m.waitDist, m.waitLen = true, d, bestLen
		return Match{d, bestLen}, true, true
	}
	return Match{d, bestLen}, true, false
}
