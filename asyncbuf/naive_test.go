package asyncbuf

// 朴素参照模型（naive oracle）。
//
// 它与生产实现刻意采用不同的内部结构：不删除条目，而是给每条记录维护
// emitted 标记，每次状态变更后做全局不动点（fixpoint）扫描，直到没有
// 新条目可以输出。两种模式的“可输出”判定都是局部、显然正确的：
//
//   Ordered：第一条未输出条目若为水位线或已完成元素即可输出，否则停。
//   Unordered：在“第一条未输出水位线”之前，取 doneSeq 最小的已完成元素；
//              若没有，则当第一条未输出条目恰为水位线时输出水位线。
//
// 若生产实现（增量删除式队列）与本模型在同一步后逐条产出一致，
// 即可认为两种实现相互印证。

type nEntry struct {
	isWM      bool
	id        string
	value     any
	wm        int64
	completed bool
	doneSeq   int64
	emitted   bool
	index     int
}

type naiveBuffer struct {
	mode     Mode
	capacity int

	entries  []*nEntry
	byID     map[string]*nEntry
	occupied int

	lastWM int64
	hasWM  bool

	nextSeq   int64
	nextIndex int

	outputs []Output
}

func newNaive(mode Mode, capacity int) *naiveBuffer {
	return &naiveBuffer{
		mode:     mode,
		capacity: capacity,
		byID:     make(map[string]*nEntry),
	}
}

func (n *naiveBuffer) snapshot() (occupied, pending, buffered int) {
	pending = 0
	for _, e := range n.entries {
		if !e.emitted {
			pending++
		}
	}
	return n.occupied, pending, len(n.outputs)
}

func (n *naiveBuffer) Submit(id string, value any) error {
	if id == "" {
		return &Error{Kind: ErrEmptyID}
	}
	if _, ok := n.byID[id]; ok {
		return &Error{Kind: ErrDuplicateID, ID: id}
	}
	if n.occupied >= n.capacity {
		return &Error{Kind: ErrCapacityFull, ID: id}
	}
	e := &nEntry{id: id, value: value, index: n.nextIndex}
	n.nextIndex++
	n.entries = append(n.entries, e)
	n.byID[id] = e
	n.occupied++
	n.advance()
	return nil
}

func (n *naiveBuffer) SubmitWatermark(wm int64) error {
	if n.hasWM && wm <= n.lastWM {
		return &Error{Kind: ErrWatermarkNotMonotonic, Watermark: wm}
	}
	e := &nEntry{isWM: true, wm: wm, index: n.nextIndex}
	n.nextIndex++
	n.entries = append(n.entries, e)
	n.lastWM = wm
	n.hasWM = true
	n.advance()
	return nil
}

func (n *naiveBuffer) Complete(id string) error {
	e, ok := n.byID[id]
	if !ok {
		return &Error{Kind: ErrUnknownID, ID: id}
	}
	if e.completed {
		return &Error{Kind: ErrAlreadyCompleted, ID: id}
	}
	e.completed = true
	e.doneSeq = n.nextSeq
	n.nextSeq++
	n.advance()
	return nil
}

func (n *naiveBuffer) Drain() []Output {
	if len(n.outputs) == 0 {
		return nil
	}
	out := n.outputs
	n.outputs = nil
	return out
}

func (n *naiveBuffer) advance() {
	if n.mode == Ordered {
		n.advanceOrdered()
		return
	}
	n.advanceUnordered()
}

func (n *naiveBuffer) advanceOrdered() {
	for {
		var head *nEntry
		for _, e := range n.entries {
			if !e.emitted {
				head = e
				break
			}
		}
		if head == nil {
			return
		}
		if head.isWM || head.completed {
			n.emit(head)
			continue
		}
		return
	}
}

func (n *naiveBuffer) advanceUnordered() {
	for {
		// 1) 开放段（第一条未输出水位线之前）中 doneSeq 最小的已完成元素。
		var cand *nEntry
		for _, e := range n.entries {
			if e.emitted {
				continue
			}
			if e.isWM {
				break
			}
			if e.completed && (cand == nil || e.doneSeq < cand.doneSeq) {
				cand = e
			}
		}
		if cand != nil {
			n.emit(cand)
			continue
		}
		// 2) 段已排空：若第一条未输出条目是水位线则输出（级联）。
		var first *nEntry
		for _, e := range n.entries {
			if !e.emitted {
				first = e
				break
			}
		}
		if first != nil && first.isWM {
			n.emit(first)
			continue
		}
		return
	}
}

func (n *naiveBuffer) emit(e *nEntry) {
	e.emitted = true
	if e.isWM {
		n.outputs = append(n.outputs, Output{
			Kind: WatermarkOutput, Watermark: e.wm, Index: e.index,
		})
		return
	}
	delete(n.byID, e.id)
	n.occupied--
	n.outputs = append(n.outputs, Output{
		Kind: ElementOutput, ID: e.id, Value: e.value, Index: e.index,
	})
}
