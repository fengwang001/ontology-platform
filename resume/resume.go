// Package resume 管理玩家会话、重连令牌与补帧计划。
package resume

import (
	"errors"
	"sync"

	"ontology/frames"
	"ontology/snap"
)

// 参数边界。
const (
	MaxNow  int64  = 1_000_000_000_000
	MaxSize uint64 = 1_000_000
	MaxP           = 10_000
	MaxK           = 1_000_000
	MaxC           = 1_000_000
	MaxG    int64  = 1_000_000_000
	MaxN           = 64
)

var (
	// ErrInvalid 参数非法（含 have/a 为负、size 越界、player 为空、now 越界）。
	ErrInvalid = errors.New("resume: invalid argument")
	// ErrClock 时钟回退：now 小于已接受操作的最大 now。
	ErrClock = errors.New("resume: clock moved backwards")
	// ErrNotFound 玩家不存在。
	ErrNotFound = errors.New("resume: player not found")
	// ErrState 状态不符。
	ErrState = errors.New("resume: player state mismatch")
	// ErrToken 重连令牌与当前 gen 不符。
	ErrToken = errors.New("resume: reconnect token mismatch")
	// ErrAhead 确认帧号或 have 超前于 cur。
	ErrAhead = errors.New("resume: frame number ahead of current")
	// ErrFull 在场玩家数已达上限。
	ErrFull = errors.New("resume: player roster full")
)

// Kind 是补帧计划类型。
type Kind int

const (
	None         Kind = iota // 已与最新帧同步
	Delta                    // 走增量补帧
	SnapshotKind             // 走快照 + 尾部增量
)

func (k Kind) String() string {
	switch k {
	case Delta:
		return "Delta"
	case SnapshotKind:
		return "Snapshot"
	default:
		return "None"
	}
}

// Plan 是一次重连的补帧计划。
type Plan struct {
	Kind     Kind
	SnapTick int64  // 仅 Kind==SnapshotKind 时有效
	From     int64  // 补帧区间起点（含）
	To       int64  // 补帧区间终点（含）；From>To 表示区间为空
	Bytes    uint64 // 区间各帧 size 之和；Snapshot 另加快照大小
}

// session 是单个玩家的会话状态。
type session struct {
	online bool
	gen    int
	ack    int64
	discAt int64 // 仅在 online=false 时有意义
}

// Planner 组合帧缓冲、快照与玩家会话。
type Planner struct {
	mu      sync.Mutex
	p       int
	c       int
	g       int64
	n       int
	maxNow  int64
	ring    *frames.Ring
	snaps   *snap.Store
	players map[string]*session

	// lastReason 记录最近一次裁决依据，供测试与日志使用。
	lastReason string
}

// New 构造规划器。P∈[1,1e4]，K∈[P,1e6]，C∈[0,1e6]，G∈[1,1e9]，N∈[1,64]。
// 参数非法直接 panic：构造期错误属于编程错误，调用方无法从返回错误恢复。
func New(p, k, c int, g int64, n int) *Planner {
	if p < 1 || p > MaxP || k < p || k > MaxK || c < 0 || c > MaxC ||
		g < 1 || g > MaxG || n < 1 || n > MaxN {
		panic("resume: invalid constructor parameters")
	}
	return &Planner{
		p:       p,
		c:       c,
		g:       g,
		n:       n,
		ring:    frames.New(k),
		snaps:   &snap.Store{},
		players: map[string]*session{},
	}
}

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// presentAt 判定某会话在时刻 now 是否“在场”（在线或宽限期内断线）。
// 纯谓词，不修改状态，保证被拒操作不产生副作用。
func (pl *Planner) presentAt(s *session, now int64) bool {
	if s.online {
		return true
	}
	return now < s.discAt+pl.g
}

// rangeBytes 求缓冲内 [from,to]（含）各帧 size 之和，空区间为 0。
// 非空时至多点读两次端点帧；from-1 若已被淘汰则直接用淘汰前缀和，
// 使一次计划触碰的帧记录数不超过 2，且与区间长度、K 无关。
func (pl *Planner) rangeBytes(from, to int64) uint64 {
	if from > to {
		return 0
	}
	toRec, ok := pl.ring.Read(to)
	if !ok {
		return 0
	}
	var head uint64
	if from > 1 {
		if from-1 >= pl.ring.Low() {
			if rec, ok := pl.ring.Read(from - 1); ok {
				head = rec.Sum
			}
		} else {
			head = pl.ring.EvictedPrefix()
		}
	}
	return toRec.Sum - head
}

// Append 追加下一帧；帧号为 P 的倍数时生成快照。
func (pl *Planner) Append(now int64, size uint64) error {
	if !validNow(now) || size > MaxSize {
		pl.lastReason = "reject: invalid argument"
		return ErrInvalid
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if now < pl.maxNow {
		pl.lastReason = "reject: clock moved backwards"
		return ErrClock
	}
	rec := pl.ring.Append(now, size)
	pl.maxNow = now
	if rec.Tick%int64(pl.p) == 0 {
		window := pl.ring.Window(rec.Tick-int64(pl.p)+1, rec.Tick)
		pl.snaps.Observe(rec.Tick, window)
		pl.lastReason = "append frame and snapshot"
	} else {
		pl.lastReason = "append frame"
	}
	return nil
}

// Join 登记玩家在线；首次加入 gen=0，离场后再加入 gen+1、ack=0。
func (pl *Planner) Join(now int64, player string) error {
	if !validNow(now) || player == "" {
		pl.lastReason = "reject: invalid argument"
		return ErrInvalid
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if now < pl.maxNow {
		pl.lastReason = "reject: clock moved backwards"
		return ErrClock
	}
	s := pl.players[player]
	if s != nil && pl.presentAt(s, now) {
		pl.lastReason = "reject: player still present"
		return ErrState
	}
	count := 0
	for _, other := range pl.players {
		if other != s && pl.presentAt(other, now) {
			count++
		}
	}
	if count >= pl.n {
		pl.lastReason = "reject: roster full"
		return ErrFull
	}
	if s == nil {
		s = &session{}
		pl.players[player] = s
	} else {
		s.gen++ // 离场后重新加入
	}
	s.online = true
	s.ack = 0
	s.discAt = 0
	pl.maxNow = now
	pl.lastReason = "join accepted"
	return nil
}

// Ack 记录玩家确认帧号；a 小于已记录 ack 时成功但不改字段。
func (pl *Planner) Ack(now int64, player string, a int64) error {
	if !validNow(now) || player == "" || a < 0 {
		pl.lastReason = "reject: invalid argument"
		return ErrInvalid
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if now < pl.maxNow {
		pl.lastReason = "reject: clock moved backwards"
		return ErrClock
	}
	s := pl.players[player]
	if s == nil {
		pl.lastReason = "reject: player not found"
		return ErrNotFound
	}
	if !s.online {
		pl.lastReason = "reject: player not online"
		return ErrState
	}
	if a > pl.ring.Cur() {
		pl.lastReason = "reject: ack ahead of cur"
		return ErrAhead
	}
	pl.maxNow = now
	if a < s.ack {
		pl.lastReason = "accepted: stale ack, fields unchanged"
		return nil
	}
	s.ack = a
	pl.lastReason = "accepted: ack recorded"
	return nil
}

// Disconnect 将在线玩家置为断线，返回当前 gen 作为重连令牌。
func (pl *Planner) Disconnect(now int64, player string) (int, error) {
	if !validNow(now) || player == "" {
		pl.lastReason = "reject: invalid argument"
		return 0, ErrInvalid
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if now < pl.maxNow {
		pl.lastReason = "reject: clock moved backwards"
		return 0, ErrClock
	}
	s := pl.players[player]
	if s == nil {
		pl.lastReason = "reject: player not found"
		return 0, ErrNotFound
	}
	if !s.online {
		pl.lastReason = "reject: player not online"
		return 0, ErrState
	}
	s.online = false
	s.discAt = now
	pl.maxNow = now
	pl.lastReason = "accepted: disconnected, token is current gen"
	return s.gen, nil
}

// Reconnect 校验令牌并产出补帧计划。成功后玩家在线、gen+1、ack=have。
func (pl *Planner) Reconnect(now int64, player string, token int, have int64) (Plan, error) {
	if !validNow(now) || player == "" || have < 0 {
		pl.lastReason = "reject: invalid argument"
		return Plan{}, ErrInvalid
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if now < pl.maxNow {
		pl.lastReason = "reject: clock moved backwards"
		return Plan{}, ErrClock
	}
	s := pl.players[player]
	if s == nil {
		pl.lastReason = "reject: player not found"
		return Plan{}, ErrNotFound
	}
	if s.online || !pl.presentAt(s, now) {
		pl.lastReason = "reject: player online or already departed"
		return Plan{}, ErrState
	}
	if token != s.gen {
		pl.lastReason = "reject: stale reconnect token"
		return Plan{}, ErrToken
	}
	cur := pl.ring.Cur()
	if have > cur {
		pl.lastReason = "reject: have ahead of cur"
		return Plan{}, ErrAhead
	}
	s.online = true
	s.discAt = 0
	s.gen++
	s.ack = have
	pl.maxNow = now
	pl.ring.ResetTouched()

	plan := Plan{From: have + 1, To: cur}
	switch {
	case have == cur:
		plan.Kind = None
		pl.lastReason = "plan None: have equals cur"
	default:
		n1 := cur - have
		snapRec, hasSnap := pl.snaps.Latest()
		buffered := have+1 >= pl.ring.Low()
		cheaperOrEqual := !hasSnap || have >= snapRec.Tick ||
			n1 <= cur-snapRec.Tick+int64(pl.c)
		if buffered && cheaperOrEqual {
			plan.Kind = Delta
			plan.Bytes = pl.rangeBytes(have+1, cur)
			pl.lastReason = "plan Delta: gap in buffer and n1 <= (cur-s)+C (ties go Delta)"
		} else {
			plan.Kind = SnapshotKind
			plan.SnapTick = snapRec.Tick
			plan.From = snapRec.Tick + 1
			plan.To = cur
			plan.Bytes = pl.rangeBytes(plan.From, cur) + snapRec.Size
			if buffered {
				pl.lastReason = "plan Snapshot: delta cost exceeds snapshot-equivalent cost"
			} else {
				pl.lastReason = "plan Snapshot: gap frame already evicted from buffer"
			}
		}
	}
	return plan, nil
}

// Cur 返回最新帧号。
func (pl *Planner) Cur() int64 {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.ring.Cur()
}

// Low 返回缓冲内最旧帧号。
func (pl *Planner) Low() int64 {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.ring.Low()
}

// Touched 返回上次 Reconnect 读取的帧记录数。
func (pl *Planner) Touched() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.ring.Touched()
}

// LastReason 返回最近一次操作的裁决依据（测试/日志用）。
func (pl *Planner) LastReason() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.lastReason
}
