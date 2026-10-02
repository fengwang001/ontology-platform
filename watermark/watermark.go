package watermark

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

type Policy int

const (
	Earliest Policy = iota
	Latest
	End
)

var (
	ErrInvalidParam = errors.New("watermark: invalid parameter")
	ErrCapacity     = errors.New("watermark: pane table capacity exceeded")
	ErrRegression   = errors.New("watermark: input watermark regression")
)

type LatePane struct {
	WS    int64
	Count int64
	Sum   int64
	TS    int64
}

type OnTimePane struct {
	WS    int64
	Count int64
	Sum   int64
	TS    int64
}

type PaneInfo struct {
	WS    int64
	Count int64
	Sum   int64
	MinTS int64
	MaxTS int64
	Hold  int64
}

type pane struct {
	count int64
	sum   int64
	minTS int64
	maxTS int64
	hold  int64
}

type Merger struct {
	mu     sync.Mutex
	S      int64
	D      int64
	policy Policy
	AL     int64
	Cap    int64

	I       int64
	O       int64
	panes   map[int64]*pane
	expiry  wsHeap
	holds   holdHeap
	dropped int64

	holdProbes int64
	holdStale  int64
}

func New(S, D int64, policy Policy, AL, Cap int64) (*Merger, error) {
	if D < 1 || S < D || S > 16*D || S > 1_000_000_000 ||
		AL < 0 || AL > 1_000_000_000 ||
		Cap < 1 || Cap > 1_000_000 ||
		policy < Earliest || policy > End {
		return nil, ErrInvalidParam
	}
	return &Merger{
		S: S, D: D, policy: policy, AL: AL, Cap: Cap,
		I: -1, O: -1,
		panes: make(map[int64]*pane),
	}, nil
}

const maxTS = 1_000_000_000_000_000

// floorDiv returns floor(a/b) for b > 0, exact for negative a.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// ceilDiv returns ceil(a/b) for b > 0, exact for negative a.
func ceilDiv(a, b int64) int64 {
	return -floorDiv(-a, b)
}

func (m *Merger) Add(ts, val int64) ([]LatePane, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts < 0 || ts > maxTS || val < -1_000_000_000 || val > 1_000_000_000 {
		return nil, ErrInvalidParam
	}
	kLo := ceilDiv(ts-m.S+1, m.D)
	kHi := floorDiv(ts, m.D)
	var lates []LatePane
	var buffered []int64
	var newPanes int64
	var drops int64
	for k := kLo; k <= kHi; k++ {
		ws := k * m.D
		end := ws + m.S
		switch {
		case m.I >= end+m.AL:
			drops++
		case m.I >= end:
			f := ts
			if m.policy == End {
				f = end - 1
			}
			lates = append(lates, LatePane{WS: ws, Count: 1, Sum: val, TS: max(f, m.O)})
		default:
			buffered = append(buffered, ws)
			if _, ok := m.panes[ws]; !ok {
				newPanes++
			}
		}
	}
	if int64(len(m.panes))+newPanes > m.Cap {
		return nil, ErrCapacity
	}
	m.dropped += drops
	for _, ws := range buffered {
		end := ws + m.S
		p, ok := m.panes[ws]
		if !ok {
			p = &pane{minTS: ts, maxTS: ts}
			m.panes[ws] = p
			heap.Push(&m.expiry, ws)
		}
		p.count++
		p.sum += val
		p.minTS = min(p.minTS, ts)
		p.maxTS = max(p.maxTS, ts)
		var raw int64
		switch m.policy {
		case Earliest:
			raw = p.minTS
		case Latest:
			raw = p.maxTS
		case End:
			raw = end - 1
		}
		p.hold = max(raw, m.O)
		heap.Push(&m.holds, holdEntry{hold: p.hold, ws: ws})
	}
	return lates, nil
}

func (m *Merger) Advance(I2 int64) ([]OnTimePane, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if I2 < 0 || I2 > maxTS {
		return nil, ErrInvalidParam
	}
	if I2 < m.I {
		return nil, ErrRegression
	}
	m.I = I2
	var out []OnTimePane
	for len(m.expiry) > 0 && m.expiry[0]+m.S <= I2 {
		ws := heap.Pop(&m.expiry).(int64)
		p := m.panes[ws]
		delete(m.panes, ws)
		out = append(out, OnTimePane{WS: ws, Count: p.count, Sum: p.sum, TS: p.hold})
	}
	if h, ok := m.minHold(); ok {
		m.O = max(m.O, min(I2, h))
	} else {
		m.O = max(m.O, I2)
	}
	return out, nil
}

// minHold returns the minimum hold among buffered panes, lazily
// discarding stale heap entries. Every inspected heap item, including
// discarded stale ones, is counted in holdProbes.
func (m *Merger) minHold() (int64, bool) {
	for len(m.holds) > 0 {
		m.holdProbes++
		top := m.holds[0]
		p, ok := m.panes[top.ws]
		if !ok || p.hold != top.hold {
			m.holdStale++
			heap.Pop(&m.holds)
			continue
		}
		return top.hold, true
	}
	return 0, false
}

func (m *Merger) Output() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.O
}

func (m *Merger) Dropped() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped
}

func (m *Merger) Panes() []PaneInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PaneInfo, 0, len(m.panes))
	for ws, p := range m.panes {
		out = append(out, PaneInfo{
			WS:    ws,
			Count: p.count,
			Sum:   p.sum,
			MinTS: p.minTS,
			MaxTS: p.maxTS,
			Hold:  p.hold,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WS < out[j].WS })
	return out
}
