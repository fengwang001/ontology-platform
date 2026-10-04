package sched

// naiveSim 是按题目规则逐行写成的朴素参考模型：
// 队列用切片线性扫描（与生产实现的 O(1) 链表+索引刻意不同），
// 组表直接遍历。每次操作返回 (id, err)，并在内部维护完整状态。
type naiveSim struct {
	c, q   int
	nextID int
	runs   map[int]*nRun
	queue  []int // FIFO，存运行 id
	busy   int   // Running + Cancelling 数
	log    []string
}

type nRun struct {
	group     string
	state     groupState
	protected bool
}

type groupState int

const (
	nWaiting groupState = iota
	nRunning
	nCancelling
	nPending
	nSucceeded
	nFailed
	nCancelled
	nSuperseded
)

func newNaive(c, q int) *naiveSim {
	return &naiveSim{c: c, q: q, nextID: 1, runs: map[int]*nRun{}}
}

// holderPending 线性查找某组占位者与 Pending（空组名永远无占位者）。
func (n *naiveSim) holderPending(g string) (holder, pending int) {
	holder, pending = -1, -1
	if g == "" {
		return
	}
	for id, r := range n.runs {
		if r.group != g {
			continue
		}
		switch r.state {
		case nWaiting, nRunning, nCancelling:
			holder = id
		case nPending:
			pending = id
		}
	}
	return
}

func (n *naiveSim) enqueue(id int) { n.queue = append(n.queue, id) }

func (n *naiveSim) dequeue(id int) {
	for i, x := range n.queue {
		if x == id {
			n.queue = append(n.queue[:i], n.queue[i+1:]...)
			return
		}
	}
}

// allocate 操作末尾：有空位且队列非空则队首 Running。
func (n *naiveSim) allocate() {
	for n.busy < n.c && len(n.queue) > 0 {
		id := n.queue[0]
		n.queue = n.queue[1:]
		n.runs[id].state = nRunning
		n.busy++
	}
}

// promote 本组 Pending 晋升占位者并入队尾（不受 q 约束）。
func (n *naiveSim) promote(g string) {
	_, pending := n.holderPending(g)
	if pending < 0 {
		return
	}
	n.runs[pending].state = nWaiting
	n.enqueue(pending)
}

func (n *naiveSim) submit(g string, cancel, prot bool) (int, error) {
	if len(g) > 64 {
		return 0, ErrInvalid
	}
	l0 := len(n.queue)
	holder, pending := n.holderPending(g)

	// 先在一份「影子」变更上模拟，得到 L1 后再决定提交或整体回滚。
	snapQueue := append([]int(nil), n.queue...)
	snapState := map[int]groupState{}
	for id, r := range n.runs {
		snapState[id] = r.state
	}
	snapBusy := n.busy
	snapNext := n.nextID

	id := n.nextID
	n.nextID++
	n.runs[id] = &nRun{group: g, protected: prot}
	if holder < 0 {
		n.runs[id].state = nWaiting
		n.enqueue(id)
		n.allocate()
	} else {
		if pending >= 0 {
			n.runs[pending].state = nSuperseded
		}
		if !cancel {
			n.runs[id].state = nPending
			n.allocate()
		} else {
			switch n.runs[holder].state {
			case nWaiting:
				n.runs[holder].state = nCancelled
				n.dequeue(holder)
				n.runs[id].state = nWaiting
				n.enqueue(id)
				n.allocate()
			case nRunning:
				if n.runs[holder].protected {
					n.runs[id].state = nPending
				} else {
					n.runs[holder].state = nCancelling
					n.runs[id].state = nPending
				}
				n.allocate()
			default: // nCancelling
				n.runs[id].state = nPending
				n.allocate()
			}
		}
	}
	l1 := len(n.queue)
	if l1 > l0 && l1 > n.q {
		// 队列已满：整体回滚（含不顶替原 Pending、不占号）。
		n.queue = snapQueue
		for sid, st := range snapState {
			n.runs[sid].state = st
		}
		delete(n.runs, id)
		n.busy = snapBusy
		n.nextID = snapNext
		return 0, ErrQueueFull
	}
	return id, nil
}

func (n *naiveSim) finish(id int, ok bool) error {
	if id <= 0 {
		return ErrInvalid
	}
	r := n.runs[id]
	if r == nil {
		return ErrNotFound
	}
	switch r.state {
	case nRunning:
		if ok {
			r.state = nSucceeded
		} else {
			r.state = nFailed
		}
	case nCancelling:
		r.state = nCancelled
	default:
		return ErrState
	}
	n.busy--
	g := r.group
	n.promote(g)
	n.allocate()
	return nil
}

func (n *naiveSim) ackCancel(id int) error {
	if id <= 0 {
		return ErrInvalid
	}
	r := n.runs[id]
	if r == nil {
		return ErrNotFound
	}
	if r.state != nCancelling {
		return ErrState
	}
	r.state = nCancelled
	n.busy--
	n.promote(r.group)
	n.allocate()
	return nil
}

func (n *naiveSim) cancel(id int) error {
	if id <= 0 {
		return ErrInvalid
	}
	r := n.runs[id]
	if r == nil {
		return ErrNotFound
	}
	switch r.state {
	case nPending:
		r.state = nCancelled
		n.allocate()
	case nWaiting:
		r.state = nCancelled
		n.dequeue(id)
		n.allocate()
	case nRunning:
		r.state = nCancelling
		n.allocate()
	default:
		return ErrState
	}
	return nil
}

func (n *naiveSim) do(o op) (int, error) {
	switch o.kind {
	case opSubmit:
		return n.submit(string(o.group), o.cancel, o.prot)
	case opFinish:
		return 0, n.finish(o.id, true)
	case opFinishFail:
		return 0, n.finish(o.id, false)
	case opAckCancel:
		return 0, n.ackCancel(o.id)
	default:
		return 0, n.cancel(o.id)
	}
}

func (n *naiveSim) stateOf(id int) (groupState, bool) {
	r, ok := n.runs[id]
	if !ok {
		return 0, false
	}
	return r.state, true
}
