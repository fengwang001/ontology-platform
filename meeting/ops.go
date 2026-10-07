package meeting

import "sort"

// 本文件实现会议室的全部公开操作。
// 每个带 now 的操作遵循统一的检查次序（只报告第一个失败类别）：
// 参数非法 > 时钟回退 > 房间已关闭 > 操作者不在室内 >
// 权限不足 > 目标不存在 > 状态不允许。

// Join 以与会者身份加入会议室；首个加入者成为主持人。
func (r *Room) Join(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	if _, ok := r.members[user]; ok {
		return reject(CatState, ReasonAlreadyExists)
	}
	role := RoleAttendee
	if len(r.members) == 0 {
		role = RoleHost
	}
	r.members[user] = &member{role: role, joinOrd: r.joinCounter}
	r.joinCounter++
	return nil
}

// Leave 离开会议室。
// 发言者离开等同于失去发言权并在 now 触发顺延；
// 队列中者离开则被移出；主持人离开触发移交，无其他成员时关闭会议室。
func (r *Room) Leave(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	m, ok := r.members[user]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if r.speaker == user {
		r.speaker = ""
		r.autoGrant(now)
	}
	if r.queue.contains(user) {
		r.queue.remove(user)
	}
	delete(r.members, user)
	if m.role == RoleHost {
		r.transferHost()
	}
	return nil
}

// Raise 举手，进入队尾。
// 重复举手、已在发言、被静音者举手均被拒绝且原因可区分；队列满报队列已满。
func (r *Room) Raise(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	m, ok := r.members[user]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	switch {
	case r.speaker == user:
		return reject(CatState, ReasonAlreadySpeaking)
	case r.queue.contains(user):
		return reject(CatState, ReasonAlreadyInQueue)
	case m.muted:
		return reject(CatState, ReasonMuted)
	case r.queue.len() >= r.queueCap:
		return reject(CatState, ReasonQueueFull)
	}
	r.queue.pushBack(user)
	return nil
}

// Lower 取消自己的举手。
func (r *Room) Lower(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	if _, ok := r.members[user]; !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if !r.queue.contains(user) {
		return reject(CatState, ReasonNotInQueue)
	}
	r.queue.remove(user)
	return nil
}

// Appoint 任命协管员。主持人与协管员可发起。
func (r *Room) Appoint(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	op, ok := r.members[operator]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if op.role == RoleAttendee {
		return reject(CatPermission, ReasonNeedHostOrCo)
	}
	tm, ok := r.members[target]
	if !ok {
		return reject(CatTargetNotFound, ReasonTargetNotFound)
	}
	switch tm.role {
	case RoleHost:
		return reject(CatState, ReasonTargetIsHost)
	case RoleCoHost:
		return reject(CatState, ReasonAlreadyCoHost)
	}
	tm.role = RoleCoHost
	return nil
}

// Revoke 撤销协管员。仅主持人可发起。
func (r *Room) Revoke(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	op, ok := r.members[operator]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if op.role != RoleHost {
		return reject(CatPermission, ReasonNeedHost)
	}
	tm, ok := r.members[target]
	if !ok {
		return reject(CatTargetNotFound, ReasonTargetNotFound)
	}
	if tm.role != RoleCoHost {
		return reject(CatState, ReasonNotCoHost)
	}
	tm.role = RoleAttendee
	return nil
}

// Grant 把发言权授予队首。主持人或协管员可发起。
// 已有发言者或队列为空时拒绝。授予时刻取本操作的 now。
func (r *Room) Grant(operator string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	op, ok := r.members[operator]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if op.role == RoleAttendee {
		return reject(CatPermission, ReasonNeedHostOrCo)
	}
	if r.speaker != "" {
		return reject(CatState, ReasonSpeakerExists)
	}
	next, ok := r.queue.popFront()
	if !ok {
		return reject(CatState, ReasonQueueEmpty)
	}
	r.speaker = next
	r.grantAt = now
	return nil
}

// Yield 发言者主动交还发言权。交还后不自动顺延，
// 发言权保持空闲，直至主持人或协管员再次 Grant。
func (r *Room) Yield(user string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	if _, ok := r.members[user]; !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if r.speaker != user {
		return reject(CatState, ReasonNotSpeaker)
	}
	r.speaker = ""
	return nil
}

// Mute 静音成员。仅主持人可发起；不能静音主持人本人。
// 被静音者若正在发言则立即失去发言权，队首在 now 自动获得发言权；
// 若在队列中则被移出；被静音期间不能举手。
func (r *Room) Mute(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	op, ok := r.members[operator]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if op.role != RoleHost {
		return reject(CatPermission, ReasonNeedHost)
	}
	tm, ok := r.members[target]
	if !ok {
		return reject(CatTargetNotFound, ReasonTargetNotFound)
	}
	if tm.role == RoleHost {
		return reject(CatState, ReasonMuteHost)
	}
	if tm.muted {
		return reject(CatState, ReasonAlreadyMuted)
	}
	tm.muted = true
	if r.speaker == target {
		r.speaker = ""
		r.autoGrant(now)
	}
	if r.queue.contains(target) {
		r.queue.remove(target)
	}
	return nil
}

// Unmute 解除静音。仅主持人可发起。静音不因到期而解除。
func (r *Room) Unmute(operator, target string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operator == "" || target == "" {
		return reject(CatInvalidParam, ReasonEmptyUser)
	}
	if rej := r.prologue(now); rej != nil {
		return rej
	}
	if r.closed {
		return reject(CatClosed, ReasonRoomClosed)
	}
	op, ok := r.members[operator]
	if !ok {
		return reject(CatNotInRoom, ReasonNotInRoom)
	}
	if op.role != RoleHost {
		return reject(CatPermission, ReasonNeedHost)
	}
	tm, ok := r.members[target]
	if !ok {
		return reject(CatTargetNotFound, ReasonTargetNotFound)
	}
	if !tm.muted {
		return reject(CatState, ReasonNotMuted)
	}
	tm.muted = false
	return nil
}

// Snapshot 返回 now 时刻的一致性快照。查询同样先做惰性到期处理。
func (r *Room) Snapshot(now int64) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rej := r.prologue(now); rej != nil {
		return Snapshot{}, rej
	}
	if r.closed {
		return Snapshot{}, reject(CatClosed, ReasonRoomClosed)
	}
	snap := Snapshot{
		Now:     now,
		Speaker: r.speaker,
		Queue:   r.queue.order(),
		Members: make([]MemberInfo, 0, len(r.members)),
	}
	if r.speaker != "" {
		snap.RemainingSecs = r.grantAt + r.speakSecs - now
	}
	type ordInfo struct {
		ord  uint64
		info MemberInfo
	}
	tmp := make([]ordInfo, 0, len(r.members))
	for u, m := range r.members {
		tmp = append(tmp, ordInfo{m.joinOrd, MemberInfo{User: u, Role: m.role, Muted: m.muted}})
	}
	// 按加入次序排序，保证快照确定性。
	sort.Slice(tmp, func(i, j int) bool { return tmp[i].ord < tmp[j].ord })
	for _, t := range tmp {
		snap.Members = append(snap.Members, t.info)
	}
	return snap, nil
}

// QueuePos 返回 user 的队列名次（队首为 1）。
// 纯查询，不携带 now，不做惰性处理、不推进时钟。
func (r *Room) QueuePos(user string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return 0, reject(CatInvalidParam, ReasonEmptyUser)
	}
	if r.closed {
		return 0, reject(CatClosed, ReasonRoomClosed)
	}
	if _, ok := r.members[user]; !ok {
		return 0, reject(CatNotInRoom, ReasonNotInRoom)
	}
	pos := r.queue.pos(user)
	if pos == 0 {
		return 0, reject(CatState, ReasonNotInQueue)
	}
	return pos, nil
}
