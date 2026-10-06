package room

// naiveRoom 是一份刻意“低效但直白”的独立参考实现，用于差分测试：
//
//   - 在室玩家保存为加入次序切片，房主每次线性取首元素；
//   - 全体就绪、人数、名单归属等条件全部线性扫描现算；
//   - 不维护任何聚合计数器（如未就绪数、票数、存活数）；
//   - 不做任何惰性外的副作用，逻辑直接逐条翻译题面。
//
// 它与 Room 共享 Phase/VoidReason/Reject 等纯类型，但代码路径完全独立，
// 因而任何状态机层面的分歧都会在随机对照中暴露。
type naiveRoom struct {
	l, u      int
	c, r      int64
	phase     Phase
	void      VoidReason
	now       int64
	present   []string // 加入次序
	ready     map[string]bool
	cStart    int64
	startAt   int64
	roster    []string
	quit      map[string]bool
	reportDue int64
	reports   map[string]string // 上报人 -> 胜者
	winner    string
}

func newNaive(cfg Config) *naiveRoom {
	return &naiveRoom{
		l: cfg.L, u: cfg.U, c: cfg.C, r: cfg.R,
		phase: PhaseWaiting,
		ready: map[string]bool{},
		quit:  map[string]bool{},
	}
}

func (n *naiveRoom) has(u string) bool {
	for _, p := range n.present {
		if p == u {
			return true
		}
	}
	return false
}

func (n *naiveRoom) onRoster(u string) bool {
	for _, p := range n.roster {
		if p == u {
			return true
		}
	}
	return false
}

func (n *naiveRoom) allReady() bool {
	if len(n.present) == 0 {
		return false
	}
	for _, p := range n.present {
		if !n.ready[p] {
			return false
		}
	}
	return true
}

// alive 为对局名单中仍在室的玩家数。
func (n *naiveRoom) alive() int {
	k := 0
	for _, p := range n.roster {
		if n.has(p) {
			k++
		}
	}
	return k
}

func (n *naiveRoom) expire() {
	if n.phase == PhaseCountdown && n.now >= n.cStart+n.c {
		n.startAt = n.cStart + n.c
		n.roster = append([]string(nil), n.present...)
		n.phase = PhasePlaying
	}
	if n.phase == PhaseSettling && n.now >= n.reportDue {
		w, cnt, distinct := "", 0, map[string]int{}
		for _, cand := range n.reports {
			distinct[cand]++
			cnt++
		}
		if len(distinct) == 1 {
			for k := range distinct {
				w = k
			}
		}
		if w != "" && 2*cnt > n.alive() {
			n.phase = PhaseEnded
			n.winner = w
		} else {
			n.phase = PhaseVoided
			n.void = VoidTimeout
		}
	}
}

func (n *naiveRoom) tryStartCountdown() {
	if n.phase == PhaseWaiting && len(n.present) >= n.l && n.allReady() {
		n.cStart = n.now
		n.phase = PhaseCountdown
	}
}

func (n *naiveRoom) remove(u string) {
	out := n.present[:0]
	for _, p := range n.present {
		if p != u {
			out = append(out, p)
		}
	}
	n.present = out
	delete(n.ready, u)
}

func (n *naiveRoom) join(user string, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{RejectInvalidArg}
	}
	if now < n.now {
		return OpError{RejectClockRewind}
	}
	n.now = now
	n.expire()
	switch n.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{RejectTerminated}
	case PhaseWaiting, PhaseCountdown:
	default:
		return OpError{RejectPhase}
	}
	if n.has(user) {
		return OpError{RejectConflict}
	}
	if len(n.present) >= n.u {
		return OpError{RejectConflict}
	}
	n.phase = PhaseWaiting
	n.present = append(n.present, user)
	n.tryStartCountdown()
	return nil
}

func (n *naiveRoom) setReady(user string, on bool, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{RejectInvalidArg}
	}
	if now < n.now {
		return OpError{RejectClockRewind}
	}
	n.now = now
	n.expire()
	switch n.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{RejectTerminated}
	case PhaseWaiting, PhaseCountdown:
	default:
		return OpError{RejectPhase}
	}
	if !n.has(user) {
		return OpError{RejectNotPresent}
	}
	if n.ready[user] == on {
		return OpError{RejectConflict}
	}
	n.ready[user] = on
	if !on {
		n.phase = PhaseWaiting
		return nil
	}
	if n.phase == PhaseCountdown {
		return nil
	}
	n.tryStartCountdown()
	return nil
}

func (n *naiveRoom) leave(user string, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{RejectInvalidArg}
	}
	if now < n.now {
		return OpError{RejectClockRewind}
	}
	n.now = now
	n.expire()
	switch n.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{RejectTerminated}
	case PhaseWaiting, PhaseCountdown:
		if !n.has(user) {
			return OpError{RejectNotPresent}
		}
		n.remove(user)
		if len(n.present) == 0 {
			n.phase = PhaseVoided
			n.void = VoidEmpty
			return nil
		}
		if len(n.present) < n.l || !n.allReady() {
			n.phase = PhaseWaiting
		} else {
			n.tryStartCountdown()
		}
		return nil
	case PhasePlaying:
		if !n.has(user) {
			return OpError{RejectNotPresent}
		}
		n.quit[user] = true
		n.remove(user)
		if n.alive() < 2 {
			n.phase = PhaseVoided
			n.void = VoidTooFew
		}
		return nil
	default:
		return OpError{RejectPhase}
	}
}

func (n *naiveRoom) end(user string, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{RejectInvalidArg}
	}
	if now < n.now {
		return OpError{RejectClockRewind}
	}
	n.now = now
	n.expire()
	switch n.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{RejectTerminated}
	case PhasePlaying:
	default:
		return OpError{RejectPhase}
	}
	if !n.has(user) {
		return OpError{RejectNotPresent}
	}
	if user != n.present[0] {
		return OpError{RejectNotOwner}
	}
	n.phase = PhaseSettling
	n.reportDue = now + n.r
	n.reports = map[string]string{}
	return nil
}

func (n *naiveRoom) report(user, winner string, now int64) error {
	if user == "" || winner == "" || now < 0 || now > 1e12 {
		return OpError{RejectInvalidArg}
	}
	if now < n.now {
		return OpError{RejectClockRewind}
	}
	n.now = now
	n.expire()
	switch n.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{RejectTerminated}
	case PhaseSettling:
	default:
		return OpError{RejectPhase}
	}
	if !n.has(user) {
		return OpError{RejectNotPresent}
	}
	if !n.onRoster(winner) {
		return OpError{RejectNotPresent}
	}
	n.reports[user] = winner
	if len(n.reports) == n.alive() {
		w, distinct := "", map[string]int{}
		for _, cand := range n.reports {
			distinct[cand]++
		}
		if len(distinct) == 1 {
			for k := range distinct {
				w = k
			}
			n.phase = PhaseEnded
			n.winner = w
		} else {
			n.phase = PhaseVoided
			n.void = VoidDispute
		}
	}
	return nil
}

func (n *naiveRoom) snapshot(now int64) (Snapshot, error) {
	if now < 0 || now > 1e12 {
		return Snapshot{}, OpError{RejectInvalidArg}
	}
	if now < n.now {
		return Snapshot{}, OpError{RejectClockRewind}
	}
	n.now = now
	n.expire()
	s := Snapshot{
		Phase:          n.phase,
		Void:           n.void,
		Now:            n.now,
		Present:        append([]string(nil), n.present...),
		Ready:          map[string]bool{},
		CountdownStart: n.cStart,
		CountdownDue:   n.cStart + n.c,
		StartAt:        n.startAt,
		Roster:         append([]string(nil), n.roster...),
		Quitters:       map[string]bool{},
		ReportDeadline: n.reportDue,
		Reports:        map[string]string{},
		Winner:         n.winner,
	}
	if len(n.present) > 0 {
		s.Owner = n.present[0]
	}
	for _, p := range n.present {
		if n.ready[p] {
			s.Ready[p] = true
			s.ReadyCount++
		}
	}
	for u := range n.quit {
		s.Quitters[u] = true
	}
	for u, w := range n.reports {
		s.Reports[u] = w
	}
	return s, nil
}
