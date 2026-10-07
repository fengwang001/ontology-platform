// Package sender 实现传输层发送端的小段合并调度器。
//
// 调度器在应用写入、对端确认与窗口通告、选项变更、时间推进与关闭
// 这五类事件的驱动下，决定何时把缓冲数据切成段发出。时钟由调用方
// 注入（每个事件携带当前时刻），因此软木塞超时与零窗口探测的相互
// 作用可以被精确复现。
//
// 语义要点（详见 DESIGN.md）：
//   - 可发送上限 = 对端窗口右边缘 - 已发送序号；右边缘不得收缩。
//   - 切分时先发满段，尾部不足一个满段的数据称小段；被窗口截短的
//     段同样视为小段。
//   - 满段在窗口允许范围内总是可发；小段受保留规则约束（默认模式、
//     不延迟选项、软木塞选项、关闭）。
//   - 窗口为零且缓冲非空、在途为空时，按探测间隔发出一字节探测段。
package sender

import (
	"errors"
	"math"
	"sync"
	"time"
)

// 错误按固定优先级判定（靠前者优先）：
//  1. ErrInvalidParam    参数非法
//  2. ErrClockBackward   时钟回退
//  3. ErrWriteAfterClose 关闭后写入
//  4. ErrBufferFull      缓冲已满
//  5. ErrWindowShrink    窗口收缩
//  6. ErrAckOutOfRange   确认越界
var (
	ErrInvalidParam    = errors.New("sender: invalid parameter")
	ErrClockBackward   = errors.New("sender: clock moved backward")
	ErrWriteAfterClose = errors.New("sender: write after close")
	ErrBufferFull      = errors.New("sender: buffer full")
	ErrWindowShrink    = errors.New("sender: window shrank")
	ErrAckOutOfRange   = errors.New("sender: ack out of range")
)

// MaxWriteLen 是单次写入的长度上限（2^31 - 1）。
const MaxWriteLen = math.MaxInt32

// Config 是调度器配置。所有时长非负；MSS 必须为正。
type Config struct {
	MSS           int           // 最大段长 M
	InitialWindow int           // 初始对端窗口（字节）
	BufferCap     int           // 缓冲区总量上限 B（字节）
	CorkTimeout   time.Duration // 软木塞超时
	ProbeInterval time.Duration // 零窗口探测间隔
}

func (c Config) validate() error {
	if c.MSS <= 0 || c.InitialWindow < 0 || c.BufferCap < 0 ||
		c.CorkTimeout < 0 || c.ProbeInterval < 0 {
		return ErrInvalidParam
	}
	return nil
}

// Segment 是本次事件发出的一段数据。
type Segment struct {
	Len int       // 段长（字节）
	At  time.Time // 发出时刻（即事件携带的时刻）
}

// Scheduler 是小段合并调度器。所有方法可并发调用，效果等价于某个
// 串行顺序（内部以互斥串行化，线性化点在各方法内部）。
//
// 每个事件的处理开销为 O(本次发出的段数)：全部状态为定长计数器，
// 不维护随缓冲总量或历史事件数增长的数据结构。
type Scheduler struct {
	mu sync.Mutex

	mss           int
	bufferCap     int
	corkTimeout   time.Duration
	probeInterval time.Duration

	unsent     int   // 缓冲中尚未发出的字节数
	inFlight   int   // 已发送未确认（在途）字节数
	sentTotal  int64 // 下一个待发送序号（累计已发字节数）
	ackedTotal int64 // 累计已确认字节数
	wndRight   int64 // 对端窗口右边缘（ackedTotal + 最新通告窗口）

	noDelay bool
	cork    bool
	closed  bool

	// 软木塞保留批次起点：当前保留批次首次出现窗口可容纳的小段的时刻。
	corkSince time.Time
	hasCork   bool
	// 可用窗口变为零的时刻。
	zeroSince time.Time
	hasZero   bool
	// 上次探测时刻。
	lastProbe time.Time
	hasProbe  bool

	lastTime time.Time
	hasTime  bool
}

// NewScheduler 按配置创建调度器。
func NewScheduler(cfg Config) (*Scheduler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Scheduler{
		mss:           cfg.MSS,
		bufferCap:     cfg.BufferCap,
		corkTimeout:   cfg.CorkTimeout,
		probeInterval: cfg.ProbeInterval,
		wndRight:      int64(cfg.InitialWindow),
	}, nil
}

// Write 处理应用写入 n 字节。n <= 0 或 n > MaxWriteLen 报
// ErrInvalidParam；写入使缓冲超过上限时整次拒绝（ErrBufferFull），
// 不接受其中一部分。返回本次事件发出的各段。
func (s *Scheduler) Write(now time.Time, n int) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n > MaxWriteLen {
		return nil, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if s.closed {
		return nil, ErrWriteAfterClose
	}
	if n > s.bufferCap-s.unsent {
		return nil, ErrBufferFull
	}
	s.accept(now)
	s.unsent += n
	return s.transmit(now), nil
}

// Ack 处理对端确认：acked 为本次累计确认的字节数（必须落在在途范围
// 内），window 为最新通告窗口。新右边缘 = 累计确认 + window，小于已知
// 右边缘时报 ErrWindowShrink。
func (s *Scheduler) Ack(now time.Time, acked int, window int) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if window < 0 {
		return nil, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	newRight := s.ackedTotal + int64(acked) + int64(window)
	if newRight < s.wndRight {
		return nil, ErrWindowShrink
	}
	if acked < 0 || acked > s.inFlight {
		return nil, ErrAckOutOfRange
	}
	s.accept(now)
	s.ackedTotal += int64(acked)
	s.inFlight -= acked
	s.wndRight = newRight
	return s.transmit(now), nil
}

// SetNoDelay 设置或清除不延迟选项。打开时小段不受保留约束，只受窗口
// 约束（软木塞同时打开时软木塞优先）。
func (s *Scheduler) SetNoDelay(now time.Time, on bool) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.accept(now)
	s.noDelay = on
	return s.transmit(now), nil
}

// SetCork 设置或清除软木塞选项。打开时即使在途为空也只发满段，小段
// 一律保留，直到保留超时；清除时立即解除保留。
func (s *Scheduler) SetCork(now time.Time, on bool) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.accept(now)
	s.cork = on
	if !on {
		s.hasCork = false
	}
	return s.transmit(now), nil
}

// Advance 推进时钟，使到期的软木塞保留或零窗口探测触发。
func (s *Scheduler) Advance(now time.Time) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.accept(now)
	return s.transmit(now), nil
}

// Close 关闭发送端。之后不再接受写入；缓冲中剩余数据忽略保留约束，
// 仅按窗口约束继续发出，直至清空。重复关闭是空操作。
func (s *Scheduler) Close(now time.Time) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.accept(now)
	s.closed = true
	return s.transmit(now), nil
}

// InFlight 返回在途（已发送未确认）字节数。
func (s *Scheduler) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight
}

// Buffered 返回缓冲中尚未发出的字节数。
func (s *Scheduler) Buffered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsent
}

// Retained 返回缓冲中当前被保留规则扣留的小段字节数，即：若现在解除
// 保留规则，在当前窗口下会立即发出、但因保留而未发出的字节数。窗口为
// 零时该值为零（此时数据是被窗口而非保留规则挡住）。按最近一次被接受
// 事件的时刻求值。
func (s *Scheduler) Retained() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, piece := s.pending()
	if piece == 0 || s.smallAllowed(s.lastTime) {
		return 0
	}
	return piece
}

// NextDeadline 返回下一个仅因时间推进就会触发动作的时刻（软木塞到期
// 或探测到期的较早者）；没有则返回 false。
func (s *Scheduler) NextDeadline() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var deadline time.Time
	ok := false
	if s.cork && !s.closed && s.hasCork {
		if t := s.corkSince.Add(s.corkTimeout); t.After(s.lastTime) {
			deadline, ok = t, true
		}
	}
	if s.unsent > 0 && s.inFlight == 0 && s.usable() <= 0 && s.hasZero {
		t := s.probeBase().Add(s.probeInterval)
		if !ok || t.Before(deadline) {
			deadline, ok = t, true
		}
	}
	return deadline, ok
}

func (s *Scheduler) checkClock(now time.Time) error {
	if s.hasTime && now.Before(s.lastTime) {
		return ErrClockBackward
	}
	return nil
}

func (s *Scheduler) accept(now time.Time) {
	s.lastTime = now
	s.hasTime = true
}

// usable 是可发送上限：窗口右边缘减去已发送序号。
func (s *Scheduler) usable() int64 {
	return s.wndRight - s.sentTotal
}

// pending 返回当前窗口可容纳的满段个数与小段长度（小段不考虑保留
// 规则）。小段可能是数据尾部，也可能是被窗口截短的段。
func (s *Scheduler) pending() (fulls, piece int) {
	usable := s.usable()
	if usable <= 0 || s.unsent == 0 {
		return 0, 0
	}
	fulls = s.unsent / s.mss
	if w := usable / int64(s.mss); w < int64(fulls) {
		fulls = int(w)
	}
	fullBytes := int64(fulls) * int64(s.mss)
	p := int64(s.unsent) - fullBytes
	if rem := usable - fullBytes; rem < p {
		p = rem
	}
	if p > 0 {
		piece = int(p)
	}
	return fulls, piece
}

// smallAllowed 报告在时刻 now 小段是否放行。
func (s *Scheduler) smallAllowed(now time.Time) bool {
	if s.closed {
		return true
	}
	if s.cork {
		if !s.hasCork {
			// 保留自本时刻起算；超时为零则立即到期。
			return s.corkTimeout <= 0
		}
		return now.Sub(s.corkSince) >= s.corkTimeout
	}
	if s.noDelay {
		return true
	}
	return s.inFlight == 0
}

// transmit 在每个被接受的事件处理完毕后执行发送决策。
func (s *Scheduler) transmit(now time.Time) []Segment {
	var segs []Segment
	fulls, piece := s.pending()
	if fulls > 0 {
		for i := 0; i < fulls; i++ {
			segs = append(segs, Segment{Len: s.mss, At: now})
		}
		n := fulls * s.mss
		s.unsent -= n
		s.inFlight += n
		s.sentTotal += int64(n)
	}
	// 小段的放行判定在本批满段发出之后进行：默认模式下，本批满段
	// 已使在途非空，尾部小段随之保留（与 Nagle 语义一致）。
	if piece > 0 && s.smallAllowed(now) {
		segs = append(segs, Segment{Len: piece, At: now})
		s.unsent -= piece
		s.inFlight += piece
		s.sentTotal += int64(piece)
	}
	s.refreshZero(now)
	if s.unsent > 0 && s.inFlight == 0 && s.usable() <= 0 && s.hasZero {
		if base := s.probeBase(); now.Sub(base) >= s.probeInterval {
			segs = append(segs, Segment{Len: 1, At: now})
			s.unsent--
			s.inFlight++
			s.sentTotal++
			s.lastProbe = now
			s.hasProbe = true
		}
	}
	s.refreshCork(now)
	return segs
}

// refreshZero 维护“窗口变为零”的时刻锚点。
func (s *Scheduler) refreshZero(now time.Time) {
	if s.usable() <= 0 {
		if !s.hasZero {
			s.zeroSince = now
			s.hasZero = true
		}
	} else {
		s.hasZero = false
	}
}

// probeBase 是探测计时基点：窗口变为零或上次探测的较晚者。
func (s *Scheduler) probeBase() time.Time {
	base := s.zeroSince
	if s.hasProbe && s.lastProbe.After(base) {
		base = s.lastProbe
	}
	return base
}

// refreshCork 维护软木塞保留批次的起点：保留批次首次出现窗口可容纳
// 的小段时锚定；批次数据全部发出（或软木塞关闭、连接关闭）时清除。
// 窗口为零不清除锚点，保证超时解除保留的效果跨越窗口关闭期保持粘性。
func (s *Scheduler) refreshCork(now time.Time) {
	if !s.cork || s.closed {
		s.hasCork = false
		return
	}
	_, piece := s.pending()
	if !s.hasCork && piece > 0 {
		s.corkSince = now
		s.hasCork = true
	}
	if s.hasCork && piece == 0 && s.usable() > 0 {
		s.hasCork = false
	}
}
