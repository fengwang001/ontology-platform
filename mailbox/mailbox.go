// Package mailbox implements an offline-device pending-delivery message box.
package mailbox

import (
	"errors"
	"sort"
	"sync"
)

const (
	maxK     = 64
	maxP     = 10000
	maxLmax  = 10000
	maxCKLen = 64
	maxTTL   = 1_000_000_000
	maxNow   = 1_000_000_000_000
	maxCnt   = 10000
)

var (
	ErrInvalidParam    = errors.New("mailbox: invalid parameter")
	ErrClockRegression = errors.New("mailbox: clock regression")
	ErrDuplicateID     = errors.New("mailbox: duplicate id")
)

type Outcome int

const (
	OutcomeStored Outcome = iota
	OutcomeCollapsed
	OutcomeOverflow
	OutcomeEvicted
	OutcomeDropped
)

func (o Outcome) String() string {
	switch o {
	case OutcomeStored:
		return "Stored"
	case OutcomeCollapsed:
		return "Collapsed"
	case OutcomeOverflow:
		return "Overflow"
	case OutcomeEvicted:
		return "Evicted"
	case OutcomeDropped:
		return "Dropped"
	}
	return "Unknown"
}

type Item struct {
	Marker bool
	ID     string
	Prio   int
	Seq    uint64
	N      uint64
	Exp    int64
}

type msg struct {
	id   string
	ck   string
	prio int
	exp  int64
	seq  uint64
}

type marker struct {
	n   uint64
	seq uint64
}

type Mailbox struct {
	mu sync.Mutex

	k, p, lmax int

	msgs   []*msg
	byID   map[string]*msg
	byCK   map[string]*msg
	nonCol int

	marker *marker
	seq    uint64
	maxNow int64
}

func NewMailbox(k, p, lmax int) (*Mailbox, error) {
	if k < 1 || k > maxK || p < 1 || p > maxP || lmax < 1 || lmax > maxLmax {
		return nil, ErrInvalidParam
	}
	return &Mailbox{
		k:    k,
		p:    p,
		lmax: lmax,
		byID: make(map[string]*msg),
		byCK: make(map[string]*msg),
	}, nil
}

// Enqueue 入箱一条消息。ttl==0 的消息不存放，返回 OutcomeDropped。
// 被拒绝时不改变任何状态（消息、标记、序号、最大 now 均不变）。
func (b *Mailbox) Enqueue(id, ck string, prio int, ttl, now int64) (Outcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id == "" || len(ck) > maxCKLen ||
		(prio != 0 && prio != 1) ||
		ttl < 0 || ttl > maxTTL || now < 0 || now > maxNow {
		return 0, ErrInvalidParam
	}
	if now < b.maxNow {
		return 0, ErrClockRegression
	}
	if m, ok := b.byID[id]; ok && m.exp > now {
		return 0, ErrDuplicateID
	}

	b.maxNow = now
	b.purgeExpired(now)

	if ttl == 0 {
		return OutcomeDropped, nil
	}

	if ck != "" {
		if old, ok := b.byCK[ck]; ok {
			b.removeMsg(old)
			b.storeMsg(id, ck, prio, now+ttl)
			return OutcomeCollapsed, nil
		}
		if len(b.byCK) >= b.k {
			c := uint64(len(b.byCK) + 1)
			b.dropAllCollapsible()
			b.bumpMarker(c)
			return OutcomeOverflow, nil
		}
		m := b.storeMsg(id, ck, prio, now+ttl)
		return b.maybeEvict(m), nil
	}

	if b.nonCol >= b.p {
		c := uint64(b.nonCol + 1)
		b.dropAllNonCollapsible()
		b.bumpMarker(c)
		return OutcomeOverflow, nil
	}
	m := b.storeMsg(id, "", prio, now+ttl)
	return b.maybeEvict(m), nil
}

// Drain 取出至多 cnt 项：先清除过期消息，再按「高优先级消息与标记一类
// 在先、普通消息一类在后，类内按序号升序」的顺序取前 cnt 项并移出箱。
func (b *Mailbox) Drain(now int64, cnt int) ([]Item, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cnt < 1 || cnt > maxCnt || now < 0 || now > maxNow {
		return nil, ErrInvalidParam
	}
	if now < b.maxNow {
		return nil, ErrClockRegression
	}
	b.maxNow = now
	b.purgeExpired(now)

	entries := b.orderedEntries()
	if len(entries) > cnt {
		entries = entries[:cnt]
	}
	items := make([]Item, 0, len(entries))
	for _, e := range entries {
		if e.m == nil {
			items = append(items, Item{Marker: true, Seq: b.marker.seq, N: b.marker.n})
			b.marker = nil
		} else {
			items = append(items, Item{ID: e.m.id, Prio: e.m.prio, Seq: e.m.seq, Exp: e.m.exp})
			b.removeMsg(e.m)
		}
	}
	return items, nil
}

// Peek 只读返回当前存活项按 Drain 同一顺序的列表，不清除任何消息。
func (b *Mailbox) Peek(now int64) ([]Item, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if now < 0 || now > maxNow {
		return nil, ErrInvalidParam
	}
	if now < b.maxNow {
		return nil, ErrClockRegression
	}

	var items []Item
	for _, e := range b.orderedEntries() {
		if e.m == nil {
			items = append(items, Item{Marker: true, Seq: b.marker.seq, N: b.marker.n})
		} else if e.m.exp > now {
			items = append(items, Item{ID: e.m.id, Prio: e.m.prio, Seq: e.m.seq, Exp: e.m.exp})
		}
	}
	return items, nil
}

type entry struct {
	m *msg // nil 表示标记
}

// orderedEntries 返回出队顺序：高优先级消息与标记一类（按序号升序）在先，
// 普通消息一类（按序号升序）在后。调用方须持锁。
func (b *Mailbox) orderedEntries() []entry {
	var high, normal []entry
	for _, m := range b.msgs {
		if m.prio == 1 {
			high = append(high, entry{m: m})
		} else {
			normal = append(normal, entry{m: m})
		}
	}
	if b.marker != nil {
		high = append(high, entry{m: nil})
	}
	seqOf := func(e entry) uint64 {
		if e.m == nil {
			return b.marker.seq
		}
		return e.m.seq
	}
	sort.Slice(high, func(i, j int) bool { return seqOf(high[i]) < seqOf(high[j]) })
	sort.Slice(normal, func(i, j int) bool { return normal[i].m.seq < normal[j].m.seq })
	return append(high, normal...)
}

// purgeExpired 静默清除全部 exp <= now 的消息，不计入标记。调用方须持锁。
func (b *Mailbox) purgeExpired(now int64) {
	kept := b.msgs[:0]
	for _, m := range b.msgs {
		if m.exp <= now {
			delete(b.byID, m.id)
			if m.ck != "" {
				delete(b.byCK, m.ck)
			} else {
				b.nonCol--
			}
		} else {
			kept = append(kept, m)
		}
	}
	for i := len(kept); i < len(b.msgs); i++ {
		b.msgs[i] = nil
	}
	b.msgs = kept
}

func (b *Mailbox) nextSeq() uint64 {
	b.seq++
	return b.seq
}

func (b *Mailbox) storeMsg(id, ck string, prio int, exp int64) *msg {
	m := &msg{id: id, ck: ck, prio: prio, exp: exp, seq: b.nextSeq()}
	b.msgs = append(b.msgs, m)
	b.byID[id] = m
	if ck != "" {
		b.byCK[ck] = m
	} else {
		b.nonCol++
	}
	return m
}

func (b *Mailbox) removeMsg(target *msg) {
	for i, m := range b.msgs {
		if m == target {
			copy(b.msgs[i:], b.msgs[i+1:])
			b.msgs[len(b.msgs)-1] = nil
			b.msgs = b.msgs[:len(b.msgs)-1]
			break
		}
	}
	delete(b.byID, target.id)
	if target.ck != "" {
		delete(b.byCK, target.ck)
	} else {
		b.nonCol--
	}
}

func (b *Mailbox) dropAllCollapsible() {
	kept := b.msgs[:0]
	for _, m := range b.msgs {
		if m.ck != "" {
			delete(b.byID, m.id)
			delete(b.byCK, m.ck)
		} else {
			kept = append(kept, m)
		}
	}
	for i := len(kept); i < len(b.msgs); i++ {
		b.msgs[i] = nil
	}
	b.msgs = kept
}

func (b *Mailbox) dropAllNonCollapsible() {
	kept := b.msgs[:0]
	for _, m := range b.msgs {
		if m.ck == "" {
			delete(b.byID, m.id)
			b.nonCol--
		} else {
			kept = append(kept, m)
		}
	}
	for i := len(kept); i < len(b.msgs); i++ {
		b.msgs[i] = nil
	}
	b.msgs = kept
}

func (b *Mailbox) bumpMarker(c uint64) {
	if b.marker != nil {
		b.marker.n += c
		b.marker.seq = b.nextSeq()
	} else {
		b.marker = &marker{n: c, seq: b.nextSeq()}
	}
}

// maybeEvict 在存入新消息后执行容量淘汰：总数超过 Lmax 时淘汰普通类中
// 序号最小者，无普通类则淘汰高优先级类中序号最小者。调用方须持锁。
func (b *Mailbox) maybeEvict(newMsg *msg) Outcome {
	if len(b.msgs) <= b.lmax {
		return OutcomeStored
	}
	var victim *msg
	for _, prio := range []int{0, 1} {
		for _, m := range b.msgs {
			if m.prio == prio && (victim == nil || m.seq < victim.seq) {
				victim = m
			}
		}
		if victim != nil {
			break
		}
	}
	b.removeMsg(victim)
	b.bumpMarker(1)
	if victim == newMsg {
		return OutcomeEvicted
	}
	return OutcomeStored
}
