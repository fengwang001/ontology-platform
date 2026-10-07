package meeting

import "sync"

// Room 是在线会议室发言权控制服务。
// 所有导出方法都可在任意 goroutine 上并发调用；
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Room struct {
	mu sync.Mutex

	speakSecs int64 // 单次发言时限 S
	queueCap  int   // 举手队列容量 Q

	clock  int64 // 最近一次通过参数与时钟检查的操作的 now
	closed bool

	members     map[string]*member
	joinCounter uint64

	speaker string // 当前发言者，空串表示无人发言
	grantAt int64  // 当前发言权的授予时刻；到期时刻为 grantAt + speakSecs

	queue *handQueue

	expiryRounds uint64 // 惰性到期处理累计轮数（测试探针）
}

type member struct {
	role    Role
	muted   bool
	joinOrd uint64 // 加入次序，单调递增，用于主持人移交
}

// NewRoom 创建会议室。speakSecs ∈ [1, 3600]，queueCap ∈ [1, 500]。
func NewRoom(speakSecs int64, queueCap int) (*Room, error) {
	if speakSecs < MinSpeakSecs || speakSecs > MaxSpeakSecs {
		return nil, reject(CatInvalidParam, "speak_secs_out_of_range")
	}
	if queueCap < MinQueueCap || queueCap > MaxQueueCap {
		return nil, reject(CatInvalidParam, "queue_cap_out_of_range")
	}
	return &Room{
		speakSecs: speakSecs,
		queueCap:  queueCap,
		clock:     -1, // 尚未接受任何操作；now >= 0 恒不回退
		members:   make(map[string]*member),
		// 种子只取决于房间配置，保证重放时 Treap 形态一致。
		queue: newHandQueue(uint64(speakSecs)<<32 | uint64(queueCap)),
	}, nil
}

// prologue 是每个带 now 操作的前置检查与惰性处理：
// 参数（时刻）非法或时钟回退时不做任何处理；
// 通过后先处理全部已到期发言权，再把时钟推进到 now。
// 后续检查（已关闭、权限等）即使拒绝，上述处理也已生效。
func (r *Room) prologue(now int64) *Reject {
	if now < 0 || now > MaxNow {
		return reject(CatInvalidParam, ReasonNowOutOfRange)
	}
	if now < r.clock {
		return reject(CatClockRegression, ReasonClockBackwards)
	}
	r.advance(now)
	return nil
}

// advance 处理全部已到期的发言权，并把时钟推进到 now。
// 处理一次到期：发言者在到期时刻 t 失去发言权，队首（若有）
// 在同一时刻 t 自动获得发言权（授予时刻取 t 而非 now），
// 因此可能连续触发多轮，直至发言者未到期或队列为空。
// 开销只与实际发生的到期轮数成正比，与成员总数无关。
func (r *Room) advance(now int64) {
	for r.speaker != "" && r.grantAt+r.speakSecs <= now {
		r.expiryRounds++
		t := r.grantAt + r.speakSecs
		next, ok := r.queue.popFront()
		if !ok {
			r.speaker = ""
			break
		}
		r.speaker = next
		r.grantAt = t
	}
	r.clock = now
}

// autoGrant 把发言权在时刻 now 自动授予队首（若有）。
// 用于发言者被静音、发言者离开时的顺延。
func (r *Room) autoGrant(now int64) {
	if next, ok := r.queue.popFront(); ok {
		r.speaker = next
		r.grantAt = now
	}
}

// transferHost 在主持人离开后移交主持人身份：
// 优先移交给加入最早的协管员，否则移交给加入最早的其余成员，
// 无其他成员则关闭会议室。移交不改变发言权与队列。
func (r *Room) transferHost() {
	pick := ""
	pickOrd := uint64(0)
	found := false
	// 第一优先：加入最早的协管员。
	for u, m := range r.members {
		if m.role == RoleCoHost && (!found || m.joinOrd < pickOrd) {
			pick, pickOrd, found = u, m.joinOrd, true
		}
	}
	// 第二优先：加入最早的其余成员。
	if !found {
		for u, m := range r.members {
			if !found || m.joinOrd < pickOrd {
				pick, pickOrd, found = u, m.joinOrd, true
			}
		}
	}
	if !found {
		r.closed = true
		return
	}
	r.members[pick].role = RoleHost
}
