package mailbox

import "sort"

// naiveBox 是按规格逐条写成的朴素模拟：只用切片与线性扫描，
// 与正式实现结构完全不同，用于随机对拍。
type naiveBox struct {
	k, p, lmax int

	msgs      []naiveMsg
	hasMarker bool
	markerN   uint64
	markerSeq uint64

	seq    uint64
	maxNow int64

	// 守恒校验：入箱丢弃或淘汰的存活消息数（含触发溢出的新消息与
	// 被自己淘汰的新消息）累计，与标记累计 n（含已取走）恒等。
	discarded uint64
	marked    uint64
}

type naiveMsg struct {
	id, ck string
	prio   int
	exp    int64
	seq    uint64
}

func newNaive(k, p, lmax int) *naiveBox {
	return &naiveBox{k: k, p: p, lmax: lmax}
}

func (n *naiveBox) nextSeq() uint64 {
	n.seq++
	return n.seq
}

func (n *naiveBox) purge(now int64) {
	kept := n.msgs[:0]
	for _, m := range n.msgs {
		if m.exp > now {
			kept = append(kept, m)
		}
	}
	n.msgs = kept
}

func (n *naiveBox) bumpMarker(c uint64) {
	n.markerN += c
	n.markerSeq = n.nextSeq()
	n.hasMarker = true
	n.marked += c
}

func (n *naiveBox) enqueue(id, ck string, prio int, ttl, now int64) (Outcome, error, string) {
	if id == "" || len(ck) > maxCKLen ||
		(prio != 0 && prio != 1) ||
		ttl < 0 || ttl > maxTTL || now < 0 || now > maxNow {
		return 0, ErrInvalidParam, "参数非法"
	}
	if now < n.maxNow {
		return 0, ErrClockRegression, "时钟回退"
	}
	for _, m := range n.msgs {
		if m.id == id && m.exp > now {
			return 0, ErrDuplicateID, "与存活消息 id 重复"
		}
	}
	n.maxNow = now
	n.purge(now)

	if ttl == 0 {
		return OutcomeDropped, nil, "ttl 为 0，不存放"
	}

	if ck != "" {
		for i, m := range n.msgs {
			if m.ck == ck {
				n.msgs = append(n.msgs[:i], n.msgs[i+1:]...)
				n.msgs = append(n.msgs, naiveMsg{id: id, ck: ck, prio: prio, exp: now + ttl, seq: n.nextSeq()})
				return OutcomeCollapsed, nil, "同 ck 替换，取新序号"
			}
		}
		kinds := 0
		seen := map[string]bool{}
		for _, m := range n.msgs {
			if m.ck != "" && !seen[m.ck] {
				seen[m.ck] = true
				kinds++
			}
		}
		if kinds >= n.k {
			before := len(n.msgs)
			kept := n.msgs[:0]
			for _, m := range n.msgs {
				if m.ck == "" {
					kept = append(kept, m)
				}
			}
			n.msgs = kept
			c := uint64(before-len(n.msgs)) + 1
			n.discarded += c
			n.bumpMarker(c)
			return OutcomeOverflow, nil, "折叠溢出，丢弃全部可折叠消息与新消息"
		}
		return n.storeAndEvict(id, ck, prio, ttl, now), nil, "存入"
	}

	nonCol := 0
	for _, m := range n.msgs {
		if m.ck == "" {
			nonCol++
		}
	}
	if nonCol >= n.p {
		before := len(n.msgs)
		kept := n.msgs[:0]
		for _, m := range n.msgs {
			if m.ck != "" {
				kept = append(kept, m)
			}
		}
		n.msgs = kept
		c := uint64(before-len(n.msgs)) + 1
		n.discarded += c
		n.bumpMarker(c)
		return OutcomeOverflow, nil, "不可折叠溢出，丢弃全部不可折叠消息与新消息"
	}
	return n.storeAndEvict(id, ck, prio, ttl, now), nil, "存入"
}

func (n *naiveBox) storeAndEvict(id, ck string, prio int, ttl, now int64) Outcome {
	m := naiveMsg{id: id, ck: ck, prio: prio, exp: now + ttl, seq: n.nextSeq()}
	n.msgs = append(n.msgs, m)
	if len(n.msgs) <= n.lmax {
		return OutcomeStored
	}
	victim := -1
	for _, wantPrio := range []int{0, 1} {
		for i, c := range n.msgs {
			if c.prio == wantPrio && (victim < 0 || c.seq < n.msgs[victim].seq) {
				victim = i
			}
		}
		if victim >= 0 {
			break
		}
	}
	evicted := n.msgs[victim]
	n.msgs = append(n.msgs[:victim], n.msgs[victim+1:]...)
	n.discarded++
	n.bumpMarker(1)
	if evicted.seq == m.seq {
		return OutcomeEvicted
	}
	return OutcomeStored
}

// ordered 返回出队顺序：高优先级消息与标记一类在先（类内序号升序），
// 普通消息一类在后。
func (n *naiveBox) ordered() []Item {
	type ent struct {
		item Item
		high bool
	}
	var ents []ent
	for _, m := range n.msgs {
		ents = append(ents, ent{
			item: Item{ID: m.id, Prio: m.prio, Seq: m.seq, Exp: m.exp},
			high: m.prio == 1,
		})
	}
	if n.hasMarker {
		ents = append(ents, ent{
			item: Item{Marker: true, Seq: n.markerSeq, N: n.markerN},
			high: true,
		})
	}
	sort.SliceStable(ents, func(i, j int) bool {
		if ents[i].high != ents[j].high {
			return ents[i].high
		}
		return ents[i].item.Seq < ents[j].item.Seq
	})
	items := make([]Item, len(ents))
	for i, e := range ents {
		items[i] = e.item
	}
	return items
}

func (n *naiveBox) drain(now int64, cnt int) ([]Item, error) {
	if cnt < 1 || cnt > maxCnt || now < 0 || now > maxNow {
		return nil, ErrInvalidParam
	}
	if now < n.maxNow {
		return nil, ErrClockRegression
	}
	n.maxNow = now
	n.purge(now)

	items := n.ordered()
	if len(items) > cnt {
		items = items[:cnt]
	}
	for _, it := range items {
		if it.Marker {
			n.hasMarker = false
			n.markerN = 0
			continue
		}
		for i, m := range n.msgs {
			if m.seq == it.Seq {
				n.msgs = append(n.msgs[:i], n.msgs[i+1:]...)
				break
			}
		}
	}
	return items, nil
}

func (n *naiveBox) peek(now int64) ([]Item, error) {
	if now < 0 || now > maxNow {
		return nil, ErrInvalidParam
	}
	if now < n.maxNow {
		return nil, ErrClockRegression
	}
	var items []Item
	for _, it := range n.ordered() {
		if it.Marker {
			items = append(items, it)
			continue
		}
		live := false
		for _, m := range n.msgs {
			if m.seq == it.Seq && m.exp > now {
				live = true
				break
			}
		}
		if live {
			items = append(items, it)
		}
	}
	return items, nil
}
