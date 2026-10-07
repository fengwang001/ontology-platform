package pharmacy

type eventKind int8

const (
	evResExpiry eventKind = iota // 预留失效
	evRxExpiry                   // 处方过期
)

// event 是时间堆中的事件。同一时刻的事件按创建序号 seq 依次处理，
// 保证重放顺序完全确定。
type event struct {
	time  int
	seq   int
	kind  eventKind
	res   *Reservation
	rx    *Prescription
	index int // 在堆数组中的位置，用于 O(log n) 删除（回滚用）
}

type eventHeap struct {
	s []*event
}

func (h *eventHeap) len() int { return len(h.s) }

func (h *eventHeap) top() *event {
	if len(h.s) == 0 {
		return nil
	}
	return h.s[0]
}

func lessEvent(a, b *event) bool {
	if a.time != b.time {
		return a.time < b.time
	}
	return a.seq < b.seq
}

func (h *eventHeap) swap(i, j int) {
	h.s[i], h.s[j] = h.s[j], h.s[i]
	h.s[i].index = i
	h.s[j].index = j
}

func (h *eventHeap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !lessEvent(h.s[i], h.s[p]) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *eventHeap) down(i int) {
	for {
		l := 2*i + 1
		if l >= len(h.s) {
			return
		}
		m := l
		if r := l + 1; r < len(h.s) && lessEvent(h.s[r], h.s[l]) {
			m = r
		}
		if !lessEvent(h.s[m], h.s[i]) {
			return
		}
		h.swap(i, m)
		i = m
	}
}

func (h *eventHeap) push(ev *event) {
	ev.index = len(h.s)
	h.s = append(h.s, ev)
	h.up(ev.index)
}

func (h *eventHeap) pop() *event {
	top := h.s[0]
	last := len(h.s) - 1
	h.swap(0, last)
	h.s = h.s[:last]
	if last > 0 {
		h.down(0)
	}
	top.index = -1
	return top
}

// remove 删除堆中任意事件（仅回滚路径使用）。
func (h *eventHeap) remove(ev *event) {
	i := ev.index
	if i < 0 || i >= len(h.s) || h.s[i] != ev {
		return
	}
	last := len(h.s) - 1
	h.swap(i, last)
	h.s = h.s[:last]
	if i < last {
		h.down(i)
		h.up(i)
	}
	ev.index = -1
}
