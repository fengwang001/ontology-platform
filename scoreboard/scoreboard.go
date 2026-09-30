// Package scoreboard 实现基于选择确认（SACK）的发送端记分板。
//
// 序号为字节偏移，从 0 起。记分板记录已发数据段、收到的累计确认点
// 与选择确认区间（取并集），据此判定丢失段、维护在途字节数，
// 并按“起点最小优先”重传已判丢失且未重传的段。
package scoreboard

import (
	"errors"
	"sort"
	"sync"
)

// Block 表示一个半开区间 [Start, End) 的选择确认块。
type Block struct {
	Start int
	End   int
}

// 构造与各类操作的拒绝原因。被拒绝的操作不改变任何状态。
var (
	ErrNonPositiveM         = errors.New("scoreboard: 最大段长 M 必须为正")
	ErrThresholdTooSmall    = errors.New("scoreboard: 阈值 D 必须不小于 2")
	ErrCapTooSmall          = errors.New("scoreboard: 在途上限 W 必须不小于 M")
	ErrInvalidLength        = errors.New("scoreboard: 段长必须在 1 至 M 之间")
	ErrCumBehind            = errors.New("scoreboard: 累计点小于已收到的最大累计点")
	ErrCumBeyondEnd         = errors.New("scoreboard: 累计点大于已发送末端")
	ErrCumNotBoundary       = errors.New("scoreboard: 累计点不在段边界")
	ErrBlockEmptyOrInverted = errors.New("scoreboard: 块为空或倒置")
	ErrBlockNotAboveCum     = errors.New("scoreboard: 块起点不大于累计点")
	ErrBlockBeyondEnd       = errors.New("scoreboard: 块终点大于已发送末端")
	ErrBlockNotBoundary     = errors.New("scoreboard: 块端点不在段边界")
	ErrNoLostSegment        = errors.New("scoreboard: 无可重传段")
	ErrInFlightFull         = errors.New("scoreboard: 在途字节已达上限")
)

// segment 为一段已发送数据，[start, start+length)。
type segment struct {
	start  int
	length int
}

// Scoreboard 是发送端记分板，所有方法可并发调用。
type Scoreboard struct {
	mu      sync.Mutex
	m       int
	d       int
	w       int
	segs    []segment
	sentEnd int
	maxCum  int
	sacks   []Block // 已合并为互不相交、按起点升序的并集，且均在 maxCum 之上
	retrans map[int]bool
}

// New 构造记分板。M 非正、D 小于 2、W 小于 M 时按序返回对应错误。
func New(m, d, w int) (*Scoreboard, error) {
	if m <= 0 {
		return nil, ErrNonPositiveM
	}
	if d < 2 {
		return nil, ErrThresholdTooSmall
	}
	if w < m {
		return nil, ErrCapTooSmall
	}
	return &Scoreboard{m: m, d: d, w: w, retrans: make(map[int]bool)}, nil
}

// Send 按序追加一个长度为 length 的段，返回其起点。
func (s *Scoreboard) Send(length int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if length < 1 || length > s.m {
		return 0, ErrInvalidLength
	}
	start := s.sentEnd
	s.segs = append(s.segs, segment{start: start, length: length})
	s.sentEnd += length
	return start, nil
}

// Ack 应用累计确认点 cum 与若干选择确认块。
// 校验按固定顺序进行且只报第一个错误；任何拒绝都不改变状态。
func (s *Scoreboard) Ack(cum int, blocks []Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cum < s.maxCum {
		return ErrCumBehind
	}
	if cum > s.sentEnd {
		return ErrCumBeyondEnd
	}
	if !s.isBoundaryLocked(cum) {
		return ErrCumNotBoundary
	}
	for _, b := range blocks {
		if b.Start >= b.End {
			return ErrBlockEmptyOrInverted
		}
		if b.Start <= cum {
			return ErrBlockNotAboveCum
		}
		if b.End > s.sentEnd {
			return ErrBlockBeyondEnd
		}
		if !s.isBoundaryLocked(b.Start) || !s.isBoundaryLocked(b.End) {
			return ErrBlockNotBoundary
		}
	}
	s.maxCum = cum
	merged := make([]Block, 0, len(s.sacks)+len(blocks))
	merged = append(merged, s.sacks...)
	merged = append(merged, blocks...)
	s.sacks = mergeBlocks(merged)
	// 累计点越过的记录随之清除：丢弃 cum 之下的部分。
	kept := s.sacks[:0]
	for _, b := range s.sacks {
		if b.End <= cum {
			continue
		}
		if b.Start < cum {
			b.Start = cum
		}
		kept = append(kept, b)
	}
	s.sacks = kept
	return nil
}

// Retransmit 重传起点最小的已判丢失且未重传的段，返回其起点。
// 无可重传段的拒绝先于在途已满的拒绝。
func (s *Scoreboard) Retransmit() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.segs {
		if s.retrans[i] {
			continue
		}
		if s.isLostLocked(i) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, ErrNoLostSegment
	}
	if s.inFlightLocked()+s.segs[idx].length > s.w {
		return 0, ErrInFlightFull
	}
	s.retrans[idx] = true
	return s.segs[idx].start, nil
}

// InFlight 返回当前在途字节数，按定义对当前记录实时重算。
func (s *Scoreboard) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlightLocked()
}

// LostSegments 返回当前已判丢失段的起点（升序），用于查询与日志。
func (s *Scoreboard) LostSegments() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for i := range s.segs {
		if s.isLostLocked(i) {
			out = append(out, s.segs[i].start)
		}
	}
	return out
}

// MaxCum 返回已收到的最大累计确认点。
func (s *Scoreboard) MaxCum() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxCum
}

// SentEnd 返回已发送末端偏移。
func (s *Scoreboard) SentEnd() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sentEnd
}

// SACKBlocks 返回当前记录的选择确认并集（互不相交、升序）。
func (s *Scoreboard) SACKBlocks() []Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Block, len(s.sacks))
	copy(out, s.sacks)
	return out
}

// isBoundaryLocked 判断 x 是否为段边界（某段起点或已发送末端）。
func (s *Scoreboard) isBoundaryLocked(x int) bool {
	if x == s.sentEnd {
		return true
	}
	i := sort.Search(len(s.segs), func(i int) bool { return s.segs[i].start >= x })
	return i < len(s.segs) && s.segs[i].start == x
}

// isSackedLocked 判断段是否与选择确认并集相交。
func (s *Scoreboard) isSackedLocked(seg segment) bool {
	end := seg.start + seg.length
	for _, b := range s.sacks {
		if b.Start >= end {
			break
		}
		if b.End > seg.start {
			return true
		}
	}
	return false
}

// sackStatsAboveLocked 统计严格高于 pos 的选择确认并集：
// 返回不相交连续片段数与字节总数。
func (s *Scoreboard) sackStatsAboveLocked(pos int) (fragments, bytes int) {
	for _, b := range s.sacks {
		if b.Start >= pos {
			fragments++
			bytes += b.End - b.Start
		}
	}
	return fragments, bytes
}

// isLostLocked 判定第 i 段是否丢失：未确认且未被选择确认，且
// 高于它的已选择确认片段数不少于 D，或字节数不少于 (D-1)*M。
func (s *Scoreboard) isLostLocked(i int) bool {
	seg := s.segs[i]
	end := seg.start + seg.length
	if end <= s.maxCum {
		return false
	}
	if s.isSackedLocked(seg) {
		return false
	}
	fragments, bytes := s.sackStatsAboveLocked(end)
	return fragments >= s.d || bytes >= (s.d-1)*s.m
}

// inFlightLocked 按定义重算在途字节：全部未确认且未被选择确认的段中，
// 未判丢失或已重传者的长度之和。
func (s *Scoreboard) inFlightLocked() int {
	total := 0
	for i, seg := range s.segs {
		if seg.start+seg.length <= s.maxCum {
			continue
		}
		if s.isSackedLocked(seg) {
			continue
		}
		if s.retrans[i] || !s.isLostLocked(i) {
			total += seg.length
		}
	}
	return total
}

// mergeBlocks 将块按起点排序后合并重叠或相邻者，得到互不相交的并集。
func mergeBlocks(blocks []Block) []Block {
	if len(blocks) == 0 {
		return nil
	}
	sorted := make([]Block, len(blocks))
	copy(sorted, blocks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	out := []Block{sorted[0]}
	for _, b := range sorted[1:] {
		top := &out[len(out)-1]
		if b.Start <= top.End {
			if b.End > top.End {
				top.End = b.End
			}
			continue
		}
		out = append(out, b)
	}
	return out
}
