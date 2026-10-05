// Package resume 管理玩家会话、重连令牌与补帧计划。
package resume

import (
	"errors"
	"sync"

	"ontology/frames"
	"ontology/snap"
)

// 拒绝原因，按规定的拒绝次序排列：
var (
	ErrInvalidArg   = errors.New("invalid argument")
	ErrClockRewind  = errors.New("clock rewind")
	ErrNoPlayer     = errors.New("player not found")
	ErrInvalidState = errors.New("invalid player state")
	ErrRoomFull     = errors.New("room full")
	ErrBadToken     = errors.New("token mismatch")
	ErrAhead        = errors.New("ahead of current frame")
)

// Kind 为补帧计划类型。
type Kind int

const (
	None Kind = iota
	Delta
	Snapshot
)

// Plan 描述一次重连后的补帧计划（骨架）。
type Plan struct {
	Kind     Kind
	SnapTick int
	From     int
	To       int
	Bytes    int64
}

type player struct {
	online bool
	ack    int
	gen    int
	discAt int64 // 最近一次断线时刻
}

// Planner 是重连规划器；一把互斥锁保证并发操作等价于某种串行顺序。
type Planner struct {
	mu sync.Mutex

	period int
	equiv  int
	grace  int64
	capN   int

	now     int64
	ring    *frames.Ring
	snaps   *snap.Store
	players map[string]*player
}

// New 创建规划器。
func New(p, k, c int, g int64, n int) *Planner {
	if p < 1 || p > 10000 ||
		k < p || k > 1_000_000 ||
		c < 0 || c > 1_000_000 ||
		g < 1 || g > 1_000_000_000 ||
		n < 1 || n > 64 {
		panic(ErrInvalidArg)
	}
	return &Planner{
		period:  p,
		equiv:   c,
		grace:   g,
		capN:    n,
		ring:    frames.New(k),
		snaps:   snap.New(p),
		players: make(map[string]*player),
	}
}

// Append 追加下一帧。
func (q *Planner) Append(now int64, size int) error {
	if size < 0 || size > 1_000_000 {
		return ErrInvalidArg
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return err
	}
	q.ring.Append(size)
	cur := q.ring.Cur()
	if cur%q.period == 0 {
		q.snaps.Observe(cur, q.ring.RangeSum(cur-q.period+1, cur))
	}
	q.now = now
	return nil
}

// Join 登记新玩家或离场后重新加入。
func (q *Planner) Join(now int64, playerName string) error {
	if playerName == "" {
		return ErrInvalidArg
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return err
	}
	if p, ok := q.players[playerName]; ok && q.present(p, now) {
		return ErrInvalidState
	}
	if q.presentCount(now) >= q.capN {
		return ErrRoomFull
	}
	p := q.players[playerName]
	if p == nil {
		p = &player{}
		q.players[playerName] = p
	} else {
		p.gen++
	}
	p.online = true
	p.ack = 0
	q.now = now
	return nil
}

// Ack 记录在线玩家的确认帧号；回退静默成功但不改字段。
func (q *Planner) Ack(now int64, playerName string, a int) error {
	if playerName == "" || a < 0 {
		return ErrInvalidArg
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return err
	}
	p, ok := q.players[playerName]
	if !ok {
		return ErrNoPlayer
	}
	if !q.present(p, now) {
		return ErrInvalidState
	}
	if !p.online {
		return ErrInvalidState
	}
	if a > q.ring.Cur() {
		return ErrAhead
	}
	if a > p.ack {
		p.ack = a
	}
	q.now = now
	return nil
}

// Disconnect 将在线玩家置为断线并返回当前 gen 作为重连令牌。
func (q *Planner) Disconnect(now int64, playerName string) (int, error) {
	if playerName == "" {
		return 0, ErrInvalidArg
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return 0, err
	}
	p, ok := q.players[playerName]
	if !ok {
		return 0, ErrNoPlayer
	}
	if !q.present(p, now) {
		return 0, ErrInvalidState
	}
	if !p.online {
		return 0, ErrInvalidState
	}
	p.online = false
	p.discAt = now
	q.now = now
	return p.gen, nil
}

// Reconnect 校验令牌并生成补帧计划。
func (q *Planner) Reconnect(now int64, playerName string, token, have int) (Plan, error) {
	if playerName == "" || have < 0 {
		return Plan{}, ErrInvalidArg
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return Plan{}, err
	}
	p, ok := q.players[playerName]
	if !ok {
		return Plan{}, ErrNoPlayer
	}
	if !q.present(p, now) {
		return Plan{}, ErrInvalidState
	}
	if p.online {
		return Plan{}, ErrInvalidState
	}
	if token != p.gen {
		return Plan{}, ErrBadToken
	}
	cur := q.ring.Cur()
	if have > cur {
		return Plan{}, ErrAhead
	}

	plan := q.makePlan(have, cur)
	p.online = true
	p.gen++
	p.ack = have
	q.now = now
	return plan, nil
}

// makePlan 在锁内依据规则推导补帧计划。
func (q *Planner) makePlan(have, cur int) Plan {
	if have == cur {
		return Plan{Kind: None, From: cur + 1, To: cur}
	}
	n1 := cur - have
	s := q.snaps.Tick()
	low := q.ring.Low()
	if have+1 >= low && (s == 0 || have >= s || n1 <= (cur-s)+q.equiv) {
		return Plan{
			Kind:  Delta,
			From:  have + 1,
			To:    cur,
			Bytes: q.ring.RangeSum(have+1, cur),
		}
	}
	return Plan{
		Kind:     Snapshot,
		SnapTick: s,
		From:     s + 1,
		To:       cur,
		Bytes:    q.snaps.Size() + q.ring.RangeSum(s+1, cur),
	}
}

func (q *Planner) checkClock(now int64) error {
	if now < q.now {
		return ErrClockRewind
	}
	return nil
}

// present 报告玩家是否在场（在线，或断线仍在宽限期内，取等离场）。
func (q *Planner) present(p *player, now int64) bool {
	return p.online || now < p.discAt+q.grace
}

func (q *Planner) presentCount(now int64) int {
	count := 0
	for _, p := range q.players {
		if q.present(p, now) {
			count++
		}
	}
	return count
}
