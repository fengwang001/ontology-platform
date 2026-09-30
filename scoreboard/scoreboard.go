package scoreboard

import (
	"errors"
	"io"
	"log"
	"sort"
	"sync"
)

// Block 是选择确认中的一个半开字节区间 [Start, End)。
type Block struct {
	Start int
	End   int
}

// 各类可区分的拒绝原因。
var (
	ErrInvalidM            = errors.New("scoreboard: M must be positive")
	ErrInvalidD            = errors.New("scoreboard: D must be >= 2")
	ErrInvalidW            = errors.New("scoreboard: W must be >= M")
	ErrInvalidLength       = errors.New("scoreboard: length must be in [1, M]")
	ErrCumulativeRevert    = errors.New("scoreboard: cumulative point is smaller than the largest seen cumulative point")
	ErrCumulativeTooFar    = errors.New("scoreboard: cumulative point is beyond the sent end")
	ErrCumulativeBoundary  = errors.New("scoreboard: cumulative point is not at a segment boundary")
	ErrNoBlocks            = errors.New("scoreboard: at least one SACK block is required")
	ErrBlockReversed       = errors.New("scoreboard: SACK block is empty or reversed")
	ErrBlockStartAtCum     = errors.New("scoreboard: SACK block start must be greater than the cumulative point")
	ErrBlockEndTooFar      = errors.New("scoreboard: SACK block end is beyond the sent end")
	ErrBlockBoundary       = errors.New("scoreboard: SACK block endpoint is not at a segment boundary")
	ErrNothingToRetransmit = errors.New("scoreboard: no segment is eligible for retransmission")
	ErrWindowFull          = errors.New("scoreboard: in-flight window cannot fit the segment")
)

// Scoreboard 是基于选择确认的发送端记分板。
type Scoreboard struct {
	mu sync.Mutex

	m int // 最大段长
	d int // 判丢阈值（不小于 2）
	w int // 在途上限

	segs       []*segment // 已发送的段（累计点之前的段保留也无妨，判定时会过滤）
	sentEnd    int        // 已发送末端
	cumulative int        // 已收到的最大累计点 c（小于 c 的字节全部收到）
	sack       []Block    // 已取并集的选择确认区间：互不相交、按起点排序、均与 [c, sentEnd) 有交集

	logf func(string, ...any)
	hook func(kind string, p1, p2 int, blocks []Block, ok bool) // 持锁触发的操作钩子（测试用）
}

type segment struct {
	start         int
	end           int
	retransmitted bool // 是否已重传
}

// New 构造记分板：最大段长 M、判丢阈值 D、在途上限 W。
// 拒绝顺序：M 非正 -> D 小于 2 -> W 小于 M。
func New(m, d, w int, logWriter io.Writer) (*Scoreboard, error) {
	if m <= 0 {
		return nil, ErrInvalidM
	}
	if d < 2 {
		return nil, ErrInvalidD
	}
	if w < m {
		return nil, ErrInvalidW
	}
	if logWriter == nil {
		logWriter = io.Discard
	}
	return &Scoreboard{
		m:    m,
		d:    d,
		w:    w,
		logf: log.New(logWriter, "", log.LstdFlags|log.Lmicroseconds).Printf,
	}, nil
}

// Send 按序追加一个给定长度的段并返回其起点序号。
func (s *Scoreboard) Send(length int) (start int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if s.hook != nil {
			s.hook("send", start, length, nil, err == nil)
		}
	}()

	s.logf("SEND 输入: length=%d", length)
	if length < 1 || length > s.m {
		s.logf("SEND 拒绝: %v (length=%d 不在 [1,%d])，状态不变", ErrInvalidLength, length, s.m)
		return 0, ErrInvalidLength
	}
	start = s.sentEnd
	s.segs = append(s.segs, &segment{start: start, end: start + length})
	s.sentEnd += length
	s.logf("SEND 输出: 段起点=%d，新已发送末端=%d，当前在途=%d", start, s.sentEnd, s.inFlightLocked())
	return start, nil
}

// Acknowledge 记录一次确认：累计点 c 与若干 SACK 块（取并集）。
// 所有输入先校验通过后才整体生效；任一非法则状态不变。
func (s *Scoreboard) Acknowledge(c int, blocks []Block) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if s.hook != nil {
			s.hook("ack", c, 0, blocks, err == nil)
		}
	}()

	s.logf("ACK 输入: cumulative=%d blocks=%v", c, blocks)

	// 1) 累计点校验，按题目规定的顺序只报第一个错误。
	if c < s.cumulative {
		s.logf("ACK 拒绝: %v (新=%d < 已收到最大累计点=%d)，状态不变", ErrCumulativeRevert, c, s.cumulative)
		return ErrCumulativeRevert
	}
	if c > s.sentEnd {
		s.logf("ACK 拒绝: %v (%d > 已发送末端=%d)，状态不变", ErrCumulativeTooFar, c, s.sentEnd)
		return ErrCumulativeTooFar
	}
	if !s.isBoundaryLocked(c) {
		s.logf("ACK 拒绝: %v (cumulative=%d 不在段边界)，状态不变", ErrCumulativeBoundary, c)
		return ErrCumulativeBoundary
	}

	// 2) 逐块校验，按给出顺序，每块内部按固定错误顺序。
	if len(blocks) == 0 {
		s.logf("ACK 拒绝: %v，状态不变", ErrNoBlocks)
		return ErrNoBlocks
	}
	for i, b := range blocks {
		if b.End <= b.Start {
			s.logf("ACK 拒绝: %v (blocks[%d]=%v 为空或倒置)，状态不变", ErrBlockReversed, i, b)
			return ErrBlockReversed
		}
		if b.Start <= c {
			s.logf("ACK 拒绝: %v (blocks[%d]=%v 起点 %d <= cumulative=%d)，状态不变", ErrBlockStartAtCum, i, b, b.Start, c)
			return ErrBlockStartAtCum
		}
		if b.End > s.sentEnd {
			s.logf("ACK 拒绝: %v (blocks[%d]=%v 终点 %d > 已发送末端=%d)，状态不变", ErrBlockEndTooFar, i, b, b.End, s.sentEnd)
			return ErrBlockEndTooFar
		}
		if !s.isBoundaryLocked(b.Start) || !s.isBoundaryLocked(b.End) {
			s.logf("ACK 拒绝: %v (blocks[%d]=%v 端点不在段边界)，状态不变", ErrBlockBoundary, i, b)
			return ErrBlockBoundary
		}
	}

	// 3) 全部合法，整体生效。
	s.cumulative = c

	// 3a) 清除被累计点越过的旧记录；部分越过则在边界 c 处截断。
	// 使用全新切片，避免就地复用对外部传入 blocks 产生副作用。
	kept := make([]Block, 0, len(s.sack)+len(blocks))
	for _, b := range s.sack {
		if b.End <= c {
			continue
		}
		if b.Start < c {
			b.Start = c
		}
		kept = append(kept, b)
	}

	// 3b) 复制新块后并入，对所有区间取并集（重叠或相邻合并）。
	merged := append(kept, append(make([]Block, 0, len(blocks)), blocks...)...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].Start < merged[j].Start })
	s.sack = s.sack[:0]
	for _, b := range merged {
		if n := len(s.sack); n > 0 && b.Start <= s.sack[n-1].End {
			if b.End > s.sack[n-1].End {
				s.sack[n-1].End = b.End
			}
		} else {
			s.sack = append(s.sack, b)
		}
	}

	s.logf("ACK 输出: 接受；新累计点=%d，并集后 SACK=%v", s.cumulative, s.sack)
	s.logLossRationaleLocked()
	return nil
}

// Retransmit 重传起点最小的已判丢失且未重传的段，返回其起点。
// 无可重传段的拒绝先于在途已满的拒绝。
func (s *Scoreboard) Retransmit() (start int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if s.hook != nil {
			s.hook("rtx", start, 0, nil, err == nil)
		}
	}()

	s.logf("RTX 输入: 请求重传；当前在途=%d/%d", s.inFlightLocked(), s.w)

	var target *segment
	for _, seg := range s.segs {
		if s.isCumAckedLocked(seg) || s.isSackCoveredLocked(seg) || seg.retransmitted {
			continue
		}
		if s.isLostLocked(seg) && (target == nil || seg.start < target.start) {
			target = seg
		}
	}
	if target == nil {
		s.logf("RTX 拒绝: %v，状态不变", ErrNothingToRetransmit)
		return 0, ErrNothingToRetransmit
	}

	inFlight := s.inFlightLocked()
	length := target.end - target.start
	if inFlight+length > s.w {
		s.logf("RTX 拒绝: %v (段 [%d,%d) 长 %d；在途 %d+%d > W=%d)，状态不变",
			ErrWindowFull, target.start, target.end, length, inFlight, length, s.w)
		return 0, ErrWindowFull
	}

	target.retransmitted = true
	s.logf("RTX 输出: 重传段起点=%d（长度=%d），新在途=%d/%d", target.start, length, s.inFlightLocked(), s.w)
	return target.start, nil
}

// InFlight 返回当前在途字节数（按定义重算）。
func (s *Scoreboard) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.inFlightLocked()
	s.logf("QUERY 在途=%d", v)
	return v
}

// inFlightLocked 按定义重算在途字节：
// 全部“未确认且未被选择确认”的段中，未判丢失或已重传者的长度之和。
func (s *Scoreboard) inFlightLocked() int {
	total := 0
	for _, seg := range s.segs {
		if s.isCumAckedLocked(seg) || s.isSackCoveredLocked(seg) {
			continue
		}
		lost := s.isLostLocked(seg)
		if !lost || seg.retransmitted {
			total += seg.end - seg.start
		}
	}
	return total
}

// isLostLocked 判丢：未确认且未被选择确认的段，当且仅当
// 高于它的已选择确认区间中不相交连续片段不少于 D 个，
// 或高于它的已选择确认字节数不少于 (D-1)*M。
func (s *Scoreboard) isLostLocked(seg *segment) bool {
	fragments := 0
	bytes := 0
	for _, b := range s.sack {
		if b.Start < seg.end { // 区间与段重叠或位于其下；未被覆盖的段不可能与 SACK 相交，这里取严格高于
			continue
		}
		fragments++
		bytes += b.End - b.Start
	}
	return fragments >= s.d || bytes >= (s.d-1)*s.m
}

func (s *Scoreboard) isCumAckedLocked(seg *segment) bool {
	return seg.end <= s.cumulative
}

func (s *Scoreboard) isSackCoveredLocked(seg *segment) bool {
	for _, b := range s.sack {
		if b.Start <= seg.start && seg.end <= b.End {
			return true
		}
	}
	return false
}

// isBoundaryLocked 判断序号是否为段边界：某段起点或已发送末端。
func (s *Scoreboard) isBoundaryLocked(seq int) bool {
	if seq == s.sentEnd {
		return true
	}
	for _, seg := range s.segs {
		if seg.start == seq {
			return true
		}
		if seg.start > seq {
			break
		}
	}
	return false
}

// logLossRationaleLocked 打印每个未确认段的判丢依据。
func (s *Scoreboard) logLossRationaleLocked() {
	for _, seg := range s.segs {
		if s.isCumAckedLocked(seg) {
			s.logf("判定: 段 [%d,%d) 已被累计点 %d 确认", seg.start, seg.end, s.cumulative)
			continue
		}
		if s.isSackCoveredLocked(seg) {
			s.logf("判定: 段 [%d,%d) 已被选择确认覆盖", seg.start, seg.end)
			continue
		}
		fragments, bytes := s.higherSackLocked(seg)
		lost := fragments >= s.d || bytes >= (s.d-1)*s.m
		s.logf("判定: 段 [%d,%d) 高于它的不相交 SACK 片段=%d（阈值 %d），字节=%d（阈值 %d）=> 丢失=%v（已重传=%v）",
			seg.start, seg.end, fragments, s.d, bytes, (s.d-1)*s.m, lost, seg.retransmitted)
	}
}

func (s *Scoreboard) higherSackLocked(seg *segment) (fragments, bytes int) {
	for _, b := range s.sack {
		if b.Start >= seg.end {
			fragments++
			bytes += b.End - b.Start
		}
	}
	return fragments, bytes
}
