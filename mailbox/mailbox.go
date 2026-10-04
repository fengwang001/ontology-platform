// Package mailbox implements an offline-device pending-message box with
// collapse keys, overflow markers, capacity eviction, TTL expiry and
// priority-ordered draining.
package mailbox

import (
	"errors"
	"sort"
	"sync"
)

// Limits for constructor and operation parameters.
const (
	MaxK     = 64
	MaxP     = 10000
	MaxLmax  = 10000
	MaxCKLen = 64
	MaxTTL   = 1_000_000_000
	MaxNow   = 1_000_000_000_000
	MaxCnt   = 10000
)

// Rejection reasons. Rejected operations never mutate the box.
var (
	ErrInvalidParam  = errors.New("mailbox: invalid parameter")
	ErrClockRollback = errors.New("mailbox: clock rollback")
	ErrDuplicateID   = errors.New("mailbox: duplicate id")
)

// Result is the disposition of a single Enqueue call.
type Result int

const (
	// Stored: the message was stored in the box.
	Stored Result = iota
	// Collapsed: the message replaced an existing message with the same
	// collapse key.
	Collapsed
	// Overflow: the message triggered a category overflow and was discarded
	// together with the live messages of its category; the count was merged
	// into the overflow marker.
	Overflow
	// Evicted: the message was stored but immediately evicted by the total
	// capacity rule (it was the lowest-priority, smallest-seq message).
	Evicted
	// Dropped: the message had ttl == 0 and was not stored.
	Dropped
)

func (r Result) String() string {
	switch r {
	case Stored:
		return "Stored"
	case Collapsed:
		return "Collapsed"
	case Overflow:
		return "Overflow"
	case Evicted:
		return "Evicted"
	case Dropped:
		return "Dropped"
	}
	return "Unknown"
}

// Item is an entry returned by Drain or Peek: either a message or the
// overflow marker.
type Item struct {
	// Marker is true when this item is the overflow marker; then N holds
	// the accumulated discarded count.
	Marker bool
	N      int64
	// Message fields, valid when Marker is false.
	ID   string
	CK   string
	Prio int
	Exp  int64
	Seq  uint64
}

type message struct {
	id   string
	ck   string
	prio int
	exp  int64
	seq  uint64
}

type marker struct {
	n   int64
	seq uint64
}

// Mailbox is a pending-message box. All methods are safe for concurrent
// use; results are equivalent to some serial order.
type Mailbox struct {
	mu     sync.Mutex
	k      int
	p      int
	lmax   int
	seq    uint64
	maxNow int64

	msgs  map[string]*message // messages in the box, by id
	ckSet map[string]bool     // distinct collapse keys currently in the box
	plain int                 // number of uncollapsible messages
	mark  *marker             // at most one overflow marker
}

// New creates a Mailbox. K is the collapse-key kind limit (1..64), P the
// uncollapsible message limit (1..1e4) and Lmax the total message limit
// (1..1e4, marker not counted).
func New(K, P, Lmax int) (*Mailbox, error) {
	if K < 1 || K > MaxK || P < 1 || P > MaxP || Lmax < 1 || Lmax > MaxLmax {
		return nil, ErrInvalidParam
	}
	return &Mailbox{
		k:     K,
		p:     P,
		lmax:  Lmax,
		msgs:  make(map[string]*message),
		ckSet: make(map[string]bool),
	}, nil
}

// Enqueue stores a message. id must be non-empty; ck is the collapse key
// (empty means uncollapsible, otherwise at most 64 bytes); prio is 0
// (normal) or 1 (high); ttl is 0..1e9; now is 0..1e12. The message expires
// at exp = now + ttl and is alive iff exp > current time.
func (m *Mailbox) Enqueue(id, ck string, prio int, ttl, now int64) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" || len(ck) > MaxCKLen || (prio != 0 && prio != 1) ||
		ttl < 0 || ttl > MaxTTL || now < 0 || now > MaxNow {
		return 0, ErrInvalidParam
	}
	if now < m.maxNow {
		return 0, ErrClockRollback
	}
	for _, msg := range m.msgs {
		if msg.id == id && msg.exp > now {
			return 0, ErrDuplicateID
		}
	}
	m.maxNow = now
	m.purge(now)

	if ttl == 0 {
		return Dropped, nil
	}
	exp := now + ttl

	if ck != "" {
		if old := m.findByCK(ck); old != nil {
			m.remove(old)
			m.store(&message{id: id, ck: ck, prio: prio, exp: exp})
			return Collapsed, nil
		}
		if len(m.ckSet) == m.k {
			// Collapse overflow: discard every collapsible message plus
			// the new one.
			c := int64(len(m.ckSet) + 1)
			for _, msg := range m.msgs {
				if msg.ck != "" {
					m.remove(msg)
				}
			}
			m.bumpMarker(c)
			return Overflow, nil
		}
		msg := m.store(&message{id: id, ck: ck, prio: prio, exp: exp})
		return m.maybeEvict(msg), nil
	}

	if m.plain == m.p {
		// Uncollapsible overflow: discard every uncollapsible message plus
		// the new one.
		c := int64(m.plain + 1)
		for _, msg := range m.msgs {
			if msg.ck == "" {
				m.remove(msg)
			}
		}
		m.bumpMarker(c)
		return Overflow, nil
	}
	msg := m.store(&message{id: id, ck: "", prio: prio, exp: exp})
	return m.maybeEvict(msg), nil
}

// Drain removes and returns up to cnt items (1..1e4). Expired messages are
// purged first, then items are taken in order: the high class (high-priority
// messages and the marker) before the normal class, ascending seq within
// each class.
func (m *Mailbox) Drain(now int64, cnt int) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cnt < 1 || cnt > MaxCnt || now < 0 || now > MaxNow {
		return nil, ErrInvalidParam
	}
	if now < m.maxNow {
		return nil, ErrClockRollback
	}
	m.maxNow = now
	m.purge(now)

	items := m.orderedAt(-1)
	if len(items) > cnt {
		items = items[:cnt]
	}
	for _, it := range items {
		if it.Marker {
			m.mark = nil
		} else {
			m.remove(m.msgs[it.ID])
		}
	}
	return items, nil
}

// Peek returns the live items in the same order Drain would take them,
// without purging or otherwise mutating the box. now must not be smaller
// than the maximum now of accepted Enqueue/Drain calls.
func (m *Mailbox) Peek(now int64) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now < 0 || now > MaxNow {
		return nil, ErrInvalidParam
	}
	if now < m.maxNow {
		return nil, ErrClockRollback
	}
	return m.orderedAt(now), nil
}

// purge silently removes all messages with exp <= now. Never counted in the
// marker.
func (m *Mailbox) purge(now int64) {
	for _, msg := range m.msgs {
		if msg.exp <= now {
			m.remove(msg)
		}
	}
}

func (m *Mailbox) findByCK(ck string) *message {
	for _, msg := range m.msgs {
		if msg.ck == ck {
			return msg
		}
	}
	return nil
}

// store inserts a message, assigning it a fresh seq.
func (m *Mailbox) store(msg *message) *message {
	m.seq++
	msg.seq = m.seq
	m.msgs[msg.id] = msg
	if msg.ck != "" {
		m.ckSet[msg.ck] = true
	} else {
		m.plain++
	}
	return msg
}

func (m *Mailbox) remove(msg *message) {
	if msg == nil {
		return
	}
	delete(m.msgs, msg.id)
	if msg.ck != "" {
		delete(m.ckSet, msg.ck)
	} else {
		m.plain--
	}
}

// bumpMarker adds c to the overflow marker, creating it if absent, and
// assigns the marker a fresh seq.
func (m *Mailbox) bumpMarker(c int64) {
	m.seq++
	if m.mark != nil {
		m.mark.n += c
		m.mark.seq = m.seq
	} else {
		m.mark = &marker{n: c, seq: m.seq}
	}
}

// maybeEvict applies the total-capacity rule after a Stored insert. It
// returns the final Enqueue result.
func (m *Mailbox) maybeEvict(fresh *message) Result {
	if len(m.msgs) <= m.lmax {
		return Stored
	}
	var victim *message
	for _, msg := range m.msgs {
		if msg.prio != 0 {
			continue
		}
		if victim == nil || msg.seq < victim.seq {
			victim = msg
		}
	}
	if victim == nil {
		for _, msg := range m.msgs {
			if victim == nil || msg.seq < victim.seq {
				victim = msg
			}
		}
	}
	m.remove(victim)
	m.bumpMarker(1)
	if victim == fresh {
		return Evicted
	}
	return Stored
}

// orderedAt builds items in drain order. When now >= 0 only messages with
// exp > now are included (Peek); when now < 0 all messages are included
// (Drain, called after purge).
func (m *Mailbox) orderedAt(now int64) []Item {
	var high, normal []Item
	for _, msg := range m.msgs {
		if now >= 0 && msg.exp <= now {
			continue
		}
		it := Item{
			ID:   msg.id,
			CK:   msg.ck,
			Prio: msg.prio,
			Exp:  msg.exp,
			Seq:  msg.seq,
		}
		if msg.prio == 1 {
			high = append(high, it)
		} else {
			normal = append(normal, it)
		}
	}
	if m.mark != nil {
		high = append(high, Item{Marker: true, N: m.mark.n, Seq: m.mark.seq})
	}
	bySeq := func(items []Item) {
		sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	}
	bySeq(high)
	bySeq(normal)
	return append(high, normal...)
}
