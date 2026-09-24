// Package tjoin implements the versioned table, watermark, event buffer and
// FOR SYSTEM_TIME AS OF temporal join emission.
package tjoin

import (
	"errors"
	"math"
	"sort"
	"sync"

	"ontology/ver"
)

// The four failure kinds are mutually distinguishable sentinel errors.
var (
	ErrEmptyKey      = errors.New("tjoin: empty key")
	ErrLateVersion   = errors.New("tjoin: version change at or below watermark")
	ErrWatermarkBack = errors.New("tjoin: watermark cannot move backwards")
	ErrBufferFull    = errors.New("tjoin: event buffer full")
)

// Joined is one temporal join result emitted for an accepted event.
type Joined struct {
	Key, Value string
	TS         int64
	Seq        int
	Found      bool
}
type pending struct {
	key     string
	ts, seq int64
}

// Table is the in-memory versioned table plus stream join state; seq is the
// next arrival sequence and only accepted events consume one.
type Table struct {
	mu            sync.Mutex
	vers          map[string]ver.Versions
	vwm           int64
	wmSet, flushd bool
	buf           []pending
	maxBuf        int
	seq           int64
	outputs       []Joined
}

// New creates a Table buffering at most maxBuffered events.
func New(maxBuffered int) *Table {
	return &Table{vers: map[string]ver.Versions{}, maxBuf: maxBuffered}
}
func (t *Table) write(key string, vf int64, tomb bool, value string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key == "" {
		return ErrEmptyKey // validate before touching any state
	}
	if t.flushd || (t.wmSet && vf <= t.vwm) {
		return ErrLateVersion
	}
	s := t.vers[key] // map value copy; Put on the local, write back
	s.Put(ver.Version{ValidFrom: vf, Value: value, Tombstone: tomb})
	t.vers[key] = s
	return nil
}
func (t *Table) Upsert(key string, validFrom int64, value string) error {
	return t.write(key, validFrom, false, value)
}
func (t *Table) Delete(key string, validFrom int64) error {
	return t.write(key, validFrom, true, "")
}
func (t *Table) joinLocked(key string, ts int64) Joined {
	j := Joined{Key: key, TS: ts}
	if s := t.vers[key]; s.Len() > 0 {
		if v, ok := s.AsOf(ts); ok && !v.Tombstone {
			j.Found, j.Value = true, v.Value
		}
	}
	return j
}

// releaseLocked emits buffered events with TS <= vwm ordered by (TS, Seq);
// callers must hold t.mu. After Flush vwm is MaxInt64, so one split handles
// both. Arrival order in the buffer carries no meaning.
func (t *Table) releaseLocked() []Joined {
	sort.Slice(t.buf, func(i, j int) bool {
		return t.buf[i].ts < t.buf[j].ts || (t.buf[i].ts == t.buf[j].ts && t.buf[i].seq < t.buf[j].seq)
	})
	n := sort.Search(len(t.buf), func(i int) bool { return t.buf[i].ts > t.vwm })
	out := make([]Joined, 0, n)
	for _, p := range t.buf[:n] {
		j := t.joinLocked(p.key, p.ts)
		j.Seq = int(p.seq)
		t.outputs = append(t.outputs, j)
		out = append(out, j)
	}
	t.buf = append([]pending(nil), t.buf[n:]...)
	return out
}

// Feed accepts one event, emitting it immediately when TS <= vwm (or after
// Flush), otherwise buffering it.
func (t *Table) Feed(key string, ts int64) ([]Joined, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key == "" {
		return nil, ErrEmptyKey
	}
	imm := t.flushd || (t.wmSet && ts <= t.vwm)
	if !imm && len(t.buf) >= t.maxBuf {
		return nil, ErrBufferFull // checked before seq is spent
	}
	seq := t.seq
	t.seq++
	if !imm {
		t.buf = append(t.buf, pending{key, ts, seq})
		return nil, nil
	}
	j := t.joinLocked(key, ts)
	j.Seq = int(seq)
	t.outputs = append(t.outputs, j)
	return []Joined{j}, nil
}

// Watermark advances to w and releases buffered events with TS <= w. After
// Flush vwm is MaxInt64, so one comparison covers no-op and backwards.
func (t *Table) Watermark(w int64) ([]Joined, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.wmSet && w <= t.vwm {
		if w < t.vwm {
			return nil, ErrWatermarkBack
		}
		return nil, nil
	}
	t.wmSet, t.vwm, t.flushd = true, w, w == math.MaxInt64
	return t.releaseLocked(), nil
}

// Flush advances the watermark to positive infinity and releases all events.
func (t *Table) Flush() []Joined { out, _ := t.Watermark(math.MaxInt64); return out }

// Outputs returns a copy of every result emitted so far.
func (t *Table) Outputs() []Joined {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Joined, len(t.outputs))
	copy(out, t.outputs)
	return out
}
