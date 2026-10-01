package mvcc

import "fmt"

// naive is an independent oracle implementation used only by tests. It keeps
// an append-only log of accepted mutating operations and recomputes the full
// state by replaying the log from scratch for every validation or query.

type naiveOp struct {
	kind   string
	x, p   int
	t      string
	c      int
	snapID int
}

type naiveTx struct {
	parent int
	state  TxState
}

type naiveTuple struct {
	xmin, cmin int
	xmax, cmax int
	hasXmax    bool
}

type naiveState struct {
	txs      map[int]naiveTx
	tuples   map[string]naiveTuple
	snaps    map[int]map[int]bool
	treeMax  map[int]int
	treeHas  map[int]bool
	nextSnap int
}

type naive struct {
	log []naiveOp
}

func newNaive() *naive { return &naive{} }

func (n *naive) replay() *naiveState {
	st := &naiveState{
		txs:     make(map[int]naiveTx),
		tuples:  make(map[string]naiveTuple),
		snaps:   make(map[int]map[int]bool),
		treeMax: make(map[int]int),
		treeHas: make(map[int]bool),
	}
	st.nextSnap = 1
	for _, op := range n.log {
		switch op.kind {
		case "begin":
			st.txs[op.x] = naiveTx{state: Running}
		case "beginsub":
			st.txs[op.x] = naiveTx{parent: op.p, state: Running}
		case "commitsub":
			tx := st.txs[op.x]
			tx.state = SubCommitted
			st.txs[op.x] = tx
		case "commit":
			tx := st.txs[op.x]
			tx.state = Committed
			st.txs[op.x] = tx
		case "abort":
			abortSet := map[int]bool{op.x: true}
			changed := true
			for changed {
				changed = false
				for id, tx := range st.txs {
					if tx.parent != 0 && abortSet[tx.parent] && !abortSet[id] {
						abortSet[id] = true
						changed = true
					}
				}
			}
			for id := range abortSet {
				tx := st.txs[id]
				tx.state = Aborted
				st.txs[id] = tx
			}
		case "snapshot":
			committed := make(map[int]bool)
			for id, tx := range st.txs {
				if tx.parent == 0 && tx.state == Committed {
					committed[id] = true
				}
			}
			st.snaps[op.snapID] = committed
			st.nextSnap = op.snapID + 1
		case "insert":
			st.tuples[op.t] = naiveTuple{xmin: op.x, cmin: op.c}
			root := st.rootOf(op.x)
			st.treeMax[root] = op.c
			st.treeHas[root] = true
		case "delete":
			tv := st.tuples[op.t]
			tv.xmax = op.x
			tv.cmax = op.c
			tv.hasXmax = true
			st.tuples[op.t] = tv
			root := st.rootOf(op.x)
			st.treeMax[root] = op.c
			st.treeHas[root] = true
		}
	}
	return st
}

func (st *naiveState) rootOf(x int) int {
	for st.txs[x].parent != 0 {
		x = st.txs[x].parent
	}
	return x
}

func (st *naiveState) effective(x int) EffectiveStatus {
	tx := st.txs[x]
	if tx.state == Aborted {
		return EffAborted
	}
	if tx.state == SubCommitted || tx.state == Committed {
		if st.txs[st.rootOf(x)].state == Committed {
			return EffCommitted
		}
	}
	return EffActive
}

func (st *naiveState) hasRunningDescendant(x int) bool {
	for id, tx := range st.txs {
		if tx.parent == x {
			if tx.state == Running || st.hasRunningDescendant(id) {
				return true
			}
		}
	}
	return false
}

func (n *naive) begin(x int) error {
	st := n.replay()
	if x <= 0 {
		return ErrTxIDNotPositive
	}
	if _, ok := st.txs[x]; ok {
		return ErrTxExists
	}
	n.log = append(n.log, naiveOp{kind: "begin", x: x})
	return nil
}

func (n *naive) beginSub(x, p int) error {
	st := n.replay()
	if x <= 0 {
		return ErrTxIDNotPositive
	}
	if _, ok := st.txs[x]; ok {
		return ErrTxExists
	}
	parent, ok := st.txs[p]
	if !ok {
		return ErrParentNotFound
	}
	if parent.state != Running {
		return ErrParentNotRunning
	}
	n.log = append(n.log, naiveOp{kind: "beginsub", x: x, p: p})
	return nil
}

func (n *naive) commitCheck(x int, wantSub bool) error {
	st := n.replay()
	tx, ok := st.txs[x]
	if !ok {
		return ErrTxNotFound
	}
	if tx.state != Running {
		return ErrTxNotRunning
	}
	if isSub := tx.parent != 0; isSub != wantSub {
		return ErrTxTypeMismatch
	}
	if st.hasRunningDescendant(x) {
		return ErrTxHasRunningDescendants
	}
	return nil
}

func (n *naive) commitSub(x int) error {
	if err := n.commitCheck(x, true); err != nil {
		return err
	}
	n.log = append(n.log, naiveOp{kind: "commitsub", x: x})
	return nil
}

func (n *naive) commit(x int) error {
	if err := n.commitCheck(x, false); err != nil {
		return err
	}
	n.log = append(n.log, naiveOp{kind: "commit", x: x})
	return nil
}

func (n *naive) abort(x int) error {
	st := n.replay()
	tx, ok := st.txs[x]
	if !ok {
		return ErrTxNotFound
	}
	if tx.state != Running {
		return ErrTxNotRunning
	}
	n.log = append(n.log, naiveOp{kind: "abort", x: x})
	return nil
}

func (n *naive) snapshot() int {
	st := n.replay()
	id := st.nextSnap
	n.log = append(n.log, naiveOp{kind: "snapshot", snapID: id})
	return id
}

func (n *naive) writeCheck(t string, x, c int) (*naiveState, error) {
	st := n.replay()
	tx, ok := st.txs[x]
	if !ok {
		return nil, ErrTxNotFound
	}
	if tx.state != Running {
		return nil, ErrTxNotRunning
	}
	if c < 0 {
		return nil, ErrNegativeCommandID
	}
	root := st.rootOf(x)
	if st.treeHas[root] && c < st.treeMax[root] {
		return nil, ErrCommandIDTooSmall
	}
	return st, nil
}

func (n *naive) insert(t string, x, c int) error {
	st, err := n.writeCheck(t, x, c)
	if err != nil {
		return err
	}
	if _, ok := st.tuples[t]; ok {
		return ErrTupleExists
	}
	n.log = append(n.log, naiveOp{kind: "insert", t: t, x: x, c: c})
	return nil
}

func (n *naive) delete(t string, x, c int) error {
	st, err := n.writeCheck(t, x, c)
	if err != nil {
		return err
	}
	tv, ok := st.tuples[t]
	if !ok {
		return ErrTupleNotFound
	}
	if tv.hasXmax && st.effective(tv.xmax) != EffAborted {
		return ErrTupleDeleted
	}
	n.log = append(n.log, naiveOp{kind: "delete", t: t, x: x, c: c})
	return nil
}

func (n *naive) visible(t string, x, c, snap int) (bool, string, error) {
	st := n.replay()
	tx, ok := st.txs[x]
	if !ok {
		return false, "", ErrTxNotFound
	}
	if tx.state != Running {
		return false, "", ErrTxNotRunning
	}
	if c < 0 {
		return false, "", ErrNegativeCommandID
	}
	committed, ok := st.snaps[snap]
	if !ok {
		return false, "", ErrUnknownSnapshot
	}
	tv, ok := st.tuples[t]
	if !ok {
		return false, "", ErrTupleNotFound
	}
	seen := func(y, cy int) (bool, string) {
		yRoot := st.rootOf(y)
		xRoot := st.rootOf(x)
		eff := st.effective(y)
		if yRoot == xRoot {
			return eff != EffAborted && cy < c,
				fmt.Sprintf("same root %d, effective=%s, cmd %d < %d", yRoot, eff, cy, c)
		}
		inSnap := committed[yRoot]
		return eff == EffCommitted && inSnap,
			fmt.Sprintf("different root %d, effective=%s, root in snapshot=%v", yRoot, eff, inSnap)
	}
	xminSeen, xminWhy := seen(tv.xmin, tv.cmin)
	xmaxSeen := false
	xmaxWhy := "no delete marker"
	if tv.hasXmax {
		xmaxSeen, xmaxWhy = seen(tv.xmax, tv.cmax)
	}
	vis := xminSeen && !xmaxSeen
	why := fmt.Sprintf("xmin=(tx %d,cmd %d) seen=%v [%s]; xmax seen=%v [%s] => visible=%v",
		tv.xmin, tv.cmin, xminSeen, xminWhy, xmaxSeen, xmaxWhy, vis)
	return vis, why, nil
}
