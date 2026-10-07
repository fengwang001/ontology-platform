// Package txsched 实现传输层发送端的小段合并调度器。
//
// 调度器在应用写入、对端确认与窗口通告、选项变更、时间推进与关闭事件的
// 驱动下，决定何时把缓冲数据切成段发出。时钟由调用方注入：每个事件携带
// 发生时刻，调度器拒绝早于上一已接受事件时刻的事件（时钟回退）。
//
// 语义要点：
//   - 满段（长度等于 MSS）在窗口允许范围内总是立即发出。
//   - 小段（尾部不足 MSS，或被窗口截短而不足 MSS）默认按 Nagle 规则
//     保留：在途为空可立即发出，否则保留到在途清空或凑满一个满段。
//   - 不延迟选项取消上述保留；软木塞选项保留一切小段直到软木塞超时，
//     两个选项同时打开时软木塞优先。
//   - 关闭后忽略一切保留约束，仅按窗口约束把残余数据发完。
//   - 对端窗口为零、缓冲非空且在途为空时，按探测间隔发出 1 字节探测段。
//
// 并发：所有导出方法持有同一把互斥锁，任意并发调用等价于某个串行顺序。
//
// 复杂度：除输出（本次发出的段列表）本身外，每个事件的处理为均摊 O(1)。
// 缓冲区按写入批次组织为块队列，每块的入队、出队与压缩搬移均为均摊常数
// （见 Stats 计数器与 DESIGN.md 中的证明）。
package txsched

import (
	"errors"
	"math"
	"sync"
)

// 可区分的错误，按固定优先级检查：
// 参数非法 > 时钟回退 > 关闭后写入 > 缓冲已满 > 窗口收缩 > 确认越界。
var (
	ErrInvalidArg    = errors.New("txsched: invalid argument")
	ErrClockRollback = errors.New("txsched: clock moved backwards")
	ErrClosed        = errors.New("txsched: write after close")
	ErrBufferFull    = errors.New("txsched: buffer full")
	ErrWindowShrink  = errors.New("txsched: window shrank")
	ErrAckRange      = errors.New("txsched: ack out of range")
)

// Config 为调度器配置。MSS 即最大段长 M，BufferMax 即缓冲总量上限 B。
type Config struct {
	MSS           int   // 最大段长 M，必须为正
	BufferMax     int   // 缓冲总量上限 B，非负
	InitialWindow int   // 初始对端窗口，非负
	CorkTimeout   int64 // 软木塞超时（毫秒），非负
	ProbeInterval int64 // 零窗口探测间隔（毫秒），非负
}

func (c Config) valid() bool {
	return c.MSS >= 1 &&
		c.BufferMax >= 0 &&
		c.InitialWindow >= 0 &&
		c.CorkTimeout >= 0 &&
		c.ProbeInterval >= 0
}

// Kind 为事件种类。
type Kind int

const (
	KindWrite   Kind = iota // 应用写入 Bytes 字节
	KindAck                 // 对端确认：Bytes 为累计确认字节数，Window 为新通告窗口
	KindNoDelay             // 设置（On=true）或清除不延迟选项
	KindCork                // 设置（On=true）或清除软木塞选项
	KindTime                // 时间推进到 Time
	KindClose               // 关闭
)

// Event 为一个驱动事件。Time 为调用方注入的发生时刻（毫秒）。
type Event struct {
	Time   int64
	Kind   Kind
	Bytes  int  // Write：写入长度；Ack：累计确认字节数
	Window int  // Ack：新通告窗口
	On     bool // NoDelay/Cork：设置或清除
}

// Segment 为一次发出的段。
type Segment struct {
	Len   int   // 段长（字节）
	Time  int64 // 发出时刻
	Probe bool  // 是否为零窗口探测段
}

// Outcome 为事件处理结果。Err 非空表示事件被拒绝，且状态（含时钟）未变。
type Outcome struct {
	Segments []Segment
	Err      error
}

// Snapshot 为状态查询结果，查询不改变任何状态。
type Snapshot struct {
	InFlight  int   // 在途字节数（已发送未确认）
	Buffered  int   // 缓冲中尚未发出的字节数
	Reserved  int   // 缓冲中被保留的小段字节数
	Sent      int   // 累计已发送字节数
	Acked     int   // 累计已确认字节数
	Closed    bool  // 是否已关闭
	NoDelay   bool  // 不延迟选项
	Cork      bool  // 软木塞选项
	NextTimer int64 // 下一个需要时间推进才会触发动作的时刻
	HasTimer  bool  // NextTimer 是否有效
}

// Stats 为均摊复杂度证明用的内部计数器。
type Stats struct {
	Accepted     int // 已接受事件数
	Segments     int // 已发出段数
	ChunkPushes  int // 缓冲块入队次数
	ChunkPops    int // 缓冲块出队次数
	CompactMoves int // 队列压缩搬移的块数
}

// chunk 为一次写入在缓冲队列中的记录。
type chunk struct {
	tm  int64 // 进入缓冲的时刻
	len int
}

// Scheduler 为小段合并调度器。所有方法可并发调用。
type Scheduler struct {
	mu  sync.Mutex
	cfg Config

	now    int64 // 上一已接受事件的时刻
	hasNow bool

	closed  bool
	noDelay bool
	cork    bool

	sent      int // 已发送序号（含探测字节）
	acked     int // 累计确认字节数
	rightEdge int // 对端窗口右边缘，只增不减

	buffered int
	chunks   []chunk
	head     int

	reserved int // 不变式：reserved > 0 时必有 reserved == buffered

	zeroSet   bool  // 窗口为零且缓冲非空
	zeroSince int64 // 上述状态的开始时刻
	probed    bool
	lastProbe int64

	stats Stats
}

// New 校验配置并创建调度器。
func New(cfg Config) (*Scheduler, error) {
	if !cfg.valid() {
		return nil, ErrInvalidArg
	}
	return &Scheduler{cfg: cfg, rightEdge: cfg.InitialWindow}, nil
}

// Do 处理一个事件：校验、应用、随后立即执行发送决策。
// 被拒绝的事件不改变缓冲、在途、窗口与时钟。
func (s *Scheduler) Do(ev Event) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	switch ev.Kind {
	case KindWrite:
		if ev.Bytes <= 0 || ev.Bytes > math.MaxInt32 {
			return Outcome{Err: ErrInvalidArg}
		}
	case KindAck:
		if ev.Window < 0 {
			return Outcome{Err: ErrInvalidArg}
		}
	case KindNoDelay, KindCork, KindTime, KindClose:
	default:
		return Outcome{Err: ErrInvalidArg}
	}

	// 2. 时钟回退
	if s.hasNow && ev.Time < s.now {
		return Outcome{Err: ErrClockRollback}
	}

	// 3+. 按事件种类的拒绝检查（不修改任何状态）
	switch ev.Kind {
	case KindWrite:
		if s.closed {
			return Outcome{Err: ErrClosed}
		}
		if s.buffered+ev.Bytes > s.cfg.BufferMax {
			return Outcome{Err: ErrBufferFull}
		}
	case KindAck:
		// 右边缘不得收缩：ackNum+window < rightEdge 即收缩。
		// 用减法比较避免溢出（window >= 0 已校验）。
		if ev.Bytes < s.rightEdge-ev.Window {
			return Outcome{Err: ErrWindowShrink}
		}
		if ev.Bytes < s.acked || ev.Bytes-s.acked > s.inFlight() {
			return Outcome{Err: ErrAckRange}
		}
	}

	// 接受：推进时钟并应用变更
	s.now = ev.Time
	s.hasNow = true
	switch ev.Kind {
	case KindWrite:
		s.appendChunk(ev.Bytes)
	case KindAck:
		s.acked = ev.Bytes
		s.rightEdge = ev.Bytes + ev.Window
	case KindNoDelay:
		s.noDelay = ev.On
	case KindCork:
		s.cork = ev.On
	case KindClose:
		s.closed = true
	case KindTime:
	}

	segs := s.sendPass()
	s.stats.Accepted++
	s.stats.Segments += len(segs)
	return Outcome{Segments: segs}
}

// Snapshot 读取当前状态，不改变任何状态。
func (s *Scheduler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// Stats 读取内部计数器，用于均摊复杂度的可验证证明。
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Scheduler) inFlight() int { return s.sent - s.acked }

// frontTime 返回缓冲中最早字节的进入时刻，调用前须保证 buffered > 0。
func (s *Scheduler) frontTime() int64 { return s.chunks[s.head].tm }

func (s *Scheduler) appendChunk(n int) {
	if len(s.chunks) > s.head && s.chunks[len(s.chunks)-1].tm == s.now {
		s.chunks[len(s.chunks)-1].len += n
	} else {
		s.chunks = append(s.chunks, chunk{tm: s.now, len: n})
		s.stats.ChunkPushes++
	}
	s.buffered += n
}

// consume 从缓冲队首消费 n 字节。每个块只被出队一次，
// 压缩搬移总次数不超过出队次数（均摊 O(1)）。
func (s *Scheduler) consume(n int) {
	s.buffered -= n
	for n > 0 {
		c := &s.chunks[s.head]
		if c.len > n {
			c.len -= n
			n = 0
		} else {
			n -= c.len
			s.head++
			s.stats.ChunkPops++
		}
	}
	if s.head == len(s.chunks) {
		s.chunks = s.chunks[:0]
		s.head = 0
	} else if s.head >= 64 && s.head*2 >= len(s.chunks) {
		moved := copy(s.chunks, s.chunks[s.head:])
		s.chunks = s.chunks[:moved]
		s.head = 0
		s.stats.CompactMoves += moved
	}
}

// sendPass 执行发送决策：先发满段，再按规则处理小段，最后处理零窗口探测。
// 返回本次发出的各段，并维护 reserved 与零窗口跟踪状态。
func (s *Scheduler) sendPass() []Segment {
	var segs []Segment
	held := false
	for s.buffered > 0 {
		wnd := s.rightEdge - s.sent
		if wnd <= 0 {
			break
		}
		segLen := min(s.cfg.MSS, s.buffered, wnd)
		if segLen < s.cfg.MSS && !s.closed {
			// 小段（尾部不足 MSS 或被窗口截短）
			if s.cork {
				// 软木塞优先于不延迟；自最早被保留数据进入缓冲起满超时即解除
				if s.now < s.frontTime()+s.cfg.CorkTimeout {
					held = true
					break
				}
			} else if !s.noDelay && s.inFlight() > 0 {
				// 默认模式：在途不为空时保留
				held = true
				break
			}
		}
		segs = append(segs, Segment{Len: segLen, Time: s.now})
		s.consume(segLen)
		s.sent += segLen
	}
	if held {
		s.reserved = s.buffered
	} else {
		s.reserved = 0
	}

	// 零窗口探测：窗口为零且缓冲非空、在途为空时，
	// 自窗口变为零或上次探测起满探测间隔发出 1 字节探测段。
	s.trackZero()
	if s.zeroSet && s.inFlight() == 0 {
		base := s.zeroSince
		if s.probed && s.lastProbe > base {
			base = s.lastProbe
		}
		if s.now >= base+s.cfg.ProbeInterval {
			segs = append(segs, Segment{Len: 1, Time: s.now, Probe: true})
			s.consume(1)
			s.sent++
			s.lastProbe = s.now
			s.probed = true
			s.trackZero()
		}
	}
	return segs
}

// trackZero 维护“窗口为零且缓冲非空”状态的开始时刻。
func (s *Scheduler) trackZero() {
	if s.rightEdge-s.sent <= 0 && s.buffered > 0 {
		if !s.zeroSet {
			s.zeroSet = true
			s.zeroSince = s.now
		}
	} else {
		s.zeroSet = false
	}
}

func (s *Scheduler) snapshot() Snapshot {
	snap := Snapshot{
		InFlight: s.inFlight(),
		Buffered: s.buffered,
		Reserved: s.reserved,
		Sent:     s.sent,
		Acked:    s.acked,
		Closed:   s.closed,
		NoDelay:  s.noDelay,
		Cork:     s.cork,
	}
	if t, ok := s.nextTimer(); ok {
		snap.NextTimer = t
		snap.HasTimer = true
	}
	return snap
}

// nextTimer 返回软木塞到期与探测到期中较早者。
func (s *Scheduler) nextTimer() (int64, bool) {
	var best int64
	ok := false
	if s.cork && s.reserved > 0 {
		best = s.frontTime() + s.cfg.CorkTimeout
		ok = true
	}
	if s.zeroSet && s.inFlight() == 0 {
		base := s.zeroSince
		if s.probed && s.lastProbe > base {
			base = s.lastProbe
		}
		if t := base + s.cfg.ProbeInterval; !ok || t < best {
			best = t
			ok = true
		}
	}
	return best, ok
}
