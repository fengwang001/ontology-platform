// Package hist 维护多节点事件历史：消息登记、(时间戳,节点ID) 全序与 happens-before 判定。仅依赖 lc。
package hist

import (
	"errors"
	"math/bits"
	"sync"

	"ontology/lc"
)

var ErrInvalidNode, ErrMsgNotFound, ErrMsgRecvTwice, ErrEventLimit = errors.New("hist: node id out of range"), errors.New("hist: message does not exist"), errors.New("hist: message already received"), errors.New("hist: event count exceeds maxEvents")

type Event struct{ ID, Node, Msg, TS int }
type msg struct {
	to, sendID int
	ts         int
	received   bool
}
type History struct {
	mu             sync.RWMutex
	maxEv, lastCmp int
	clocks         []*lc.Clock
	last           []int // 各节点最近事件 ID
	order          []Event
	succ           [][]int // happens-before 直接后继边，下标事件 ID-1
	msgs           map[int]msg
}

func (h *History) valid(x int) bool { return 1 <= x && x <= len(h.clocks) }
func (h *History) full() bool       { return len(h.succ) >= h.maxEv }
func New(n, maxEvents int) (*History, error) {
	if n <= 0 || maxEvents <= 0 {
		return nil, ErrInvalidNode
	}
	h := &History{maxEv: maxEvents, clocks: make([]*lc.Clock, n), last: make([]int, n), msgs: map[int]msg{}}
	for i := range h.clocks {
		h.clocks[i] = lc.New(i + 1)
	}
	return h, nil
}
func (h *History) commitLocked(node int, ts int64, mid int) int { // 分配 ID、挂同节点先后链并二分插入全序；调用前必须已完成校验
	id := len(h.succ) + 1
	h.succ = append(h.succ, nil)
	if p := h.last[node-1]; p != 0 {
		h.succ[p-1] = append(h.succ[p-1], id)
	}
	h.last[node-1] = id
	h.lastCmp = 0
	lo, hi := 0, len(h.order)
	for lo < hi {
		m := (lo + hi) / 2
		h.lastCmp++
		q := h.order[m]
		if lc.Less(lc.MakeKey(int64(q.TS), q.Node), lc.MakeKey(ts, node)) {
			lo = m + 1
		} else {
			hi = m
		}
	}
	h.order = append(h.order[:lo], append([]Event{{id, node, mid, int(ts)}}, h.order[lo:]...)...)
	return id
}
func (h *History) Local(node int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.valid(node) {
		return ErrInvalidNode
	}
	if h.full() {
		return ErrEventLimit
	}
	h.commitLocked(node, h.clocks[node-1].Local(), 0)
	return nil
}
func (h *History) Send(from, to int) (mid int, ts int64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.valid(from) || !h.valid(to) {
		return 0, 0, ErrInvalidNode
	}
	if h.full() {
		return 0, 0, ErrEventLimit
	}
	mid = len(h.msgs) + 1
	ts = h.clocks[from-1].Send()
	id := h.commitLocked(from, ts, mid)
	h.msgs[mid] = msg{to: to, ts: int(ts), sendID: id}
	return mid, ts, nil
}
func (h *History) Recv(mid int) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.msgs[mid] // 不存在/已接收/超限：均在任何状态变更之前拒绝
	if !ok {
		return 0, ErrMsgNotFound
	}
	if m.received {
		return 0, ErrMsgRecvTwice
	}
	if h.full() {
		return 0, ErrEventLimit
	}
	ts := h.clocks[m.to-1].Recv(int64(m.ts))
	m.received = true
	h.msgs[mid] = m
	id := h.commitLocked(m.to, ts, mid)
	h.succ[m.sendID-1] = append(h.succ[m.sendID-1], id)
	return ts, nil
}
func (h *History) Order() []Event {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]Event(nil), h.order...)
}
func (h *History) HappensBefore(a, b int) bool { // a→b；严格偏序，a==b 或越界为 false
	h.mu.RLock()
	defer h.mu.RUnlock()
	if a == b || a < 1 || b < 1 || a > len(h.succ) || b > len(h.succ) {
		return false
	}
	seen, st := make([]bool, len(h.succ)+1), append([]int(nil), h.succ[a-1]...)
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if x == b {
			return true
		}
		if !seen[x] {
			seen[x] = true
			st = append(st, h.succ[x-1]...)
		}
	}
	return false
}
func LogInsertBoundHolds(m int) bool { // 造 m 事件后中段插一事件，判比较数≤2⌈log₂(m+1)⌉+2；不暴露计数器
	h, _ := New(3, m+2)
	for i := 0; i < m/5; i++ {
		for _, nd := range [5]int{1, 2, 3, 2, 3} {
			_ = h.Local(nd)
		}
	}
	_ = h.Local(1) // 新键 (m/5+1,1) 落在全序中段
	return h.lastCmp >= 1 && h.lastCmp <= 2*bits.Len(uint(m))+2
}
