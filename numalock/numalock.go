// Package numalock implements a NUMA-aware phase-fair reader-writer
// group lock: readers enter in batches, writers form per-node groups and
// hand off locally a bounded number of times.
package numalock

import (
	"errors"
	"strconv"
	"sync"
)

// Phase is the global lock phase.
type Phase uint8

const (
	PhaseIdle Phase = iota
	PhaseRead
	PhaseWrite
)

func (p Phase) String() string {
	switch p {
	case PhaseRead:
		return "read"
	case PhaseWrite:
		return "write"
	default:
		return "idle"
	}
}

// ThreadState is the per-thread state.
type ThreadState uint8

const (
	StateIdle ThreadState = iota
	StateReadWait
	StateReadHold
	StateWriteWait
	StateWriteHold
)

func (s ThreadState) String() string {
	switch s {
	case StateReadWait:
		return "read-wait"
	case StateReadHold:
		return "read-hold"
	case StateWriteWait:
		return "write-wait"
	case StateWriteHold:
		return "write-hold"
	default:
		return "idle"
	}
}

// Result reports whether a call was accepted, which threads it granted and
// the exact decision basis.
type Result struct {
	OK      bool
	Granted []int
	Reason  string
}

// Snapshot is an immutable copy of the whole lock state.
type Snapshot struct {
	Phase  Phase
	R      []int
	W      int
	Gown   int
	P      int
	Qr     []int
	Qw     [][]int
	G      []int
	States []ThreadState
}

// ErrBadConfig is returned when constructor arguments are invalid.
var ErrBadConfig = errors.New("numalock: bad configuration")

// Lock is the NUMA-aware phase-fair reader-writer group lock.
// All queues are intrusive, indexed by thread id / node id, so enqueue,
// dequeue and arbitrary removal each touch a constant number of links.
type Lock struct {
	mu sync.Mutex

	m      int
	t      int
	b      int
	nodeOf []int

	phase  Phase
	states []ThreadState

	r    []int // read holders; swap-delete set, rebuilt deterministically on downgrade
	rHas []bool
	w    int
	gown int
	p    int

	// Global reader FIFO Qr.
	qrHead, qrTail int
	qrPrev, qrNext []int

	// Per-node writer FIFOs Qw[n].
	qwHead, qwTail []int
	wPrev, wNext   []int

	// Node turn FIFO G.
	gHead, gTail int
	gPrev, gNext []int
}

// New constructs a lock: m nodes (1..8), t threads (1..32), nodeOf[tid] is
// the node of thread tid (0..m-1), and b is the local-handoff bound (1..16).
// Any invalid argument rejects the whole configuration.
func New(m, t int, nodeOf []int, b int) (*Lock, error) {
	if m < 1 || m > 8 || t < 1 || t > 32 || b < 1 || b > 16 || len(nodeOf) != t {
		return nil, ErrBadConfig
	}
	for _, n := range nodeOf {
		if n < 0 || n >= m {
			return nil, ErrBadConfig
		}
	}
	l := &Lock{
		m:      m,
		t:      t,
		b:      b,
		nodeOf: append([]int(nil), nodeOf...),
		states: make([]ThreadState, t),
		rHas:   make([]bool, t),
		w:      -1,
		gown:   -1,
		qrHead: -1,
		qrTail: -1,
		qrPrev: make([]int, t),
		qrNext: make([]int, t),
		qwHead: make([]int, m),
		qwTail: make([]int, m),
		wPrev:  make([]int, t),
		wNext:  make([]int, t),
		gHead:  -1,
		gTail:  -1,
		gPrev:  make([]int, m),
		gNext:  make([]int, m),
	}
	for tid := 0; tid < t; tid++ {
		l.qrPrev[tid], l.qrNext[tid] = -1, -1
		l.wPrev[tid], l.wNext[tid] = -1, -1
	}
	for n := 0; n < m; n++ {
		l.qwHead[n], l.qwTail[n] = -1, -1
		l.gPrev[n], l.gNext[n] = -1, -1
	}
	return l, nil
}

func itoa(x int) string { return strconv.Itoa(x) }

func rejectState(want string) Result {
	return Result{OK: false, Reason: "拒绝: 状态不符（须" + want + "）"}
}

func (l *Lock) check(tid int) (Result, bool) {
	if tid < 0 || tid >= l.t {
		return Result{OK: false, Reason: "拒绝: 线程号越界"}, false
	}
	return Result{}, true
}

// badThread returns the rejection result and true when tid is out of range.
func (l *Lock) badThread(tid int) (Result, bool) {
	if tid < 0 || tid >= l.t {
		return Result{OK: false, Reason: "拒绝: 线程号越界"}, true
	}
	return Result{}, false
}

// ---- Qr helpers (caller holds l.mu) ----

func (l *Lock) qrPush(tid int) {
	l.qrPrev[tid] = l.qrTail
	l.qrNext[tid] = -1
	if l.qrTail != -1 {
		l.qrNext[l.qrTail] = tid
	} else {
		l.qrHead = tid
	}
	l.qrTail = tid
}

func (l *Lock) qrRemove(tid int) {
	if l.qrPrev[tid] != -1 {
		l.qrNext[l.qrPrev[tid]] = l.qrNext[tid]
	} else {
		l.qrHead = l.qrNext[tid]
	}
	if l.qrNext[tid] != -1 {
		l.qrPrev[l.qrNext[tid]] = l.qrPrev[tid]
	} else {
		l.qrTail = l.qrPrev[tid]
	}
	l.qrPrev[tid], l.qrNext[tid] = -1, -1
}

// qrDrain grants all waiting readers in enqueue order and empties Qr.
func (l *Lock) qrDrain() []int {
	out := make([]int, 0)
	for tid := l.qrHead; tid != -1; tid = l.qrNext[tid] {
		out = append(out, tid)
	}
	for _, tid := range out {
		l.qrPrev[tid], l.qrNext[tid] = -1, -1
	}
	l.qrHead, l.qrTail = -1, -1
	return out
}

func (l *Lock) qrEmpty() bool { return l.qrHead == -1 }

// ---- Qw helpers ----

func (l *Lock) wPush(n, tid int) {
	l.wPrev[tid] = l.qwTail[n]
	l.wNext[tid] = -1
	if l.qwTail[n] != -1 {
		l.wNext[l.qwTail[n]] = tid
	} else {
		l.qwHead[n] = tid
	}
	l.qwTail[n] = tid
}

func (l *Lock) wPopFront(n int) int {
	tid := l.qwHead[n]
	if tid == -1 {
		return -1
	}
	l.qwHead[n] = l.wNext[tid]
	if l.qwHead[n] != -1 {
		l.wPrev[l.qwHead[n]] = -1
	} else {
		l.qwTail[n] = -1
	}
	l.wPrev[tid], l.wNext[tid] = -1, -1
	return tid
}

func (l *Lock) wRemove(n, tid int) {
	if l.wPrev[tid] != -1 {
		l.wNext[l.wPrev[tid]] = l.wNext[tid]
	} else {
		l.qwHead[n] = l.wNext[tid]
	}
	if l.wNext[tid] != -1 {
		l.wPrev[l.wNext[tid]] = l.wPrev[tid]
	} else {
		l.qwTail[n] = l.wPrev[tid]
	}
	l.wPrev[tid], l.wNext[tid] = -1, -1
}

func (l *Lock) wEmpty(n int) bool { return l.qwHead[n] == -1 }

// ---- G helpers ----

func (l *Lock) gIn(n int) bool {
	return l.gHead == n || l.gPrev[n] != -1 || l.gNext[n] != -1
}

func (l *Lock) gPush(n int) {
	l.gPrev[n] = l.gTail
	l.gNext[n] = -1
	if l.gTail != -1 {
		l.gNext[l.gTail] = n
	} else {
		l.gHead = n
	}
	l.gTail = n
}

func (l *Lock) gPopFront() int {
	n := l.gHead
	if n == -1 {
		return -1
	}
	l.gHead = l.gNext[n]
	if l.gHead != -1 {
		l.gPrev[l.gHead] = -1
	} else {
		l.gTail = -1
	}
	l.gPrev[n], l.gNext[n] = -1, -1
	return n
}

func (l *Lock) gRemove(n int) {
	if l.gPrev[n] != -1 {
		l.gNext[l.gPrev[n]] = l.gNext[n]
	} else {
		l.gHead = l.gNext[n]
	}
	if l.gNext[n] != -1 {
		l.gPrev[l.gNext[n]] = l.gPrev[n]
	} else {
		l.gTail = l.gPrev[n]
	}
	l.gPrev[n], l.gNext[n] = -1, -1
}

func (l *Lock) gEmpty() bool { return l.gHead == -1 }

func (l *Lock) writerWaiting() bool {
	for n := 0; n < l.m; n++ {
		if !l.wEmpty(n) {
			return true
		}
	}
	return false
}

func (l *Lock) grantReaders(tids []int) {
	for _, tid := range tids {
		l.states[tid] = StateReadHold
		l.rHas[tid] = true
		l.r = append(l.r, tid)
	}
}

func (l *Lock) rRemove(tid int) {
	for i, x := range l.r {
		if x == tid {
			last := len(l.r) - 1
			l.r[i] = l.r[last]
			l.r = l.r[:last]
			break
		}
	}
	l.rHas[tid] = false
}

// startWrite implements 起写: pop G's head node and grant its head writer;
// with G empty the lock becomes idle.
func (l *Lock) startWrite() {
	if l.gEmpty() {
		l.phase = PhaseIdle
		l.w = -1
		l.gown = -1
		l.p = 0
		return
	}
	n := l.gPopFront()
	tid := l.wPopFront(n)
	l.phase = PhaseWrite
	l.w = tid
	l.gown = n
	l.p = 0
	l.states[tid] = StateWriteHold
}

// RLock grants in idle phase, or in read phase while no writer waits;
// otherwise the thread joins Qr.
func (l *Lock) RLock(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	if l.states[tid] != StateIdle {
		return rejectState("空闲")
	}
	switch {
	case l.phase == PhaseIdle:
		l.phase = PhaseRead
		l.grantReaders([]int{tid})
		return Result{OK: true, Granted: []int{tid}, Reason: "相位空闲: 置读相位并授予"}
	case l.phase == PhaseRead && !l.writerWaiting():
		l.grantReaders([]int{tid})
		return Result{OK: true, Granted: []int{tid}, Reason: "读相位且无写者等待: 随当前读批进入"}
	default:
		l.states[tid] = StateReadWait
		l.qrPush(tid)
		why := "写相位"
		if l.phase == PhaseRead {
			why = "读相位但有写者等待"
		}
		return Result{OK: true, Reason: why + ": 入 Qr 等待下一读批"}
	}
}

// RUnlock releases one read hold; when R empties, the read phase ends and
// 起写 runs.
func (l *Lock) RUnlock(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	if l.states[tid] != StateReadHold {
		return rejectState("读持有")
	}
	l.rRemove(tid)
	l.states[tid] = StateIdle
	if len(l.r) > 0 {
		return Result{OK: true, Reason: "R 仍非空: 读相位继续"}
	}
	l.startWrite()
	if l.phase == PhaseWrite {
		return Result{OK: true, Granted: []int{l.w},
			Reason: "R 空, 读相位结束; 起写取 G 队首节点 " + itoa(l.gown) +
				", 其队首写者 " + itoa(l.w) + " 被授予"}
	}
	return Result{OK: true, Reason: "R 空, 读相位结束; G 空, 相位空闲"}
}

// WLock grants in idle phase; otherwise the thread joins Qw[node] and, when
// the node is not gown and absent from G, appends the node to G.
func (l *Lock) WLock(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	if l.states[tid] != StateIdle {
		return rejectState("空闲")
	}
	n := l.nodeOf[tid]
	if l.phase == PhaseIdle {
		l.phase = PhaseWrite
		l.w = tid
		l.gown = n
		l.p = 0
		l.states[tid] = StateWriteHold
		return Result{OK: true, Granted: []int{tid},
			Reason: "相位空闲: 置写相位, w=" + itoa(tid) + ", gown=节点 " + itoa(n) + ", p=0"}
	}
	l.states[tid] = StateWriteWait
	l.wPush(n, tid)
	switch {
	case l.phase == PhaseWrite && n == l.gown:
		return Result{OK: true,
			Reason: "入 Qw[" + itoa(n) + "]; 写相位中本节点即 gown, 不入 G"}
	case l.gIn(n):
		return Result{OK: true,
			Reason: "入 Qw[" + itoa(n) + "]; 节点已在 G 中, 不重复入 G"}
	default:
		l.gPush(n)
		where := "读相位"
		if l.phase == PhaseWrite {
			where = "写相位且节点非 gown"
		}
		return Result{OK: true,
			Reason: where + ": 入 Qw[" + itoa(n) + "] 并把节点 " + itoa(n) + " 追加到 G 尾"}
	}
}

// WUnlock hands off to waiting readers first, then to a local writer within
// the bound, otherwise requeues gown and runs 起写.
func (l *Lock) WUnlock(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	if l.states[tid] != StateWriteHold {
		return rejectState("写持有")
	}
	gown := l.gown

	// Rule 1: waiting readers win over every writer handoff.
	if !l.qrEmpty() {
		l.states[tid] = StateIdle
		l.w = -1
		l.phase = PhaseRead
		granted := l.qrDrain()
		l.grantReaders(granted)
		tail := ""
		if !l.wEmpty(gown) {
			l.gPush(gown)
			tail = "; Qw[gown] 非空, gown(节点 " + itoa(gown) + ") 追加到 G 尾"
		}
		l.gown = -1
		l.p = 0
		return Result{OK: true, Granted: granted,
			Reason: "Qr 非空: Qr 全体按入队序授予, 相位置读, R=Qr" + tail}
	}

	// Rule 2: bounded local handoff. An empty G is never bounded: then the
	// only possible next writer is local, so handing off keeps G empty and
	// cannot starve another node.
	if !l.wEmpty(gown) && (l.gEmpty() || l.p < l.b) {
		l.states[tid] = StateIdle
		next := l.wPopFront(gown)
		l.w = next
		l.p++
		l.states[next] = StateWriteHold
		return Result{OK: true, Granted: []int{next},
			Reason: "Qr 空且 Qw[gown] 非空, " + handoffWhy(l.gEmpty()) +
				": 本地传给 " + itoa(next) + ", p=" + itoa(l.p)}
	}

	// Rule 3: bound reached (or G blocks the handoff): requeue gown, 起写.
	l.states[tid] = StateIdle
	l.w = -1
	l.gown = -1
	l.p = 0
	if !l.wEmpty(gown) {
		l.gPush(gown)
	}
	if l.gEmpty() {
		l.phase = PhaseIdle
		return Result{OK: true,
			Reason: "无读者且无写者可传: gown 队空, 相位空闲"}
	}
	m := l.gHead
	l.startWrite()
	return Result{OK: true, Granted: []int{l.w},
		Reason: "本地传递不可继续, gown(节点 " + itoa(gown) + ") 已重入 G 尾; 起写取节点 " +
			itoa(m) + " 的队首写者 " + itoa(l.w) + ", p=0"}
}

func handoffWhy(gEmpty bool) string {
	if gEmpty {
		return "G 空, 传递不受 B 限制"
	}
	return "p < B"
}

// Downgrade converts the write hold into a read hold, granting Qr together
// with the downgrader; gown rejoins G if it still has local waiters.
func (l *Lock) Downgrade(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	if l.states[tid] != StateWriteHold {
		return rejectState("写持有")
	}
	gown := l.gown
	l.w = -1
	l.gown = -1
	l.p = 0
	l.phase = PhaseRead

	l.r = nil
	l.rHas[tid] = true
	l.r = append(l.r, tid)
	l.states[tid] = StateReadHold
	queued := l.qrDrain()
	l.grantReaders(queued)
	granted := append([]int{tid}, queued...)

	tail := ""
	if !l.wEmpty(gown) {
		l.gPush(gown)
		tail = "; Qw[gown] 非空, gown(节点 " + itoa(gown) + ") 追加到 G 尾"
	}
	return Result{OK: true, Granted: granted,
		Reason: "降级: 写者转读持有, 相位置读, R = [降级者] + Qr 全体" + tail}
}

// Upgrade succeeds only when the caller is the sole reader; it removes its
// node from G (the node's Qw stays the local queue) and takes the write hold.
func (l *Lock) Upgrade(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	if l.states[tid] != StateReadHold {
		return rejectState("读持有")
	}
	if !(l.rHas[tid] && len(l.r) == 1) {
		return Result{OK: false, Reason: "拒绝: 升级冲突（R 不恰为 {t}）"}
	}
	n := l.nodeOf[tid]
	l.r = nil
	l.rHas[tid] = false
	l.states[tid] = StateWriteHold
	l.phase = PhaseWrite
	l.w = tid
	l.gown = n
	l.p = 0
	tail := ""
	if l.gIn(n) {
		l.gRemove(n)
		tail = "; 节点 " + itoa(n) + " 从 G 移出, 其 Qw 保留为本地队列"
	}
	return Result{OK: true, Granted: []int{tid},
		Reason: "唯一读者升级: R 清空, 置写相位, gown=节点 " + itoa(n) + ", p=0" + tail}
}

// Cancel removes a read/write waiter. Cancelling the last writer unblocks
// queued readers in the read phase; an emptied node leaves G.
func (l *Lock) Cancel(tid int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, bad := l.badThread(tid); bad {
		return r
	}
	switch l.states[tid] {
	case StateReadWait:
		l.states[tid] = StateIdle
		l.qrRemove(tid)
		return Result{OK: true, Reason: "撤销读等待: 从 Qr 移除"}
	case StateWriteWait:
		n := l.nodeOf[tid]
		l.states[tid] = StateIdle
		l.wRemove(n, tid)
		tail := ""
		if l.wEmpty(n) && l.gIn(n) {
			l.gRemove(n)
			tail = "; Qw[" + itoa(n) + "] 已空, 节点 " + itoa(n) + " 从 G 移出"
		}
		if l.phase == PhaseRead && !l.writerWaiting() && !l.qrEmpty() {
			granted := l.qrDrain()
			l.grantReaders(granted)
			return Result{OK: true, Granted: granted,
				Reason: "撤销写等待: 从 Qw[" + itoa(n) + "] 移除" + tail +
					"; 读相位且已无写者等待, Qr 全体并入 R 授予"}
		}
		return Result{OK: true, Reason: "撤销写等待: 从 Qw[" + itoa(n) + "] 移除" + tail}
	default:
		return rejectState("读等待或写等待")
	}
}

// Snapshot returns a deep, deterministic copy of the lock state.
func (l *Lock) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	s := Snapshot{
		Phase:  l.phase,
		R:      append([]int(nil), l.r...),
		W:      l.w,
		Gown:   l.gown,
		P:      l.p,
		States: append([]ThreadState(nil), l.states...),
	}
	for tid := l.qrHead; tid != -1; tid = l.qrNext[tid] {
		s.Qr = append(s.Qr, tid)
	}
	s.Qw = make([][]int, l.m)
	for n := 0; n < l.m; n++ {
		for tid := l.qwHead[n]; tid != -1; tid = l.wNext[tid] {
			s.Qw[n] = append(s.Qw[n], tid)
		}
	}
	for n := l.gHead; n != -1; n = l.gNext[n] {
		s.G = append(s.G, n)
	}
	return s
}
