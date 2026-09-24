// Package ofs 维护单个 CDC 分区的位点状态：已提交位点 C（语义为
// 「下一条要读的位点」）、已投递上界与已 Ack 集合，以及 C 的推进规则。
// 本包不依赖任何其他包；State 本身不加锁，由上层 cmt 串行化访问。
package ofs

import "errors"

// 可判定哨兵错误：Deliver 不连续、Ack 越界。二者与上层错误互不相同。
var (
	// ErrDeliverGap：Deliver 的位点不等于当前已投递上界（出现空洞或回退）。
	ErrDeliverGap = errors.New("ofs: deliver offset is not contiguous")
	// ErrAckOutOfRange：Ack 的位点 >= 已投递上界，该消息尚未投递。
	ErrAckOutOfRange = errors.New("ofs: ack offset beyond delivered high-water mark")
)

// State 是单分区位点状态。零值不可用，必须经 New 构造。
type State struct {
	start     int64              // 分区起点（初始已提交位点）
	committed int64              // C：下一条要读的位点；[start,C) 均已 Ack
	delivered int64              // 已投递上界：下一条应投递的位点
	acked     map[int64]struct{} // 已 Ack 且 >= committed 的位点（< C 的不保留）

	// lastChecks 记录最近一次 Ack 推进 C 的过程中检查过的位点个数；
	// 非导出，仅供包内测试读取，绝不出现在公开接口中。
	lastChecks int
}

// New 以起点 start（等价于初始已提交位点）创建单分区状态。
func New(start int64) *State {
	return &State{
		start:     start,
		committed: start,
		delivered: start,
		acked:     make(map[int64]struct{}),
	}
}

// Start 返回分区起点。
func (s *State) Start() int64 { return s.start }

// Committed 返回已提交位点 C（下一条要读的位点）。
func (s *State) Committed() int64 { return s.committed }

// Delivered 返回已投递上界。合法的下一次 Deliver 必须恰好等于它。
func (s *State) Delivered() int64 { return s.delivered }

// InFlight 返回已投递但尚未被提交覆盖的位点数。
func (s *State) InFlight() int64 { return s.delivered - s.committed }

// Deliver 登记一条投递：off 必须恰好等于当前已投递上界，否则整体失败
// （ErrDeliverGap）且不改变任何状态。
func (s *State) Deliver(off int64) error {
	if off != s.delivered {
		return ErrDeliverGap
	}
	s.delivered++
	return nil
}

// Ack 登记位点 off 处理完成。
//   - off >= 已投递上界：越界，ErrAckOutOfRange，不改态；
//   - off < C：必然是重复 Ack，幂等成功，不改态；
//   - 其余位点重复 Ack 同样幂等成功。
//
// 新 Ack 后从 C 起逐个推进：仅当 C 处位点已 Ack 时越过它并删除其记录，
// 遇到第一个未 Ack 位点立即停止。推进只可能由「C 被 Ack」触发。
func (s *State) Ack(off int64) error {
	if off >= s.delivered {
		s.lastChecks = 0
		return ErrAckOutOfRange
	}
	if off < s.committed {
		s.lastChecks = 0 // 早已被提交覆盖，必为重复，不触发推进。
		return nil
	}
	if _, dup := s.acked[off]; dup {
		s.lastChecks = 0
		return nil
	}
	s.acked[off] = struct{}{}

	checks := 0
	for {
		checks++ // 每轮检查一个位点，含最终那个未 Ack 而停下的位点。
		if _, ok := s.acked[s.committed]; !ok {
			break
		}
		delete(s.acked, s.committed)
		s.committed++
	}
	s.lastChecks = checks
	return nil
}
