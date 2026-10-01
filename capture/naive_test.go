package capture

// naiveVar 是朴素参考模型中的捕获变量：把规则一步步直白地写下来，
// 不做任何与实现共享的抽象，以便与 Manager 独立对照。
type naiveVar struct {
	id      int
	slot    int
	holders int
	closed  bool
	storage int
}

// naiveModel 是按题目规则逐条实现的顺序模拟器。
type naiveModel struct {
	stack  []int
	open   map[int]*naiveVar // 槽 -> 该槽当前唯一的开放变量
	vars   map[int]*naiveVar
	nextID int
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		open:   map[int]*naiveVar{},
		vars:   map[int]*naiveVar{},
		nextID: 1,
	}
}

type naiveResult struct {
	val    int
	ok     bool
	reason Reason // ok == false 时有效
}

func (n *naiveModel) top() int { return len(n.stack) }

func (n *naiveModel) push(v int) int {
	slot := len(n.stack)
	n.stack = append(n.stack, v)
	return slot
}

func (n *naiveModel) capture(slot int) naiveResult {
	if slot < 0 || slot >= len(n.stack) {
		return naiveResult{reason: ReasonCaptureSlotAboveTop}
	}
	if v := n.open[slot]; v != nil {
		v.holders++
		return naiveResult{val: v.id, ok: true}
	}
	v := &naiveVar{id: n.nextID, slot: slot, holders: 1}
	n.nextID++
	n.open[slot] = v
	n.vars[v.id] = v
	return naiveResult{val: v.id, ok: true}
}

func (n *naiveModel) slotGet(slot int) naiveResult {
	if slot < 0 || slot >= len(n.stack) {
		return naiveResult{reason: ReasonSlotOutOfRange}
	}
	return naiveResult{val: n.stack[slot], ok: true}
}

func (n *naiveModel) slotSet(slot, v int) naiveResult {
	if slot < 0 || slot >= len(n.stack) {
		return naiveResult{reason: ReasonSlotOutOfRange}
	}
	n.stack[slot] = v
	return naiveResult{ok: true}
}

func (n *naiveModel) handleGet(h int) naiveResult {
	v := n.vars[h]
	if v == nil {
		return naiveResult{reason: ReasonHandleNotFound}
	}
	if v.holders == 0 {
		return naiveResult{reason: ReasonHandleReleased}
	}
	if v.closed {
		return naiveResult{val: v.storage, ok: true}
	}
	return naiveResult{val: n.stack[v.slot], ok: true}
}

func (n *naiveModel) handleSet(h, v int) naiveResult {
	x := n.vars[h]
	if x == nil {
		return naiveResult{reason: ReasonHandleNotFound}
	}
	if x.holders == 0 {
		return naiveResult{reason: ReasonHandleReleased}
	}
	if x.closed {
		x.storage = v
	} else {
		n.stack[x.slot] = v
	}
	return naiveResult{ok: true}

}

func (n *naiveModel) closeFrom(level int) naiveResult {
	if level < 0 || level > len(n.stack) {
		return naiveResult{reason: ReasonCloseLevelOutOfRange}
	}
	for slot, v := range n.open {
		if slot >= level {
			v.storage = n.stack[slot]
			v.closed = true
			delete(n.open, slot)
		}
	}
	n.stack = n.stack[:level]
	return naiveResult{ok: true}
}

func (n *naiveModel) release(h int) naiveResult {
	v := n.vars[h]
	if v == nil {
		return naiveResult{reason: ReasonHandleNotFound}
	}
	if v.holders == 0 {
		return naiveResult{reason: ReasonHandleReleased}
	}
	v.holders--
	if v.holders == 0 && !v.closed {
		delete(n.open, v.slot)
	}
	return naiveResult{ok: true}
}
