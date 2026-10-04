package mv2pl

// naive is an independent, deliberately straightforward implementation
// of the same MV2PL certification rules, written directly from the
// specification. The randomized test cross-checks the real Manager
// against it call by call.
type naiveReq struct {
	txn  int
	mode Mode
	conv bool
	val  int64
}

type naiveTxn struct {
	state  State
	locks  map[int]Mode
	buffer map[int]int64
}

type naive struct {
	k         int
	next      int
	committed []int64
	granted   []map[int]Mode // key -> txn -> mode
	queue     [][]naiveReq
	txns      map[int]*naiveTxn
}

func newNaive(k int) *naive {
	n := &naive{
		k:         k,
		committed: make([]int64, k),
		granted:   make([]map[int]Mode, k),
		queue:     make([][]naiveReq, k),
		txns:      make(map[int]*naiveTxn),
	}
	for i := range n.granted {
		n.granted[i] = make(map[int]Mode)
	}
	return n
}

func (n *naive) begin() int {
	n.next++
	n.txns[n.next] = &naiveTxn{state: Active, locks: map[int]Mode{}, buffer: map[int]int64{}}
	return n.next
}

func (n *naive) canGrant(key, txn int, mode Mode) bool {
	for other, held := range n.granted[key] {
		if other != txn && !compatible(held, mode) {
			return false
		}
	}
	return true
}

func (n *naive) grant(key, txn int, mode Mode) {
	n.granted[key][txn] = mode
	n.txns[txn].locks[key] = mode
}

func (n *naive) releaseAll(txn int, pending map[int]bool) {
	tx := n.txns[txn]
	for key := range tx.locks {
		delete(n.granted[key], txn)
		pending[key] = true
	}
	tx.locks = map[int]Mode{}
	tx.buffer = map[int]int64{}
}

func (n *naive) removeFromQueues(txn int, pending map[int]bool) {
	for key := 0; key < n.k; key++ {
		var kept []naiveReq
		removed := false
		for _, r := range n.queue[key] {
			if r.txn == txn {
				removed = true
			} else {
				kept = append(kept, r)
			}
		}
		if removed {
			n.queue[key] = kept
			pending[key] = true
		}
	}
}

func (n *naive) allCertified(tx *naiveTxn) bool {
	for key := range tx.buffer {
		if tx.locks[key] != C {
			return false
		}
	}
	return true
}

func (n *naive) finishCommit(txn int, pending map[int]bool, events *[]Event) {
	tx := n.txns[txn]
	ev := Event{Kind: EventCommit, Txn: txn}
	for key := 0; key < n.k; key++ {
		if v, ok := tx.buffer[key]; ok {
			n.committed[key] = v
			ev.Writes = append(ev.Writes, WriteEntry{Key: key, Value: v})
		}
	}
	*events = append(*events, ev)
	n.releaseAll(txn, pending)
	tx.state = Committed
}

func (n *naive) drain(pending map[int]bool, events *[]Event) {
	for {
		key := -1
		for cand := range pending {
			if key == -1 || cand < key {
				key = cand
			}
		}
		if key == -1 {
			return
		}
		delete(pending, key)
		for len(n.queue[key]) > 0 {
			r := n.queue[key][0]
			if !n.canGrant(key, r.txn, r.mode) {
				break
			}
			n.queue[key] = n.queue[key][1:]
			tx := n.txns[r.txn]
			if r.conv {
				n.grant(key, r.txn, C)
				*events = append(*events, Event{Kind: EventGrant, Txn: r.txn, Key: key, Mode: C})
				if n.allCertified(tx) {
					n.finishCommit(r.txn, pending, events)
				}
			} else {
				n.grant(key, r.txn, r.mode)
				if r.mode == S {
					*events = append(*events, Event{Kind: EventGrant, Txn: r.txn, Key: key, Mode: S, Value: n.committed[key]})
				} else {
					tx.buffer[key] = r.val
					*events = append(*events, Event{Kind: EventGrant, Txn: r.txn, Key: key, Mode: X, Value: r.val})
				}
				tx.state = Active
			}
		}
	}
}

func (n *naive) abortTxn(txn int, events *[]Event) {
	pending := map[int]bool{}
	n.removeFromQueues(txn, pending)
	n.releaseAll(txn, pending)
	n.txns[txn].state = Aborted
	n.drain(pending, events)
}

func (n *naive) deadlocked(txn int) bool {
	edges := map[int][]int{}
	for key := 0; key < n.k; key++ {
		q := n.queue[key]
		for i, r := range q {
			for other, held := range n.granted[key] {
				if other != r.txn && !compatible(held, r.mode) {
					edges[r.txn] = append(edges[r.txn], other)
				}
			}
			for j := 0; j < i; j++ {
				if q[j].txn != r.txn {
					edges[r.txn] = append(edges[r.txn], q[j].txn)
				}
			}
		}
	}
	seen := map[int]bool{}
	var reaches func(cur int) bool
	reaches = func(cur int) bool {
		if cur == txn {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		for _, next := range edges[cur] {
			if reaches(next) {
				return true
			}
		}
		return false
	}
	for _, next := range edges[txn] {
		if reaches(next) {
			return true
		}
	}
	return false
}

func (n *naive) read(txn, key int) Result {
	tx, ok := n.txns[txn]
	if !ok {
		return rejected(RejectNoSuchTxn)
	}
	if tx.state != Active {
		return rejected(RejectBadState)
	}
	if key < 0 || key >= n.k {
		return rejected(RejectBadKey)
	}
	if mode, held := tx.locks[key]; held {
		if mode == X {
			return Result{OK: true, Value: tx.buffer[key]}
		}
		return Result{OK: true, Value: n.committed[key]}
	}
	if len(n.queue[key]) == 0 && n.canGrant(key, txn, S) {
		n.grant(key, txn, S)
		v := n.committed[key]
		return Result{OK: true, Value: v, Events: []Event{{Kind: EventGrant, Txn: txn, Key: key, Mode: S, Value: v}}}
	}
	n.queue[key] = append(n.queue[key], naiveReq{txn: txn, mode: S})
	tx.state = Waiting
	res := Result{OK: true}
	if n.deadlocked(txn) {
		n.abortTxn(txn, &res.Events)
		res.Deadlock = true
	}
	return res
}

func (n *naive) write(txn, key int, val int64) Result {
	tx, ok := n.txns[txn]
	if !ok {
		return rejected(RejectNoSuchTxn)
	}
	if tx.state != Active {
		return rejected(RejectBadState)
	}
	if key < 0 || key >= n.k {
		return rejected(RejectBadKey)
	}
	if tx.locks[key] == X {
		tx.buffer[key] = val
		return Result{OK: true}
	}
	if len(n.queue[key]) == 0 && n.canGrant(key, txn, X) {
		n.grant(key, txn, X)
		tx.buffer[key] = val
		return Result{OK: true, Events: []Event{{Kind: EventGrant, Txn: txn, Key: key, Mode: X, Value: val}}}
	}
	n.queue[key] = append(n.queue[key], naiveReq{txn: txn, mode: X, val: val})
	tx.state = Waiting
	res := Result{OK: true}
	if n.deadlocked(txn) {
		n.abortTxn(txn, &res.Events)
		res.Deadlock = true
	}
	return res
}

func (n *naive) commit(txn int) Result {
	tx, ok := n.txns[txn]
	if !ok {
		return rejected(RejectNoSuchTxn)
	}
	if tx.state != Active {
		return rejected(RejectBadState)
	}
	res := Result{OK: true}
	pending := map[int]bool{}
	hasX := false
	for key := 0; key < n.k; key++ {
		if tx.locks[key] == X {
			hasX = true
		}
	}
	if !hasX {
		n.finishCommit(txn, pending, &res.Events)
		n.drain(pending, &res.Events)
		return res
	}
	for key := 0; key < n.k; key++ {
		if tx.locks[key] != X {
			continue
		}
		if n.canGrant(key, txn, C) {
			n.grant(key, txn, C)
			res.Events = append(res.Events, Event{Kind: EventGrant, Txn: txn, Key: key, Mode: C})
			continue
		}
		pos := 0
		for pos < len(n.queue[key]) && n.queue[key][pos].conv {
			pos++
		}
		q := append([]naiveReq{}, n.queue[key][:pos]...)
		q = append(q, naiveReq{txn: txn, mode: C, conv: true})
		n.queue[key] = append(q, n.queue[key][pos:]...)
		tx.state = Committing
		if n.deadlocked(txn) {
			n.abortTxn(txn, &res.Events)
			res.Deadlock = true
			return res
		}
	}
	if tx.state == Active {
		n.finishCommit(txn, pending, &res.Events)
		n.drain(pending, &res.Events)
	}
	return res
}

func (n *naive) abort(txn int) Result {
	tx, ok := n.txns[txn]
	if !ok {
		return rejected(RejectNoSuchTxn)
	}
	if tx.state != Active && tx.state != Waiting && tx.state != Committing {
		return rejected(RejectBadState)
	}
	res := Result{OK: true}
	n.abortTxn(txn, &res.Events)
	return res
}
