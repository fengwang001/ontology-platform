package room_test

// 独立朴素模型：与被测实现不共享任何逻辑，故意使用线性扫描
// 与最直接的状态重写，用于随机操作序列的差分对照。

import (
	"fmt"

	"ontology/room"
)

type naiveMember struct {
	id    string
	ready bool
	seq   int64 // 加入次序（单调递增）
}

type naiveMatchPlayer struct {
	id       string
	inRoom   bool
	reported bool
	winner   string
}

type naiveRoom struct {
	cfg   room.Config
	state room.State
	clock int64

	members []naiveMember // 切片顺序即加入次序
	host    string
	nextSeq int64

	cdActive bool
	cdStart  int64
	cdExpiry int64

	roster     []naiveMatchPlayer // 对局名单，开局时的加入次序
	matchStart int64
	deadline   int64

	winner string
	reason room.AbortReason
}

func newNaive(cfg room.Config) *naiveRoom {
	return &naiveRoom{cfg: cfg, state: room.StateWaiting}
}

func (n *naiveRoom) memberIndex(id string) int {
	for i, m := range n.members {
		if m.id == id {
			return i
		}
	}
	return -1
}

func (n *naiveRoom) rosterIndex(id string) int {
	for i, p := range n.roster {
		if p.id == id {
			return i
		}
	}
	return -1
}

func (n *naiveRoom) allReady() bool {
	if len(n.members) == 0 {
		return false
	}
	for _, m := range n.members {
		if !m.ready {
			return false
		}
	}
	return true
}

func (n *naiveRoom) conditionHolds() bool {
	return int64(len(n.members)) >= n.cfg.MinPlayers && n.allReady()
}

func (n *naiveRoom) maybeStartCountdown() {
	if n.state == room.StateWaiting && n.conditionHolds() {
		n.cdActive = true
		n.cdStart = n.clock
		n.cdExpiry = n.clock + n.cfg.Countdown
		n.state = room.StateCountdown
	}
}

func (n *naiveRoom) cancelCountdown() {
	n.cdActive = false
	n.cdStart = 0
	n.cdExpiry = 0
	n.state = room.StateWaiting
}

// expire 惰性到期处理，先于操作本身生效。
func (n *naiveRoom) expire(now int64) {
	if n.state == room.StateCountdown && n.cdActive && now >= n.cdExpiry {
		n.matchStart = n.cdExpiry
		n.roster = nil
		for _, m := range n.members {
			n.roster = append(n.roster, naiveMatchPlayer{id: m.id, inRoom: true})
		}
		n.cdActive = false
		n.cdStart = 0
		n.cdExpiry = 0
		n.state = room.StateInProgress
	}
	if n.state == room.StateSettling && now >= n.deadline {
		inRoom := int64(0)
		reports := int64(0)
		unanimous := true
		winner := ""
		first := true
		for _, p := range n.roster {
			if !p.inRoom {
				continue
			}
			inRoom++
			if p.reported {
				reports++
				if first {
					winner = p.winner
					first = false
				} else if p.winner != winner {
					unanimous = false
				}
			}
		}
		if reports > 0 && unanimous && reports > inRoom/2 {
			n.state = room.StateEnded
			n.winner = winner
		} else {
			n.state = room.StateAborted
			n.reason = room.AbortTimeout
		}
	}
}

// begin 公共前置：参数（now 范围）-> 时钟回退 -> 惰性到期 + 推进时钟。
func (n *naiveRoom) begin(op string, now int64) *room.Error {
	if now < 0 || now > room.MaxNow {
		return &room.Error{Code: room.ErrCodeInvalidParam, Op: op, Msg: "now out of range"}
	}
	if now < n.clock {
		return &room.Error{Code: room.ErrCodeClockRollback, Op: op, Msg: "clock rollback"}
	}
	n.expire(now)
	n.clock = now
	return nil
}

func (n *naiveRoom) checkPhase(op string, allowed ...room.State) *room.Error {
	if n.state.Terminal() {
		return &room.Error{Code: room.ErrCodeTerminated, Op: op}
	}
	for _, s := range allowed {
		if n.state == s {
			return nil
		}
	}
	return &room.Error{Code: room.ErrCodePhaseNotAllowed, Op: op}
}

func (n *naiveRoom) removeMember(id string) {
	i := n.memberIndex(id)
	if i < 0 {
		return
	}
	n.members = append(n.members[:i], n.members[i+1:]...)
	if n.host == id {
		n.host = ""
		if len(n.members) > 0 {
			n.host = n.members[0].id // 切片顺序即加入次序
		}
	}
}

func (n *naiveRoom) join(user string, now int64) error {
	const op = "Join"
	if user == "" {
		return &room.Error{Code: room.ErrCodeInvalidParam, Op: op}
	}
	if e := n.begin(op, now); e != nil {
		return e
	}
	if e := n.checkPhase(op, room.StateWaiting, room.StateCountdown); e != nil {
		return e
	}
	if n.memberIndex(user) >= 0 {
		return &room.Error{Code: room.ErrCodeAlreadyJoined, Op: op}
	}
	if int64(len(n.members)) >= n.cfg.MaxPlayers {
		return &room.Error{Code: room.ErrCodeRoomFull, Op: op}
	}
	n.members = append(n.members, naiveMember{id: user, seq: n.nextSeq})
	n.nextSeq++
	if n.host == "" {
		n.host = user
	}
	if n.state == room.StateCountdown {
		n.cancelCountdown()
	}
	n.maybeStartCountdown()
	return nil
}

func (n *naiveRoom) setReady(user string, on bool, now int64) error {
	const op = "SetReady"
	if user == "" {
		return &room.Error{Code: room.ErrCodeInvalidParam, Op: op}
	}
	if e := n.begin(op, now); e != nil {
		return e
	}
	if e := n.checkPhase(op, room.StateWaiting, room.StateCountdown); e != nil {
		return e
	}
	i := n.memberIndex(user)
	if i < 0 {
		return &room.Error{Code: room.ErrCodeNotInRoom, Op: op}
	}
	if on && n.members[i].ready {
		return &room.Error{Code: room.ErrCodeAlreadyReady, Op: op}
	}
	n.members[i].ready = on
	if !on && n.state == room.StateCountdown {
		n.cancelCountdown()
	}
	n.maybeStartCountdown()
	return nil
}

func (n *naiveRoom) leave(user string, now int64) error {
	const op = "Leave"
	if user == "" {
		return &room.Error{Code: room.ErrCodeInvalidParam, Op: op}
	}
	if e := n.begin(op, now); e != nil {
		return e
	}
	if e := n.checkPhase(op, room.StateWaiting, room.StateCountdown, room.StateInProgress); e != nil {
		return e
	}
	if n.memberIndex(user) < 0 {
		return &room.Error{Code: room.ErrCodeNotInRoom, Op: op}
	}
	switch n.state {
	case room.StateWaiting, room.StateCountdown:
		n.removeMember(user)
		if len(n.members) == 0 {
			n.state = room.StateAborted
			n.reason = room.AbortEmpty
			n.cdActive = false
			n.cdStart = 0
			n.cdExpiry = 0
			return nil
		}
		if n.state == room.StateCountdown && !n.conditionHolds() {
			n.cancelCountdown()
		}
		n.maybeStartCountdown()
	case room.StateInProgress:
		n.removeMember(user)
		i := n.rosterIndex(user)
		if i >= 0 && n.roster[i].inRoom {
			n.roster[i].inRoom = false
		}
		inRoom := int64(0)
		for _, p := range n.roster {
			if p.inRoom {
				inRoom++
			}
		}
		if inRoom < 2 {
			n.state = room.StateAborted
			n.reason = room.AbortInsufficient
		}
	}
	return nil
}

func (n *naiveRoom) end(user string, now int64) error {
	const op = "End"
	if user == "" {
		return &room.Error{Code: room.ErrCodeInvalidParam, Op: op}
	}
	if e := n.begin(op, now); e != nil {
		return e
	}
	if e := n.checkPhase(op, room.StateInProgress); e != nil {
		return e
	}
	if n.memberIndex(user) < 0 {
		return &room.Error{Code: room.ErrCodeNotInRoom, Op: op}
	}
	if n.host != user {
		return &room.Error{Code: room.ErrCodeNotHost, Op: op}
	}
	n.deadline = now + n.cfg.ReportWindow
	n.state = room.StateSettling
	return nil
}

func (n *naiveRoom) report(user, winner string, now int64) error {
	const op = "Report"
	if user == "" || winner == "" {
		return &room.Error{Code: room.ErrCodeInvalidParam, Op: op}
	}
	if e := n.begin(op, now); e != nil {
		return e
	}
	if e := n.checkPhase(op, room.StateSettling); e != nil {
		return e
	}
	if n.memberIndex(user) < 0 {
		return &room.Error{Code: room.ErrCodeNotInRoom, Op: op}
	}
	if n.rosterIndex(winner) < 0 {
		return &room.Error{Code: room.ErrCodeNotInRoom, Op: op}
	}
	i := n.rosterIndex(user)
	n.roster[i].reported = true
	n.roster[i].winner = winner
	// 全体在室对局玩家都已上报：一致结束，不一致作废（争议）。
	allReported := true
	unanimous := true
	w := ""
	first := true
	for _, p := range n.roster {
		if !p.inRoom {
			continue
		}
		if !p.reported {
			allReported = false
			break
		}
		if first {
			w = p.winner
			first = false
		} else if p.winner != w {
			unanimous = false
		}
	}
	if allReported {
		if unanimous {
			n.state = room.StateEnded
			n.winner = w
		} else {
			n.state = room.StateAborted
			n.reason = room.AbortDispute
		}
	}
	return nil
}

func (n *naiveRoom) query(now int64) (room.Snapshot, error) {
	const op = "Query"
	if e := n.begin(op, now); e != nil {
		return room.Snapshot{}, e
	}
	snap := room.Snapshot{
		State:           n.state,
		Now:             n.clock,
		Config:          n.cfg,
		Players:         make([]room.PlayerInfo, 0, len(n.members)),
		Host:            n.host,
		MatchStart:      n.matchStart,
		Roster:          make([]room.MatchPlayerInfo, 0, len(n.roster)),
		ReportDeadline:  n.deadline,
		Winner:          n.winner,
		AbortReason:     n.reason,
		CountdownStart:  n.cdStart,
		CountdownExpiry: n.cdExpiry,
	}
	for _, m := range n.members {
		snap.Players = append(snap.Players, room.PlayerInfo{
			ID:      m.id,
			Ready:   m.ready,
			Host:    m.id == n.host,
			JoinSeq: m.seq,
		})
	}
	var reports int64
	for _, p := range n.roster {
		if p.reported {
			reports++
		}
		snap.Roster = append(snap.Roster, room.MatchPlayerInfo{
			ID:       p.id,
			InRoom:   p.inRoom,
			Reported: p.reported,
			Winner:   p.winner,
		})
	}
	snap.ReportCount = reports
	return snap, nil
}

func (n *naiveRoom) String() string {
	return fmt.Sprintf("naive{state=%s clock=%d members=%v host=%s}", n.state, n.clock, n.members, n.host)
}
