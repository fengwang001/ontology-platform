package undo

type naiveSegment struct {
	id      int
	kind    segmentKind
	records int
	pages   int
}

type naiveTransaction struct {
	registered bool
	active     bool
	segments   [2]*naiveSegment
}

type naiveHistory struct {
	trxNo int
	seg   *naiveSegment
}

type naiveSnapshot struct {
	usedSlots int
	usedPages int
	caches    [2][]int
	history   []int
}

type naiveManager struct {
	s, k, pb int
	used     int
	pages    int
	slots    map[int]int
	trxs     map[int]*naiveTransaction
	caches   [2][]*naiveSegment
	history  []naiveHistory
	commits  int
	views    map[int]int
	nextView int
}

func newNaiveManager(s, k, pb int) *naiveManager {
	return &naiveManager{
		s: s, k: k, pb: pb,
		slots:    map[int]int{},
		trxs:     map[int]*naiveTransaction{},
		views:    map[int]int{},
		nextView: 1,
	}
}

func (n *naiveManager) Begin(t int) error {
	if t < 1 || t > 1_000_000 {
		return ErrInvalidArgument
	}
	if n.trxs[t] != nil {
		return ErrTransactionExists
	}
	n.trxs[t] = &naiveTransaction{registered: true, active: true}
	return nil
}

func (n *naiveManager) Record(t int, kind segmentKind) error {
	if t < 1 || t > 1_000_000 {
		return ErrInvalidArgument
	}
	trx := n.trxs[t]
	if trx == nil {
		return ErrTransactionNotFound
	}
	if !trx.active {
		return ErrTransactionDone
	}

	seg := trx.segments[kind]
	if seg == nil {
		if stack := n.caches[kind]; len(stack) > 0 {
			seg = stack[len(stack)-1]
			n.caches[kind] = stack[:len(stack)-1]
			seg.records = 0
		} else {
			if n.used == n.s {
				return ErrNoFreeSlot
			}
			if n.pages == n.pb {
				return ErrPageBudgetExceeded
			}
			slot := 0
			for n.slots[slot] != 0 {
				slot++
			}
			seg = &naiveSegment{id: slot + 1, kind: kind, pages: 1}
			n.slots[slot] = seg.id
			n.used++
			n.pages++
		}
		trx.segments[kind] = seg
	}

	pages := (seg.records + n.k) / n.k
	if pages > seg.pages {
		if n.pages == n.pb {
			return ErrPageBudgetExceeded
		}
		n.pages++
		seg.pages++
	}
	seg.records++
	return nil
}

func (n *naiveManager) Commit(t int) (int, error) {
	if t < 1 || t > 1_000_000 {
		return 0, ErrInvalidArgument
	}
	trx := n.trxs[t]
	if trx == nil {
		return 0, ErrTransactionNotFound
	}
	if !trx.active {
		return 0, ErrTransactionDone
	}

	n.commits++
	trxNo := n.commits
	if seg := trx.segments[insertUndo]; seg != nil {
		n.release(seg)
		trx.segments[insertUndo] = nil
	}
	if seg := trx.segments[updateUndo]; seg != nil {
		n.history = append(n.history, naiveHistory{trxNo: trxNo, seg: seg})
		trx.segments[updateUndo] = nil
	}
	trx.active = false
	return trxNo, nil
}

func (n *naiveManager) Rollback(t int) error {
	if t < 1 || t > 1_000_000 {
		return ErrInvalidArgument
	}
	trx := n.trxs[t]
	if trx == nil {
		return ErrTransactionNotFound
	}
	if !trx.active {
		return ErrTransactionDone
	}
	for _, seg := range trx.segments {
		if seg != nil {
			n.release(seg)
		}
	}
	trx.segments = [2]*naiveSegment{}
	trx.active = false
	return nil
}

func (n *naiveManager) OpenView() int {
	id := n.nextView
	n.nextView++
	n.views[id] = n.commits + 1
	return id
}

func (n *naiveManager) CloseView(id int) error {
	if _, ok := n.views[id]; !ok {
		return ErrViewNotFound
	}
	delete(n.views, id)
	return nil
}

func (n *naiveManager) Purge(limit int) ([]int, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	pl := n.commits + 1
	for _, limit := range n.views {
		if limit < pl {
			pl = limit
		}
	}
	out := []int{}
	for len(out) < limit && len(n.history) > 0 {
		if n.history[0].trxNo >= pl {
			break
		}
		entry := n.history[0]
		n.release(entry.seg)
		n.history = n.history[1:]
		out = append(out, entry.trxNo)
	}
	return out, nil
}

func (n *naiveManager) release(seg *naiveSegment) {
	if seg.pages == 1 && 4*seg.records <= 3*n.k {
		n.caches[seg.kind] = append(n.caches[seg.kind], seg)
		return
	}
	for slot, id := range n.slots {
		if id == seg.id {
			delete(n.slots, slot)
			break
		}
	}
	n.used--
	n.pages -= seg.pages
}

func (n *naiveManager) snapshot() naiveSnapshot {
	out := naiveSnapshot{usedSlots: n.used, usedPages: n.pages}
	for kind, stack := range n.caches {
		for _, seg := range stack {
			out.caches[kind] = append(out.caches[kind], seg.id)
		}
	}
	for _, entry := range n.history {
		out.history = append(out.history, entry.trxNo)
	}
	return out
}
