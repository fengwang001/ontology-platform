// Package resume 实现对局帧流的断线重连与补帧规划：
// 玩家会话、重连令牌、宽限期离场，以及增量/快照补帧计划。
//
// 所有操作可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；
// 相同操作序列重放得到相同计划。被拒绝的操作不改任何状态，也不推进时钟。
//
// 拒绝次序：参数非法 > 时钟回退 > 玩家不存在 > 状态不符 > 令牌不符 > 超前。
// Join 在状态不符（仍在场）之后才判人数已满。
package resume

import (
	"errors"
	"fmt"
	"sync"

	"ontology/frames"
	"ontology/snap"
)

// 拒绝原因（哨兵错误，可用 errors.Is 判定）。
var (
	ErrParam    = errors.New("resume: 参数非法")
	ErrClock    = errors.New("resume: 时钟回退")
	ErrNoPlayer = errors.New("resume: 玩家不存在")
	ErrState    = errors.New("resume: 状态不符")
	ErrFull     = errors.New("resume: 人数已满")
	ErrToken    = errors.New("resume: 令牌不符")
	ErrAhead    = errors.New("resume: 超前")
)

const (
	maxNow    = int64(1_000_000_000_000) // now ∈ [0, 1e12]
	maxSize   = int64(1_000_000)          // size ∈ [0, 1e6]
	maxPlayer = 64
)

// Kind 是补帧计划类型。
type Kind int

const (
	None     Kind = iota // have == cur，无需补帧
	Delta                // 增量补帧 [have+1, cur]
	Snapshot             // 快照 s 加增量 [s+1, cur]
)

func (k Kind) String() string {
	switch k {
	case None:
		return "None"
	case Delta:
		return "Delta"
	case Snapshot:
		return "Snapshot"
	}
	return "Unknown"
}

// Plan 是一次成功 Reconnect 产生的补帧计划。
// 补帧区间为 [From, To]（From > To 表示空区间），区间内每帧恰出现一次。
type Plan struct {
	Kind     Kind
	SnapTick int64 // 仅 Kind=Snapshot 有效，快照帧号 s
	From     int64
	To       int64
	Bytes    int64 // 区间内各帧 size 之和；Snapshot 另加快照大小
}

func (p Plan) String() string {
	if p.Kind == Snapshot {
		return fmt.Sprintf("%s(snap=%d,[%d,%d],bytes=%d)", p.Kind, p.SnapTick, p.From, p.To, p.Bytes)
	}
	return fmt.Sprintf("%s([%d,%d],bytes=%d)", p.Kind, p.From, p.To, p.Bytes)
}

type playerState struct {
	online bool
	ack    int64
	gen    int64
	discAt int64 // 断线时刻 t
}

// PlayerView 是玩家状态的只读快照（测试与观测用）。
type PlayerView struct {
	Exists bool
	Online bool
	Ack    int64
	Gen    int64
}

// System 是帧流断线重连规划器，组合 frames 环形缓冲与 snap 周期快照。
type System struct {
	mu      sync.Mutex
	p       int64 // 快照周期（帧）
	k       int64 // 缓冲容量（帧）
	c       int64 // 快照等价代价（帧）
	g       int64 // 重连宽限（毫秒）
	n       int64 // 在场玩家上限
	maxNow  int64 // 已接受操作的最大 now
	buf     *frames.Buffer
	snaps   *snap.Tracker
	players map[string]*playerState
}

// New 创建规划器。参数非法（含 K < P）返回 ErrParam。
// P ∈ [1, 1e4]，K ∈ [P, 1e6]，C ∈ [0, 1e6]，G ∈ [1, 1e9]，N ∈ [1, 64]。
func New(p, k, c, g, n int64) (*System, error) {
	if p < 1 || p > 10_000 ||
		k < p || k > 1_000_000 ||
		c < 0 || c > 1_000_000 ||
		g < 1 || g > 1_000_000_000 ||
		n < 1 || n > maxPlayer {
		return nil, ErrParam
	}
	return &System{
		p:       p,
		k:       k,
		c:       c,
		g:       g,
		n:       n,
		maxNow:  -1,
		buf:     frames.NewBuffer(k),
		snaps:   snap.NewTracker(p),
		players: make(map[string]*playerState),
	}, nil
}

// AppendFrame 追加下一帧（帧号从 1 连续递增），必要时生成快照。
func (s *System) AppendFrame(now, size int64) error {
	if now < 0 || now > maxNow || size < 0 || size > maxSize {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clockLocked(now); err != nil {
		return err
	}
	cur := s.buf.Append(size)
	s.snaps.Consider(cur, func() int64 {
		return s.buf.Sum(cur-s.p+1, cur)
	})
	s.maxNow = now
	return nil
}

// Join 把玩家登记为在线、ack=0；首次加入 gen=0，离场后重新加入 gen 加 1。
func (s *System) Join(now int64, player string) error {
	if now < 0 || now > maxNow || player == "" {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clockLocked(now); err != nil {
		return err
	}
	p, exists := s.players[player]
	if exists && s.presentLocked(p, now) {
		return ErrState // 玩家仍在场
	}
	if s.presentCountLocked(now) >= s.n {
		return ErrFull
	}
	if exists {
		p.gen++
		p.ack = 0
		p.online = true
	} else {
		s.players[player] = &playerState{online: true}
	}
	s.maxNow = now
	return nil
}

// Ack 上报确认帧号。a > cur 报超前；a 小于已记录 ack 时成功但不改任何字段。
func (s *System) Ack(now int64, player string, a int64) error {
	if now < 0 || now > maxNow || player == "" || a < 0 {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clockLocked(now); err != nil {
		return err
	}
	p, ok := s.players[player]
	if !ok {
		return ErrNoPlayer
	}
	if !p.online {
		return ErrState
	}
	if a > s.buf.Cur() {
		return ErrAhead
	}
	if a > p.ack {
		p.ack = a
	}
	s.maxNow = now
	return nil
}

// Disconnect 断开在线玩家，记下断线时刻并返回当前 gen 作为重连令牌。
func (s *System) Disconnect(now int64, player string) (int64, error) {
	if now < 0 || now > maxNow || player == "" {
		return 0, ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clockLocked(now); err != nil {
		return 0, err
	}
	p, ok := s.players[player]
	if !ok {
		return 0, ErrNoPlayer
	}
	if !p.online {
		return 0, ErrState
	}
	p.online = false
	p.discAt = now
	s.maxNow = now
	return p.gen, nil
}

// Reconnect 用令牌重连并给出补帧计划。成功后置为在线、gen 加 1、ack 置为
// have（ack 唯一可以变小之处；have 大于旧 ack 也允许，说明确认丢失）。
func (s *System) Reconnect(now int64, player string, token, have int64) (Plan, error) {
	if now < 0 || now > maxNow || player == "" || have < 0 {
		return Plan{}, ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clockLocked(now); err != nil {
		return Plan{}, err
	}
	p, ok := s.players[player]
	if !ok {
		return Plan{}, ErrNoPlayer
	}
	if p.online || s.departedLocked(p, now) {
		return Plan{}, ErrState // 在线或已离场
	}
	if token != p.gen {
		return Plan{}, ErrToken
	}
	cur := s.buf.Cur()
	if have > cur {
		return Plan{}, ErrAhead
	}
	p.online = true
	p.gen++
	p.ack = have
	s.maxNow = now
	return s.planLocked(have, cur), nil
}

// planLocked 计算补帧计划：代价比较取等选增量（n1 ≤ (cur-s)+C 走 Delta）。
func (s *System) planLocked(have, cur int64) Plan {
	if have == cur {
		return Plan{Kind: None, From: cur + 1, To: cur}
	}
	low := s.buf.Low()
	sn, hasSnap := s.snaps.Latest()
	n1 := cur - have
	if have+1 >= low && (!hasSnap || have >= sn.Tick || n1 <= (cur-sn.Tick)+s.c) {
		return Plan{
			Kind:  Delta,
			From:  have + 1,
			To:    cur,
			Bytes: s.buf.Sum(have+1, cur),
		}
	}
	return Plan{
		Kind:     Snapshot,
		SnapTick: sn.Tick,
		From:     sn.Tick + 1,
		To:       cur,
		Bytes:    s.buf.Sum(sn.Tick+1, cur) + sn.Size,
	}
}

// Inspect 返回玩家状态只读视图（测试与观测用）。
func (s *System) Inspect(player string) PlayerView {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[player]
	if !ok {
		return PlayerView{}
	}
	return PlayerView{Exists: true, Online: p.online, Ack: p.ack, Gen: p.gen}
}

// Stream 返回当前流位置：最新帧号 cur 与缓冲内最旧帧号 low。
func (s *System) Stream() (cur, low int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Cur(), s.buf.Low()
}

// Snapshot 返回最新快照（测试与观测用）。
func (s *System) Snapshot() (snap.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snaps.Latest()
}

// FrameReads 返回帧缓冲区间查询累计读取的记录数（测试用）。
func (s *System) FrameReads() int64 { return s.buf.Touched() }

// ResetFrameReads 清零帧读取计数器（测试用）。
func (s *System) ResetFrameReads() { s.buf.ResetTouched() }

// clockLocked 校验时钟回退：now 不得小于已接受操作的最大 now。
func (s *System) clockLocked(now int64) error {
	if now < s.maxNow {
		return ErrClock
	}
	return nil
}

// departedLocked 报告断线玩家是否已离场：now ≥ t+G（取等即离场）。
func (s *System) departedLocked(p *playerState, now int64) bool {
	return !p.online && now >= p.discAt+s.g
}

// presentLocked 报告玩家是否在场：在线或宽限期内的断线。
func (s *System) presentLocked(p *playerState, now int64) bool {
	return p.online || !s.departedLocked(p, now)
}

func (s *System) presentCountLocked(now int64) int64 {
	var count int64
	for _, p := range s.players {
		if s.presentLocked(p, now) {
			count++
		}
	}
	return count
}
