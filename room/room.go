package room

import (
	"fmt"
	"sync"
)

// Room 为多人对局房间生命周期服务。
//
// 所有方法可并发调用：内部以互斥锁串行化，
// 效果等价于某个串行顺序。到期（倒计时 / 上报期限）采用惰性处理：
// 每个操作与查询在执行自身逻辑之前，先以到期时刻完成到期跃迁，
// 因此任意操作序列的结果可精确复现，与真实时间无关。
type Room struct {
	mu  sync.Mutex
	cfg Config

	state State
	clock clock

	roster    roster
	countdown countdown
	match     match

	matchStart     int64
	reportDeadline int64

	winner      string
	abortReason AbortReason

	work int64 // 工作量计数器：验证复杂度上界（见 WorkUnits）
}

// NewRoom 创建处于等待阶段的房间。参数非法时返回错误。
func NewRoom(cfg Config) (*Room, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("room: invalid config: %w", err)
	}
	r := &Room{cfg: cfg, state: StateWaiting}
	r.roster = *newRoster(&r.work)
	r.match = *newMatch(&r.work)
	return r, nil
}

// WorkUnits 返回房间累计的内部循环工作量计数。
// 用于以可验证方式证明各操作开销不随历史事件数增长。
func (r *Room) WorkUnits() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.work
}

// beginOp 完成所有变更类操作与查询的公共前置流程：
// 参数（now 范围）-> 时钟回退 -> 惰性到期处理并推进时钟。
// 返回非 nil 错误时不产生任何变化。
func (r *Room) beginOp(now int64) *Error {
	if e := r.clock.check(now); e != nil {
		return e
	}
	r.settleExpired(now)
	r.clock.advance(now)
	return nil
}

// settleExpired 惰性到期处理：先于本次操作本身生效。
func (r *Room) settleExpired(now int64) {
	if r.state == StateCountdown && r.countdown.expired(now) {
		r.startMatch()
	}
	if r.state == StateSettling && now >= r.reportDeadline {
		r.resolveReportDeadline()
	}
}

// startMatch 以倒计时到期时刻为开局时刻进入进行中。
func (r *Room) startMatch() {
	r.matchStart = r.countdown.expiry
	r.countdown.clear()
	r.match.begin(r.roster.joinOrdered())
	r.state = StateInProgress
}

// resolveReportDeadline 上报期限到期裁决（此刻必然未全体上报，
// 否则在最后一次上报时已经产生结论）。
func (r *Room) resolveReportDeadline() {
	unanimous, winner, count := r.match.tally()
	if count > 0 && unanimous && count > r.match.inRoom/2 {
		r.finish(winner)
		return
	}
	r.abort(AbortTimeout)
}

func (r *Room) finish(winner string) {
	r.state = StateEnded
	r.winner = winner
}

func (r *Room) abort(reason AbortReason) {
	r.state = StateAborted
	r.abortReason = reason
	r.countdown.clear()
}

// checkPhase 校验阶段：终态报已终止，其余按 allowed 判断。
func (r *Room) checkPhase(op string, allowed ...State) *Error {
	if r.state.Terminal() {
		return &Error{Code: ErrCodeTerminated, Op: op, Msg: "room is in terminal state " + r.state.String()}
	}
	for _, s := range allowed {
		if r.state == s {
			return nil
		}
	}
	return &Error{Code: ErrCodePhaseNotAllowed, Op: op, Msg: "op not allowed in state " + r.state.String()}
}

// maybeStartCountdown 处于等待时，只要条件成立就立即进入倒计时，
// 起算时刻为当前时钟（即促成该条件的操作的 now）。
func (r *Room) maybeStartCountdown() {
	if r.state != StateWaiting {
		return
	}
	if r.roster.size() >= r.cfg.MinPlayers && r.roster.allReady() {
		r.countdown.begin(r.clock.now, r.cfg.Countdown)
		r.state = StateCountdown
	}
}

// Join 加入房间。第一个加入者为房主；新加入者未就绪。
func (r *Room) Join(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	const op = "Join"
	if user == "" {
		return &Error{Code: ErrCodeInvalidParam, Op: op, Msg: "empty user id"}
	}
	if e := r.beginOp(now); e != nil {
		e.Op = op
		return e
	}
	if e := r.checkPhase(op, StateWaiting, StateCountdown); e != nil {
		return e
	}
	if r.roster.has(user) {
		return &Error{Code: ErrCodeAlreadyJoined, Op: op, Msg: "user already in room"}
	}
	if r.roster.size() >= r.cfg.MaxPlayers {
		return &Error{Code: ErrCodeRoomFull, Op: op, Msg: "room is full"}
	}
	r.roster.add(user)
	if r.state == StateCountdown {
		// 倒计时期间任何加入都使房间立即退回等待。
		r.countdown.clear()
		r.state = StateWaiting
	}
	r.maybeStartCountdown()
	return nil
}

// SetReady 切换就绪标记。
func (r *Room) SetReady(user string, on bool, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	const op = "SetReady"
	if user == "" {
		return &Error{Code: ErrCodeInvalidParam, Op: op, Msg: "empty user id"}
	}
	if e := r.beginOp(now); e != nil {
		e.Op = op
		return e
	}
	if e := r.checkPhase(op, StateWaiting, StateCountdown); e != nil {
		return e
	}
	m := r.roster.get(user)
	if m == nil {
		return &Error{Code: ErrCodeNotInRoom, Op: op, Msg: "user not in room"}
	}
	if on && m.ready {
		return &Error{Code: ErrCodeAlreadyReady, Op: op, Msg: "user already ready"}
	}
	r.roster.setReady(user, on)
	if !on && r.state == StateCountdown {
		// 倒计时期间取消就绪使房间立即退回等待。
		r.countdown.clear()
		r.state = StateWaiting
	}
	r.maybeStartCountdown()
	return nil
}

// Leave 离开房间。
//
// 等待/倒计时：直接移除；无人在室则作废。
// 倒计时中仅当离开使开局条件不再成立时才退回等待，否则倒计时不受影响。
// 进行中：视为中途退出，保留在对局名单中但取消上报资格；
// 在室对局玩家不足两人时对局立即作废。
// 房主离开时（任何阶段）迁移给在室玩家中加入次序最早者。
func (r *Room) Leave(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	const op = "Leave"
	if user == "" {
		return &Error{Code: ErrCodeInvalidParam, Op: op, Msg: "empty user id"}
	}
	if e := r.beginOp(now); e != nil {
		e.Op = op
		return e
	}
	if e := r.checkPhase(op, StateWaiting, StateCountdown, StateInProgress); e != nil {
		return e
	}
	if !r.roster.has(user) {
		return &Error{Code: ErrCodeNotInRoom, Op: op, Msg: "user not in room"}
	}
	switch r.state {
	case StateWaiting, StateCountdown:
		r.roster.remove(user)
		if r.roster.size() == 0 {
			r.abort(AbortEmpty)
			return nil
		}
		if r.state == StateCountdown &&
			!(r.roster.size() >= r.cfg.MinPlayers && r.roster.allReady()) {
			// 离开使条件不再成立：退回等待并清除倒计时。
			r.countdown.clear()
			r.state = StateWaiting
		}
		// 条件仍成立则倒计时不受影响（起算时刻不变）；
		// 处于等待时条件成立则立即进入倒计时。
		r.maybeStartCountdown()
	case StateInProgress:
		r.roster.remove(user)
		r.match.quit(user)
		if r.match.inRoom < 2 {
			r.abort(AbortInsufficient)
		}
	}
	return nil
}

// End 结束对局并进入结算中，开始上报期。仅房主可调用。
func (r *Room) End(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	const op = "End"
	if user == "" {
		return &Error{Code: ErrCodeInvalidParam, Op: op, Msg: "empty user id"}
	}
	if e := r.beginOp(now); e != nil {
		e.Op = op
		return e
	}
	if e := r.checkPhase(op, StateInProgress); e != nil {
		return e
	}
	if !r.roster.has(user) {
		return &Error{Code: ErrCodeNotInRoom, Op: op, Msg: "user not in room"}
	}
	if r.roster.host != user {
		return &Error{Code: ErrCodeNotHost, Op: op, Msg: "only the host can end the match"}
	}
	r.reportDeadline = now + r.cfg.ReportWindow
	r.state = StateSettling
	return nil
}

// Report 上报胜者。仅仍在室的对局玩家可在期限内上报（含改报）；
// 胜者须为对局名单中的玩家（含中途退出者）。
func (r *Room) Report(user, winner string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	const op = "Report"
	if user == "" || winner == "" {
		return &Error{Code: ErrCodeInvalidParam, Op: op, Msg: "empty user or winner id"}
	}
	if e := r.beginOp(now); e != nil {
		e.Op = op
		return e
	}
	if e := r.checkPhase(op, StateSettling); e != nil {
		return e
	}
	if !r.roster.has(user) {
		return &Error{Code: ErrCodeNotInRoom, Op: op, Msg: "user not in room"}
	}
	if _, ok := r.match.players[winner]; !ok {
		return &Error{Code: ErrCodeNotInRoom, Op: op, Msg: "winner not in match roster"}
	}
	r.match.record(user, winner)
	if r.match.reports == r.match.inRoom {
		// 全体在室对局玩家均已上报：一致则结束，不一致则作废（争议）。
		unanimous, w, _ := r.match.tally()
		if unanimous {
			r.finish(w)
		} else {
			r.abort(AbortDispute)
		}
	}
	return nil
}

// Query 返回 now 时刻的房间快照。与其他操作一样先做惰性到期处理。
func (r *Room) Query(now int64) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	const op = "Query"
	if e := r.beginOp(now); e != nil {
		e.Op = op
		return Snapshot{}, e
	}
	return r.snapshot(), nil
}

func (r *Room) snapshot() Snapshot {
	snap := Snapshot{
		State:           r.state,
		Now:             r.clock.now,
		Config:          r.cfg,
		Players:         make([]PlayerInfo, 0, r.roster.size()),
		Host:            r.roster.host,
		MatchStart:      r.matchStart,
		ReportDeadline:  r.reportDeadline,
		ReportCount:     r.match.reports,
		Winner:          r.winner,
		AbortReason:     r.abortReason,
		Roster:          make([]MatchPlayerInfo, 0, len(r.match.order)),
		CountdownStart:  r.countdown.start,
		CountdownExpiry: r.countdown.expiry,
	}
	for _, m := range r.roster.joinOrdered() {
		snap.Players = append(snap.Players, PlayerInfo{
			ID:      m.id,
			Ready:   m.ready,
			Host:    m.id == r.roster.host,
			JoinSeq: m.joinSeq,
		})
	}
	for _, id := range r.match.order {
		*r.match.work++
		p := r.match.players[id]
		snap.Roster = append(snap.Roster, MatchPlayerInfo{
			ID:       p.id,
			InRoom:   p.inRoom,
			Reported: p.reported,
			Winner:   p.winner,
		})
	}
	return snap
}
