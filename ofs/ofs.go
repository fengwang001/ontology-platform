// Package ofs 维护单分区的位点状态：已提交位点、已投递上界、已 Ack 集合与推进规则。
// 不依赖其他包。不是并发安全的，并发控制由上层（cmt）负责。
package ofs

import "errors"

var (
	// ErrGap 表示 Deliver 的位点不等于当前已投递上界（不连续）。
	ErrGap = errors.New("ofs: deliver offset != delivered high-water")
	// ErrOutOfRange 表示 Ack 的位点 >= 已投递上界（越界）。
	ErrOutOfRange = errors.New("ofs: ack offset >= delivered high-water")
)

// State 是单分区的位点状态。committed 是已提交位点 C（下一条要读的位点），
// hi 是已投递上界（下一条 Deliver 必须恰好等于它）。
type State struct {
	committed int64
	hi        int64
	acked     map[int64]struct{}
	checked   int // 最近一次推进 C 时检查过的位点个数；非导出，不进公开接口
}

// New 以 start 为初始已提交位点（= 初始已投递上界）构造状态。
func New(start int64) *State {
	return &State{committed: start, hi: start, acked: map[int64]struct{}{}}
}

// Committed 返回已提交位点 C。
func (s *State) Committed() int64 { return s.committed }

// Hi 返回已投递上界。
func (s *State) Hi() int64 { return s.hi }

// InFlight 返回已投递未提交的位点数。
func (s *State) InFlight() int64 { return s.hi - s.committed }

// Deliver 登记一条已投递消息；off 必须恰好等于当前上界，否则整体失败、状态不变。
func (s *State) Deliver(off int64) error {
	if off != s.hi {
		return ErrGap
	}
	s.hi++
	return nil
}

// Ack 登记一条处理完成。越界报错且状态不变；重复 Ack（< C 或已登记）幂等成功。
// 仅当 off == C 时推进 C：逐个越过已 Ack 位点，遇到第一个未 Ack 位点停下，
// 不做整表重扫；checked 记录本次推进检查过的位点个数。
func (s *State) Ack(off int64) error {
	if off >= s.hi {
		return ErrOutOfRange
	}
	s.checked = 0
	if off < s.committed {
		return nil // < C 的 Ack 必然是重复，幂等
	}
	if _, dup := s.acked[off]; dup {
		return nil // 重复 Ack，幂等
	}
	s.acked[off] = struct{}{}
	if off != s.committed {
		return nil // 不在 C 上，不推进
	}
	for {
		s.checked++
		if _, ok := s.acked[s.committed]; !ok {
			break
		}
		delete(s.acked, s.committed)
		s.committed++
	}
	return nil
}

// Reset 模拟崩溃重启：丢弃未提交状态，已投递上界回退到已提交位点。
func (s *State) Reset() {
	s.hi = s.committed
	s.acked = map[int64]struct{}{}
}
