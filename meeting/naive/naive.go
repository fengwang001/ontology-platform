// Package naive 是会议室发言权控制服务的独立朴素参考实现。
//
// 它刻意使用最简单的数据结构（切片队列、线性扫描、逐成员遍历），
// 不共享 meeting 包的任何内部逻辑，仅复用其公开类型，
// 用于与优化实现 meeting.Room 做随机差分对照。
package naive

import (
	"sync"

	"ontology/meeting"
)

type member struct {
	user    string
	role    meeting.Role
	muted   bool
	joinOrd uint64
}

// Room 朴素会议室。语义与 meeting.Room 完全一致。
type Room struct {
	mu sync.Mutex

	speakSecs int64
	queueCap  int

	clock  int64
	closed bool

	members     []*member // 按加入次序排列
	joinCounter uint64

	speaker string
	grantAt int64

	queue []string // 切片实现的 FIFO 队列
}

// NewRoom 语义同 meeting.NewRoom。
func NewRoom(speakSecs int64, queueCap int) (*Room, error) {
	if speakSecs < meeting.MinSpeakSecs || speakSecs > meeting.MaxSpeakSecs {
		return nil, reject(meeting.CatInvalidParam, "speak_secs_out_of_range")
	}
	if queueCap < meeting.MinQueueCap || queueCap > meeting.MaxQueueCap {
		return nil, reject(meeting.CatInvalidParam, "queue_cap_out_of_range")
	}
	return &Room{speakSecs: speakSecs, queueCap: queueCap, clock: -1}, nil
}

func reject(cat meeting.Category, reason string) *meeting.Reject {
	return &meeting.Reject{Cat: cat, Reason: reason}
}

func (r *Room) find(user string) *member {
	for _, m := range r.members {
		if m.user == user {
			return m
		}
	}
	return nil
}

func (r *Room) inQueue(user string) bool {
	for _, u := range r.queue {
		if u == user {
			return true
		}
	}
	return false
}

func (r *Room) removeFromQueue(user string) {
	for i, u := range r.queue {
		if u == user {
			r.queue = append(r.queue[:i], r.queue[i+1:]...)
			return
		}
	}
}

func (r *Room) prologue(now int64) *meeting.Reject {
	if now < 0 || now > meeting.MaxNow {
		return reject(meeting.CatInvalidParam, meeting.ReasonNowOutOfRange)
	}
	if now < r.clock {
		return reject(meeting.CatClockRegression, meeting.ReasonClockBackwards)
	}
	r.advance(now)
	return nil
}

func (r *Room) advance(now int64) {
	for r.speaker != "" && r.grantAt+r.speakSecs <= now {
		t := r.grantAt + r.speakSecs
		if len(r.queue) == 0 {
			r.speaker = ""
			break
		}
		r.speaker = r.queue[0]
		r.queue = r.queue[1:]
		r.grantAt = t
	}
	r.clock = now
}

func (r *Room) autoGrant(now int64) {
	if len(r.queue) > 0 {
		r.speaker = r.queue[0]
		r.queue = r.queue[1:]
		r.grantAt = now
	}
}

func (r *Room) transferHost() {
	var pick *member
	for _, m := range r.members {
		if m.role == meeting.RoleCoHost && (pick == nil || m.joinOrd < pick.joinOrd) {
			pick = m
		}
	}
	if pick == nil {
		for _, m := range r.members {
			if pick == nil || m.joinOrd < pick.joinOrd {
				pick = m
			}
		}
	}
	if pick == nil {
		r.closed = true
		return
	}
	pick.role = meeting.RoleHost
}

// Join 语义同 meeting.Room.Join。
func (r *Room) Join(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	if r.find(user) != nil {
		return reject(meeting.CatState, meeting.ReasonAlreadyExists)
	}
	role := meeting.RoleAttendee
	if len(r.members) == 0 {
		role = meeting.RoleHost
	}
	r.members = append(r.members, &member{user: user, role: role, joinOrd: r.joinCounter})
	r.joinCounter++
	return nil
}

// Leave 语义同 meeting.Room.Leave。
func (r *Room) Leave(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	m := r.find(user)
	if m == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if r.speaker == user {
		r.speaker = ""
		r.autoGrant(now)
	}
	r.removeFromQueue(user)
	for i, mm := range r.members {
		if mm == m {
			r.members = append(r.members[:i], r.members[i+1:]...)
			break
		}
	}
	if m.role == meeting.RoleHost {
		r.transferHost()
	}
	return nil
}

// Raise 语义同 meeting.Room.Raise。
func (r *Room) Raise(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	m := r.find(user)
	if m == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	switch {
	case r.speaker == user:
		return reject(meeting.CatState, meeting.ReasonAlreadySpeaking)
	case r.inQueue(user):
		return reject(meeting.CatState, meeting.ReasonAlreadyInQueue)
	case m.muted:
		return reject(meeting.CatState, meeting.ReasonMuted)
	case len(r.queue) >= r.queueCap:
		return reject(meeting.CatState, meeting.ReasonQueueFull)
	}
	r.queue = append(r.queue, user)
	return nil
}

// Lower 语义同 meeting.Room.Lower。
func (r *Room) Lower(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	if r.find(user) == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if !r.inQueue(user) {
		return reject(meeting.CatState, meeting.ReasonNotInQueue)
	}
	r.removeFromQueue(user)
	return nil
}

// Appoint 语义同 meeting.Room.Appoint。
func (r *Room) Appoint(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	op := r.find(operator)
	if op == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if op.role == meeting.RoleAttendee {
		return reject(meeting.CatPermission, meeting.ReasonNeedHostOrCo)
	}
	tm := r.find(target)
	if tm == nil {
		return reject(meeting.CatTargetNotFound, meeting.ReasonTargetNotFound)
	}
	switch tm.role {
	case meeting.RoleHost:
		return reject(meeting.CatState, meeting.ReasonTargetIsHost)
	case meeting.RoleCoHost:
		return reject(meeting.CatState, meeting.ReasonAlreadyCoHost)
	}
	tm.role = meeting.RoleCoHost
	return nil
}

// Revoke 语义同 meeting.Room.Revoke。
func (r *Room) Revoke(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	op := r.find(operator)
	if op == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if op.role != meeting.RoleHost {
		return reject(meeting.CatPermission, meeting.ReasonNeedHost)
	}
	tm := r.find(target)
	if tm == nil {
		return reject(meeting.CatTargetNotFound, meeting.ReasonTargetNotFound)
	}
	if tm.role != meeting.RoleCoHost {
		return reject(meeting.CatState, meeting.ReasonNotCoHost)
	}
	tm.role = meeting.RoleAttendee
	return nil
}

// Grant 语义同 meeting.Room.Grant。
func (r *Room) Grant(operator string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	op := r.find(operator)
	if op == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if op.role == meeting.RoleAttendee {
		return reject(meeting.CatPermission, meeting.ReasonNeedHostOrCo)
	}
	if r.speaker != "" {
		return reject(meeting.CatState, meeting.ReasonSpeakerExists)
	}
	if len(r.queue) == 0 {
		return reject(meeting.CatState, meeting.ReasonQueueEmpty)
	}
	r.speaker = r.queue[0]
	r.queue = r.queue[1:]
	r.grantAt = now
	return nil
}

// Yield 语义同 meeting.Room.Yield。
func (r *Room) Yield(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	if r.find(user) == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if r.speaker != user {
		return reject(meeting.CatState, meeting.ReasonNotSpeaker)
	}
	r.speaker = ""
	return nil
}

// Mute 语义同 meeting.Room.Mute。
func (r *Room) Mute(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	op := r.find(operator)
	if op == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if op.role != meeting.RoleHost {
		return reject(meeting.CatPermission, meeting.ReasonNeedHost)
	}
	tm := r.find(target)
	if tm == nil {
		return reject(meeting.CatTargetNotFound, meeting.ReasonTargetNotFound)
	}
	if tm.role == meeting.RoleHost {
		return reject(meeting.CatState, meeting.ReasonMuteHost)
	}
	if tm.muted {
		return reject(meeting.CatState, meeting.ReasonAlreadyMuted)
	}
	tm.muted = true
	if r.speaker == target {
		r.speaker = ""
		r.autoGrant(now)
	}
	r.removeFromQueue(target)
	return nil
}

// Unmute 语义同 meeting.Room.Unmute。
func (r *Room) Unmute(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	op := r.find(operator)
	if op == nil {
		return reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	if op.role != meeting.RoleHost {
		return reject(meeting.CatPermission, meeting.ReasonNeedHost)
	}
	tm := r.find(target)
	if tm == nil {
		return reject(meeting.CatTargetNotFound, meeting.ReasonTargetNotFound)
	}
	if !tm.muted {
		return reject(meeting.CatState, meeting.ReasonNotMuted)
	}
	tm.muted = false
	return nil
}

// Snapshot 语义同 meeting.Room.Snapshot。
func (r *Room) Snapshot(now int64) (meeting.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rej := r.prologue(now); rej != nil {
		return meeting.Snapshot{}, rej
	}
	if r.closed {
		return meeting.Snapshot{}, reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	snap := meeting.Snapshot{
		Now:     now,
		Speaker: r.speaker,
		Queue:   append([]string{}, r.queue...),
		Members: make([]meeting.MemberInfo, 0, len(r.members)),
	}
	if r.speaker != "" {
		snap.RemainingSecs = r.grantAt + r.speakSecs - now
	}
	for _, m := range r.members {
		snap.Members = append(snap.Members, meeting.MemberInfo{User: m.user, Role: m.role, Muted: m.muted})
	}
	return snap, nil
}

// QueuePos 语义同 meeting.Room.QueuePos。
func (r *Room) QueuePos(user string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return 0, reject(meeting.CatInvalidParam, meeting.ReasonEmptyUser)
	}
	if r.closed {
		return 0, reject(meeting.CatClosed, meeting.ReasonRoomClosed)
	}
	if r.find(user) == nil {
		return 0, reject(meeting.CatNotInRoom, meeting.ReasonNotInRoom)
	}
	for i, u := range r.queue {
		if u == user {
			return i + 1, nil
		}
	}
	return 0, reject(meeting.CatState, meeting.ReasonNotInQueue)
}
