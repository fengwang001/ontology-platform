package numalock

// naiveModel is a literal, step-by-step transcription of the specification
// using plain slices. It is the reference oracle for the real Lock.
type naiveModel struct {
	m, t, b int
	nodeOf  []int

	phase  Phase
	states []ThreadState
	r      []int
	w      int
	gown   int
	p      int
	qr     []int
	qw     [][]int
	g      []int
}

func newNaive(m, t int, nodeOf []int, b int) *naiveModel {
	qw := make([][]int, m)
	for i := range qw {
		qw[i] = []int{}
	}
	st := make([]ThreadState, t)
	return &naiveModel{
		m: m, t: t, b: b, nodeOf: append([]int(nil), nodeOf...),
		states: st, w: -1, gown: -1,
		qr: []int{}, qw: qw, g: []int{},
	}
}

func (n *naiveModel) contains(list []int, x int) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

func (n *naiveModel) remove(list []int, x int) []int {
	out := list[:0]
	for _, v := range list {
		if v != x {
			out = append(out, v)
		}
	}
	return append([]int(nil), out...)
}

func (n *naiveModel) writerWaiting() bool {
	for _, q := range n.qw {
		if len(q) > 0 {
			return true
		}
	}
	return false
}

// startWrite: 起写
func (n *naiveModel) startWrite() {
	if len(n.g) == 0 {
		n.phase = PhaseIdle
		n.w = -1
		n.gown = -1
		n.p = 0
		return
	}
	m := n.g[0]
	n.g = append([]int(nil), n.g[1:]...)
	tid := n.qw[m][0]
	n.qw[m] = append([]int(nil), n.qw[m][1:]...)
	n.phase = PhaseWrite
	n.w = tid
	n.gown = m
	n.p = 0
	n.states[tid] = StateWriteHold
}

type naiveResult struct {
	ok      bool
	granted []int
	reason  string
}

func nrReject(reason string) naiveResult { return naiveResult{ok: false, reason: reason} }

func (n *naiveModel) badThread(tid int) (naiveResult, bool) {
	if tid < 0 || tid >= n.t {
		return nrReject("拒绝: 线程号越界"), false
	}
	return naiveResult{}, true
}

func (n *naiveModel) RLock(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	if n.states[tid] != StateIdle {
		return nrReject("拒绝: 状态不符（须空闲）")
	}
	switch {
	case n.phase == PhaseIdle:
		n.phase = PhaseRead
		n.states[tid] = StateReadHold
		n.r = append(n.r, tid)
		return naiveResult{true, []int{tid}, "相位空闲: 置读相位并授予"}
	case n.phase == PhaseRead && !n.writerWaiting():
		n.states[tid] = StateReadHold
		n.r = append(n.r, tid)
		return naiveResult{true, []int{tid}, "读相位且无写者等待: 随当前读批进入"}
	default:
		n.states[tid] = StateReadWait
		n.qr = append(n.qr, tid)
		return naiveResult{true, nil, "入 Qr 等待下一读批"}
	}
}

func (n *naiveModel) RUnlock(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	if n.states[tid] != StateReadHold {
		return nrReject("拒绝: 状态不符（须读持有）")
	}
	n.r = n.remove(n.r, tid)
	n.states[tid] = StateIdle
	if len(n.r) > 0 {
		return naiveResult{true, nil, "R 仍非空: 读相位继续"}
	}
	n.startWrite()
	if n.phase == PhaseWrite {
		return naiveResult{true, []int{n.w}, "R 空, 起写授予"}
	}
	return naiveResult{true, nil, "R 空, 相位空闲"}
}

func (n *naiveModel) WLock(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	if n.states[tid] != StateIdle {
		return nrReject("拒绝: 状态不符（须空闲）")
	}
	node := n.nodeOf[tid]
	if n.phase == PhaseIdle {
		n.phase = PhaseWrite
		n.w = tid
		n.gown = node
		n.p = 0
		n.states[tid] = StateWriteHold
		return naiveResult{true, []int{tid}, "相位空闲: 授予写"}
	}
	n.states[tid] = StateWriteWait
	n.qw[node] = append(n.qw[node], tid)
	if !(n.phase == PhaseWrite && node == n.gown) && !n.contains(n.g, node) {
		n.g = append(n.g, node)
	}
	return naiveResult{true, nil, "入 Qw"}
}

func (n *naiveModel) WUnlock(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	if n.states[tid] != StateWriteHold {
		return nrReject("拒绝: 状态不符（须写持有）")
	}
	gown := n.gown

	// ① Qr 非空: 读者先授予
	if len(n.qr) > 0 {
		n.states[tid] = StateIdle
		n.w = -1
		n.phase = PhaseRead
		granted := append([]int(nil), n.qr...)
		n.r = append([]int(nil), n.qr...)
		for _, x := range granted {
			n.states[x] = StateReadHold
		}
		n.qr = []int{}
		if len(n.qw[gown]) > 0 && !n.contains(n.g, gown) {
			n.g = append(n.g, gown)
		}
		n.gown = -1
		n.p = 0
		return naiveResult{true, granted, "① Qr 非空: 读批授予"}
	}

	// ② 本地传递（G 空时不受 B 限制）
	if len(n.qw[gown]) > 0 && (len(n.g) == 0 || n.p < n.b) {
		n.states[tid] = StateIdle
		next := n.qw[gown][0]
		n.qw[gown] = append([]int(nil), n.qw[gown][1:]...)
		n.w = next
		n.p++
		n.states[next] = StateWriteHold
		return naiveResult{true, []int{next}, "② 本地传递"}
	}

	// ③ gown 重入 G 尾, 起写
	n.states[tid] = StateIdle
	n.w = -1
	n.gown = -1
	n.p = 0
	if len(n.qw[gown]) > 0 && !n.contains(n.g, gown) {
		n.g = append(n.g, gown)
	}
	n.startWrite()
	if n.phase == PhaseWrite {
		return naiveResult{true, []int{n.w}, "③ 起写授予他节点"}
	}
	return naiveResult{true, nil, "③ 相位空闲"}
}

func (n *naiveModel) Downgrade(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	if n.states[tid] != StateWriteHold {
		return nrReject("拒绝: 状态不符（须写持有）")
	}
	gown := n.gown
	n.w = -1
	n.gown = -1
	n.p = 0
	n.phase = PhaseRead
	queued := append([]int(nil), n.qr...)
	n.qr = []int{}
	n.r = append([]int{tid}, queued...)
	n.states[tid] = StateReadHold
	for _, x := range queued {
		n.states[x] = StateReadHold
	}
	if len(n.qw[gown]) > 0 && !n.contains(n.g, gown) {
		n.g = append(n.g, gown)
	}
	return naiveResult{true, append([]int{tid}, queued...), "降级授予 [t]+Qr"}
}

func (n *naiveModel) Upgrade(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	if n.states[tid] != StateReadHold {
		return nrReject("拒绝: 状态不符（须读持有）")
	}
	if !(len(n.r) == 1 && n.r[0] == tid) {
		return nrReject("拒绝: 升级冲突（R 不恰为 {t}）")
	}
	node := n.nodeOf[tid]
	n.r = []int{}
	n.phase = PhaseWrite
	n.w = tid
	n.gown = node
	n.p = 0
	n.states[tid] = StateWriteHold
	if n.contains(n.g, node) {
		n.g = n.remove(n.g, node)
	}
	return naiveResult{true, []int{tid}, "唯一读者升级"}
}

func (n *naiveModel) Cancel(tid int) naiveResult {
	if r, ok := n.badThread(tid); !ok {
		return r
	}
	switch n.states[tid] {
	case StateReadWait:
		n.states[tid] = StateIdle
		n.qr = n.remove(n.qr, tid)
		return naiveResult{true, nil, "撤销读等待"}
	case StateWriteWait:
		node := n.nodeOf[tid]
		n.states[tid] = StateIdle
		n.qw[node] = n.remove(n.qw[node], tid)
		if len(n.qw[node]) == 0 && n.contains(n.g, node) {
			n.g = n.remove(n.g, node)
		}
		if n.phase == PhaseRead && !n.writerWaiting() && len(n.qr) > 0 {
			granted := append([]int(nil), n.qr...)
			for _, x := range granted {
				n.states[x] = StateReadHold
			}
			n.r = append(n.r, granted...)
			n.qr = []int{}
			return naiveResult{true, granted, "撤销最后写者, 读者授予"}
		}
		return naiveResult{true, nil, "撤销写等待"}
	default:
		return nrReject("拒绝: 状态不符（须读等待或写等待）")
	}
}

func (n *naiveModel) snapshot() Snapshot {
	s := Snapshot{
		Phase:  n.phase,
		R:      append([]int(nil), n.r...),
		W:      n.w,
		Gown:   n.gown,
		P:      n.p,
		Qr:     append([]int(nil), n.qr...),
		Qw:     make([][]int, n.m),
		G:      append([]int(nil), n.g...),
		States: append([]ThreadState(nil), n.states...),
	}
	for i := range n.qw {
		s.Qw[i] = append([]int(nil), n.qw[i]...)
	}
	return s
}
