package room

import "sync"

// Room 为多人对局房间生命周期服务。
//
// 所有方法可被并发调用：内部以互斥锁串行化，对外等价于某一串行顺序。
// 时间为外部注入的整数秒（单调不回退），到期一律惰性处理：
// 任何通过参数与时钟检查的操作/查询都会先做到期裁决，再执行自身逻辑。
type Room struct {
	mu sync.Mutex

	cfg Config

	phase Phase
	void  VoidReason
	now   int64

	reg    *registry
	cStart int64

	startAt   int64
	roster    []string
	rosterSet map[string]bool
	quit      map[string]bool
	alive     int

	reportDue int64
	ledger    *ledger

	winner string
}

// New 校验配置并创建一个等待阶段的房间。
func New(cfg Config) (*Room, error) {
	if cfg.L < 2 || cfg.L > 20 ||
		cfg.U < cfg.L || cfg.U > 20 ||
		cfg.C < 1 || cfg.C > 600 ||
		cfg.R < 1 || cfg.R > 3600 {
		return nil, OpError{Reason: RejectInvalidArg}
	}
	return &Room{
		cfg:   cfg,
		phase: PhaseWaiting,
		reg:   newRegistry(),
		quit:  map[string]bool{},
	}, nil
}

// Join 加入房间。
func (r *Room) Join(user string, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{Reason: RejectInvalidArg}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return OpError{Reason: RejectClockRewind}
	}
	r.now = now
	r.expireLocked()

	switch r.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{Reason: RejectTerminated}
	case PhaseWaiting, PhaseCountdown:
	default:
		return OpError{Reason: RejectPhase}
	}
	if r.reg.has(user) {
		return OpError{Reason: RejectConflict} // 已加入
	}
	if r.reg.size() >= r.cfg.U {
		return OpError{Reason: RejectConflict} // 房间已满
	}

	// 倒计时期间加入立即退回等待并清除倒计时。
	r.phase = PhaseWaiting
	r.reg.add(user)
	r.startCountdownIfReadyLocked()
	return nil
}

// SetReady 切换在室玩家的就绪标记。
func (r *Room) SetReady(user string, on bool, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{Reason: RejectInvalidArg}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return OpError{Reason: RejectClockRewind}
	}
	r.now = now
	r.expireLocked()

	switch r.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{Reason: RejectTerminated}
	case PhaseWaiting, PhaseCountdown:
	default:
		return OpError{Reason: RejectPhase}
	}
	if !r.reg.has(user) {
		return OpError{Reason: RejectNotPresent}
	}
	_, changed := r.reg.setReady(user, on)
	if !changed {
		return OpError{Reason: RejectConflict} // 已就绪再就绪 / 已取消再取消
	}

	if !on {
		// 取消就绪：倒计时中立即退回等待；等待中本就无倒计时可清。
		r.phase = PhaseWaiting
		return nil
	}
	if r.phase == PhaseCountdown {
		return nil // 倒计时中补就绪不改变起算时刻
	}
	r.startCountdownIfReadyLocked()
	return nil
}

// Leave 离开房间；进行中离开记为中途退出。
func (r *Room) Leave(user string, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{Reason: RejectInvalidArg}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return OpError{Reason: RejectClockRewind}
	}
	r.now = now
	r.expireLocked()

	switch r.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{Reason: RejectTerminated}
	case PhaseWaiting, PhaseCountdown:
		if !r.reg.has(user) {
			return OpError{Reason: RejectNotPresent}
		}
		r.reg.remove(user)
		if r.reg.size() == 0 {
			r.toVoidLocked(VoidEmpty)
			return nil
		}
		// 离开使全体就绪条件或人数下限不再成立 -> 退回等待清除倒计时；
		// 条件仍成立（倒计时中离开一名已就绪者）-> 起算时刻不变。
		if r.reg.size() < r.cfg.L || !r.reg.allReady() {
			r.phase = PhaseWaiting
		} else {
			r.startCountdownIfReadyLocked()
		}
		return nil
	case PhasePlaying:
		if !r.reg.has(user) {
			return OpError{Reason: RejectNotPresent}
		}
		// 先记录中途退出（即便其为房主也保留在名单中），再从在室摘除；
		// 摘除后链表头即新房主，完成房主迁移。
		r.quit[user] = true
		r.reg.remove(user)
		r.alive--
		if r.alive < 2 {
			r.toVoidLocked(VoidTooFew)
		}
		return nil
	default: // 结算中不允许离开
		return OpError{Reason: RejectPhase}
	}
}

// End 仅房主可调用，使进行中房间进入结算中。
func (r *Room) End(user string, now int64) error {
	if user == "" || now < 0 || now > 1e12 {
		return OpError{Reason: RejectInvalidArg}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return OpError{Reason: RejectClockRewind}
	}
	r.now = now
	r.expireLocked()

	switch r.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{Reason: RejectTerminated}
	case PhasePlaying:
	default:
		return OpError{Reason: RejectPhase}
	}
	if !r.reg.has(user) {
		return OpError{Reason: RejectNotPresent}
	}
	if user != r.reg.owner() {
		return OpError{Reason: RejectNotOwner}
	}
	r.phase = PhaseSettling
	r.reportDue = now + r.cfg.R
	r.ledger = newLedger()
	return nil
}

// Report 上报/改报胜者；仅结算中、仍在室的对局玩家可在期限内调用。
func (r *Room) Report(user, winner string, now int64) error {
	if user == "" || winner == "" || now < 0 || now > 1e12 {
		return OpError{Reason: RejectInvalidArg}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return OpError{Reason: RejectClockRewind}
	}
	r.now = now
	// 期限到期按惰性规则先于本次上报生效；到期后任何上报被拒。
	r.expireLocked()

	switch r.phase {
	case PhaseEnded, PhaseVoided:
		return OpError{Reason: RejectTerminated}
	case PhaseSettling:
	default:
		return OpError{Reason: RejectPhase}
	}
	if !r.reg.has(user) {
		// 不在室：中途退出者上报资格已取消（其亦不在在室集合）。
		return OpError{Reason: RejectNotPresent}
	}
	if !r.rosterSet[winner] {
		return OpError{Reason: RejectNotPresent} // 胜者不在对局名单
	}

	r.ledger.submit(user, winner)
	if r.ledger.reportedCount() == r.alive {
		// 全体仍在室的对局玩家都已上报。
		if w := r.ledger.unanimousWinner(); w != "" {
			r.finishLocked(w)
		} else {
			r.toVoidLocked(VoidDispute)
		}
	}
	return nil
}

// Snapshot 在推进惰性到期与时钟后返回房间只读快照。
func (r *Room) Snapshot(now int64) (Snapshot, error) {
	if now < 0 || now > 1e12 {
		return Snapshot{}, OpError{Reason: RejectInvalidArg}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return Snapshot{}, OpError{Reason: RejectClockRewind}
	}
	r.now = now
	r.expireLocked()

	s := Snapshot{
		Phase:          r.phase,
		Void:           r.void,
		Owner:          r.reg.owner(),
		Now:            r.now,
		Present:        r.reg.present(),
		Ready:          map[string]bool{},
		ReadyCount:     r.reg.size() - r.reg.notReady,
		CountdownStart: r.cStart,
		CountdownDue:   r.cStart + r.cfg.C,
		StartAt:        r.startAt,
		Roster:         append([]string(nil), r.roster...),
		Quitters:       map[string]bool{},
		ReportDeadline: r.reportDue,
		Reports:        map[string]string{},
		Winner:         r.winner,
	}
	for u, on := range r.reg.ready {
		if on {
			s.Ready[u] = true
		}
	}
	for u := range r.quit {
		s.Quitters[u] = true
	}
	if r.ledger != nil {
		for u, w := range r.ledger.given {
			s.Reports[u] = w
		}
	}
	return s, nil
}

func (r *Room) toVoidLocked(reason VoidReason) {
	r.phase = PhaseVoided
	r.void = reason
}

func (r *Room) finishLocked(winner string) {
	r.phase = PhaseEnded
	r.winner = winner
}

// expireLocked 按当前 r.now 惰性处理一切到期：
// 倒计时到期 -> 进行中；上报期限到期 -> 按过半一致裁决结束/作废。
func (r *Room) expireLocked() {
	if r.phase == PhaseCountdown && r.now >= r.cStart+r.cfg.C {
		// 以到期时刻（而非本次操作的 now）为开局时刻。
		r.startAt = r.cStart + r.cfg.C
		r.roster = r.reg.present()
		r.rosterSet = map[string]bool{}
		for _, u := range r.roster {
			r.rosterSet[u] = true
		}
		r.alive = len(r.roster)
		r.phase = PhasePlaying
	}
	if r.phase == PhaseSettling && r.now >= r.reportDue {
		w := r.ledger.unanimousWinner()
		// 过半要求严格：已上报人数 > 在室对局玩家的一半。
		if w != "" && 2*r.ledger.reportedCount() > r.alive {
			r.finishLocked(w)
		} else {
			r.toVoidLocked(VoidTimeout)
		}
	}
}

// startCountdownIfReadyLocked 在等待态检查全体就绪且人数达标，
// 成立则以 r.now 为起算时刻进入倒计时。
func (r *Room) startCountdownIfReadyLocked() {
	if r.phase == PhaseWaiting &&
		r.reg.size() >= r.cfg.L &&
		r.reg.allReady() {
		r.cStart = r.now
		r.phase = PhaseCountdown
	}
}
