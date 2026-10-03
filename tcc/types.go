// Package tcc 管理 TCC 分支记录、到期堆与全局单调时钟。
package tcc

// State 为分支终态标记。
type State int

const (
	StateTried State = iota
	StateConfirmed
	StateCancelled
)

// CancelReason 是 Cancelled 记录的原因。
type CancelReason int

const (
	CancelEmpty    CancelReason = iota // 空回滚标记
	CancelExplicit                     // 显式 Cancel
	CancelExpired                      // 到期自动取消
)

func (r CancelReason) String() string {
	switch r {
	case CancelEmpty:
		return "Empty"
	case CancelExplicit:
		return "Cancel"
	default:
		return "Expired"
	}
}

// Branch 是 (xid,br) 对应的分支记录。
type Branch struct {
	Acct     []byte
	Amount   int64
	Deadline int64 // 仅 Tried 有效：now+ttl
	State    State
	Reason   CancelReason
}

type branchKey struct {
	xid string
	br  string
}

// heapEntry 为到期堆中的一项；index 支持从堆中删除已终结的分支。
type heapEntry struct {
	key      branchKey
	deadline int64
	index    int
}

// expiryHeap 是按 deadline 排序的最小索引堆。
type expiryHeap struct {
	items []*heapEntry
}

func newExpiryHeap() *expiryHeap { return &expiryHeap{} }

func (h *expiryHeap) len() int { return len(h.items) }

func (h *expiryHeap) less(i, j int) bool {
	if h.items[i].deadline != h.items[j].deadline {
		return h.items[i].deadline < h.items[j].deadline
	}
	if h.items[i].key.xid != h.items[j].key.xid {
		return h.items[i].key.xid < h.items[j].key.xid
	}
	return h.items[i].key.br < h.items[j].key.br
}

func (h *expiryHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].index = i
	h.items[j].index = j
}

func (h *expiryHeap) push(e *heapEntry) {
	e.index = len(h.items)
	h.items = append(h.items, e)
	h.up(len(h.items) - 1)
}

func (h *expiryHeap) pop() *heapEntry {
	n := len(h.items)
	e := h.items[0]
	h.swap(0, n-1)
	h.items = h.items[:n-1]
	if n > 1 {
		h.down(0)
	}
	return e
}

// remove 按条目在堆中的位置删除（Confirm/Cancel 终结时调用）。
func (h *expiryHeap) remove(e *heapEntry) {
	i := e.index
	n := len(h.items)
	if i < 0 || i >= n || h.items[i] != e {
		return
	}
	h.swap(i, n-1)
	h.items = h.items[:n-1]
	if i < n-1 {
		h.down(i)
		h.up(i)
	}
}

func (h *expiryHeap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *expiryHeap) down(i int) {
	for {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(h.items) && h.less(l, m) {
			m = l
		}
		if r < len(h.items) && h.less(r, m) {
			m = r
		}
		if m == i {
			return
		}
		h.swap(i, m)
		i = m
	}
}
