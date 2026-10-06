package floorcontrol

import "sync"

// Role 为成员角色。
type Role int

const (
	RoleAttendee Role = iota
	RoleCoManager
	RoleHost
)

// MemberInfo 是 Snapshot 中单个成员的视图。
type MemberInfo struct {
	User     string
	Role     Role
	Muted    bool
	JoinedAt int64
}

// Snapshot 是会议室在惰性到期处理之后的确定状态视图。
type Snapshot struct {
	Host      string
	Speaker   string
	Remaining int64 // 发言剩余秒数；无发言者时为 0
	Queue     []string
	Members   []MemberInfo // 按加入次序
}

// Room 是一个带限时发言权控制的在线会议室。
// 所有方法均在互斥锁内串行执行，因此并发调用等价于某个串行顺序。
type Room struct {
	mu sync.Mutex

	s   int64 // 单次发言时限
	cap int   // 举手队列容量

	closed  bool
	members []*member // 按加入次序
	byID    map[string]*member
	host    string

	queue      *handQueue
	nextTicket int64

	speaker    string // 当前发言者，空串表示无人发言
	grantedAt  int64
	expireAt   int64
	lastNow    int64
	clockSet   bool
	expiryHook func(lost, granted string, at int64)
}

type member struct {
	user     string
	role     Role
	muted    bool
	joinedAt int64
}

// New 创建会议室：S 为单次发言秒数（1..3600），Q 为队列容量（1..500）。
func New(seconds int64, queueCap int) (*Room, error) {
	if seconds < 1 || seconds > 3600 || queueCap < 1 || queueCap > 500 {
		return nil, ErrInvalidArgument
	}
	return &Room{
		s:     seconds,
		cap:   queueCap,
		byID:  make(map[string]*member),
		queue: newHandQueue(),
	}, nil
}

const maxTime int64 = 1_000_000_000_000

func validTime(now int64) bool { return now >= 0 && now <= maxTime }

// begin 执行操作者类操作共有的前置流程：参数校验 → 时钟校验 →
// 惰性到期处理与时钟推进 → 关闭校验 → 操作者存在校验。
// 参数非法或时钟回退不做任何处理；其后的拒绝也已经完成到期处理与时钟推进。
func (r *Room) begin(operator string, now int64) (*member, error) {
	if operator == "" || !validTime(now) {
		return nil, ErrInvalidArgument
	}
	r.mu.Lock()
	if r.clockSet && now < r.lastNow {
		r.mu.Unlock()
		return nil, ErrClockBack
	}
	r.advance(now)
	if r.closed {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	m := r.byID[operator]
	if m == nil {
		r.mu.Unlock()
		return nil, ErrOperatorNotInRoom
	}
	return m, nil
}

// advance 把时钟推进到 now，并按时间顺序处理全部已到期发言权。
// 每一轮：发言者在 expireAt 时刻失去发言权；若队列非空，队首在
// 同一 expireAt 时刻获得发言权（授予时刻取 expireAt 而非 now），
// 因而可能立即再次到期并连续顺延，直至无人到期或队列清空。
func (r *Room) advance(now int64) {
	r.lastNow = now
	r.clockSet = true
	for r.speaker != "" && r.expireAt <= now {
		at := r.expireAt
		lost := r.speaker
		r.speaker = ""
		if head, ok := r.queue.popHead(); ok {
			r.speaker = head
			r.grantedAt = at
			r.expireAt = at + r.s
			if r.expiryHook != nil {
				r.expiryHook(lost, head, at)
			}
			continue
		}
		if r.expiryHook != nil {
			r.expiryHook(lost, "", at)
		}
		break
	}
}

// grantFromQueue 把队首设为发言者，授予时刻取 grantAt。
func (r *Room) grantFromQueue(grantAt int64) bool {
	head, ok := r.queue.popHead()
	if !ok {
		return false
	}
	r.speaker = head
	r.grantedAt = grantAt
	r.expireAt = grantAt + r.s
	return true
}

func (r *Room) Join(user string, now int64) error {
	if user == "" || !validTime(now) {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	if r.clockSet && now < r.lastNow {
		r.mu.Unlock()
		return ErrClockBack
	}
	r.advance(now)
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if _, exists := r.byID[user]; exists {
		r.mu.Unlock()
		return ErrAlreadyInRoom
	}
	role := RoleAttendee
	if len(r.members) == 0 {
		role = RoleHost
		r.host = user
	}
	m := &member{user: user, role: role, joinedAt: now}
	r.byID[user] = m
	r.members = append(r.members, m)
	r.mu.Unlock()
	return nil
}

func (r *Room) Raise(user string, now int64) error {
	m, err := r.begin(user, now)
	if err != nil {
		return err
	}
	switch {
	case r.speaker == user:
		err = ErrAlreadySpeaker
	case m.muted:
		err = ErrRaisedWhileMuted
	case r.queue.contains(user):
		err = ErrAlreadyRaised
	case r.queue.len() >= r.cap:
		err = ErrQueueFull
	default:
		r.nextTicket++
		r.queue.push(user, r.nextTicket)
	}
	r.mu.Unlock()
	return err
}

func (r *Room) Lower(user string, now int64) error {
	if _, err := r.begin(user, now); err != nil {
		return err
	}
	if !r.queue.remove(user) {
		r.mu.Unlock()
		return ErrNotInQueue
	}
	r.mu.Unlock()
	return nil
}

// Appoint 把 target 任命为协管员。仅主持人可调用。
func (r *Room) Appoint(operator, target string, now int64) error {
	return r.changeRole(operator, target, now, true)
}

// Dismiss 撤销 target 的协管员身份。仅主持人可调用。
func (r *Room) Dismiss(operator, target string, now int64) error {
	return r.changeRole(operator, target, now, false)
}

func (r *Room) changeRole(operator, target string, now int64, appoint bool) error {
	if target == "" {
		return ErrInvalidArgument
	}
	m, err := r.begin(operator, now)
	if err != nil {
		return err
	}
	if m.role != RoleHost {
		r.mu.Unlock()
		return ErrPermissionDenied
	}
	t := r.byID[target]
	if t == nil {
		r.mu.Unlock()
		return ErrTargetNotFound
	}
	switch {
	case t.role == RoleHost:
		err = ErrIsHost
	case appoint && t.role == RoleCoManager:
		err = ErrRoleUnchanged
	case !appoint && t.role == RoleAttendee:
		err = ErrRoleUnchanged
	case appoint:
		t.role = RoleCoManager
	default:
		t.role = RoleAttendee
	}
	r.mu.Unlock()
	return err
}

func (r *Room) Grant(operator string, now int64) error {
	m, err := r.begin(operator, now)
	if err != nil {
		return err
	}
	if m.role != RoleHost && m.role != RoleCoManager {
		r.mu.Unlock()
		return ErrPermissionDenied
	}
	switch {
	case r.speaker != "":
		err = ErrSpeakerActive
	case r.queue.len() == 0:
		err = ErrQueueEmpty
	default:
		r.grantFromQueue(now)
	}
	r.mu.Unlock()
	return err
}

func (r *Room) Yield(user string, now int64) error {
	if _, err := r.begin(user, now); err != nil {
		return err
	}
	if r.speaker != user {
		r.mu.Unlock()
		return ErrNotSpeaker
	}
	r.speaker = ""
	r.mu.Unlock()
	return nil
}

func (r *Room) Mute(operator, target string, now int64) error {
	return r.setMute(operator, target, now, true)
}

func (r *Room) Unmute(operator, target string, now int64) error {
	return r.setMute(operator, target, now, false)
}

func (r *Room) setMute(operator, target string, now int64, mute bool) error {
	if target == "" {
		return ErrInvalidArgument
	}
	m, err := r.begin(operator, now)
	if err != nil {
		return err
	}
	if m.role != RoleHost {
		r.mu.Unlock()
		return ErrPermissionDenied
	}
	t := r.byID[target]
	if t == nil {
		r.mu.Unlock()
		return ErrTargetNotFound
	}
	switch {
	case t.role == RoleHost:
		err = ErrIsHost
	case mute && t.muted:
		err = ErrAlreadyMuted
	case !mute && !t.muted:
		err = ErrNotMuted
	default:
		t.muted = mute
		if mute {
			if r.speaker == target {
				r.speaker = ""
				r.grantFromQueue(now)
			}
			r.queue.remove(target)
		}
	}
	r.mu.Unlock()
	return err
}

func (r *Room) Leave(user string, now int64) error {
	m, err := r.begin(user, now)
	if err != nil {
		return err
	}
	wasHost := m.role == RoleHost

	r.queue.remove(user)
	if r.speaker == user {
		r.speaker = ""
		r.grantFromQueue(now)
	}
	delete(r.byID, user)
	filtered := r.members[:0]
	for _, e := range r.members {
		if e.user != user {
			filtered = append(filtered, e)
		}
	}
	r.members = filtered

	if wasHost {
		r.transferHostOrClose()
	}
	r.mu.Unlock()
	return nil
}

// transferHostOrClose 主持人离开后：协管员中最早加入者，
// 否则其余成员中最早加入者，否则关闭会议室。移交不动发言权与队列。
func (r *Room) transferHostOrClose() {
	var successor *member
	for _, e := range r.members {
		if e.role == RoleCoManager {
			successor = e
			break
		}
	}
	if successor == nil {
		if len(r.members) == 0 {
			r.close()
			return
		}
		successor = r.members[0]
	}
	successor.role = RoleHost
	r.host = successor.user
}

func (r *Room) close() {
	r.closed = true
	r.host = ""
	r.speaker = ""
	r.grantedAt = 0
	r.expireAt = 0
	r.queue = newHandQueue()
	r.members = nil
	r.byID = map[string]*member{}
}

// Snapshot 返回惰性到期处理之后的完整状态。Remaining 为发言剩余秒数
// （到期时刻 - now）；无发言者时为 0。
func (r *Room) Snapshot(now int64) (Snapshot, error) {
	if !validTime(now) {
		return Snapshot{}, ErrInvalidArgument
	}
	r.mu.Lock()
	if r.clockSet && now < r.lastNow {
		r.mu.Unlock()
		return Snapshot{}, ErrClockBack
	}
	r.advance(now)
	if r.closed {
		r.mu.Unlock()
		return Snapshot{}, ErrClosed
	}
	snap := Snapshot{
		Host:    r.host,
		Speaker: r.speaker,
		Queue:   r.queue.ordered(),
		Members: make([]MemberInfo, 0, len(r.members)),
	}
	if r.speaker != "" {
		snap.Remaining = r.expireAt - now
	}
	for _, e := range r.members {
		snap.Members = append(snap.Members, MemberInfo{
			User:     e.user,
			Role:     e.role,
			Muted:    e.muted,
			JoinedAt: e.joinedAt,
		})
	}
	r.mu.Unlock()
	return snap, nil
}

// QueuePos 返回 user 的 1 基队列名次。该查询与其它操作一样先做惰性处理。
func (r *Room) QueuePos(user string, now int64) (int, error) {
	if user == "" {
		return 0, ErrInvalidArgument
	}
	if !validTime(now) {
		return 0, ErrInvalidArgument
	}
	r.mu.Lock()
	if r.clockSet && now < r.lastNow {
		r.mu.Unlock()
		return 0, ErrClockBack
	}
	r.advance(now)
	if r.closed {
		r.mu.Unlock()
		return 0, ErrClosed
	}
	if _, ok := r.byID[user]; !ok {
		r.mu.Unlock()
		return 0, ErrTargetNotFound
	}
	pos, ok := r.queue.position(user)
	r.mu.Unlock()
	if !ok {
		return 0, ErrNotInQueue
	}
	return pos, nil
}
