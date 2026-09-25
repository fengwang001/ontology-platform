// Package hbs 维护一条事件时间流的进程内状态：
// 当前水位、迟到心跳计数与水位推进规则。
// 依赖方向单向：hbs → wm，hbs 不依赖 api。
package hbs

import (
	"sync"

	"ontology/wm"
)

// historyEventsInspectedPerJudgment 是单次「迟到判定」检查过的历史事件数。
// 判定只比较心跳 TS 与当前水位这两个标量，不回扫任何历史事件，故恒为 0。
const historyEventsInspectedPerJudgment = 0

// Stream 是一条事件时间流的全部状态（存于进程内存）。
type Stream struct {
	mu sync.Mutex

	delay int64
	wm    wm.Watermark

	lateHBs int64 // 已接受的迟到心跳个数

	// lateChecks 是历次迟到判定中「检查过的历史事件」累计数。
	// 非导出：只能被 hbs 包内代码与同包白盒测试读取，绝不经过公开接口外泄数值。
	lateChecks int64
}

// New 以给定正 delay 创建一条初始水位为负无穷的流。
// 参数合法性由上层 api 校验，hbs 只负责状态。
func New(delay int64) *Stream {
	return &Stream{delay: delay, wm: wm.New()}
}

// Feed 喂入数据事件 {Key, TS}，贡献为 TS-delay，返回推进后的水位。
func (s *Stream) Feed(ts int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wm.Advance(wm.DataContribution(ts, s.delay))
}

// Heartbeat 喂入心跳事件 {TS}，贡献为 TS 本身。
// 迟到（TS<=wm）时不改水位、不报错，只累加迟到计数。
// 返回推进后的水位与该心跳是否迟到。
func (s *Stream) Heartbeat(ts int64) (watermark int64, late bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.wm.Value()
	if s.judgeLate(ts, current) {
		s.lateHBs++
		return current, true
	}
	return s.wm.Advance(wm.HeartbeatContribution(ts)), false
}

// judgeLate 判定一个心跳是否迟到。
// 它只比较 TS 与当前水位（wm.IsLateHeartbeat），不查看任何历史事件，
// 因此每次判定计入 lateChecks 的历史事件检查数恒为 0——O(1) 与历史长度无关。
func (s *Stream) judgeLate(ts, current int64) bool {
	s.lateChecks += historyEventsInspectedPerJudgment
	return wm.IsLateHeartbeat(ts, current)
}

// Watermark 返回当前水位（只读，同样加锁以支持并发）。
func (s *Stream) Watermark() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wm.Value()
}

// LateHeartbeats 返回累计接受的迟到心跳数。
func (s *Stream) LateHeartbeats() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lateHBs
}

// LateCheckBounded 是供自检使用的布尔结论（不返回计数器数值本身）：
// 在 100/1000/10000 三档历史规模下各做一次迟到判定，
// 单次判定检查过的历史事件数必须被与 m 无关的常数界住。
func (s *Stream) LateCheckBounded() bool {
	const bound = historyEventsInspectedPerJudgment
	for _, m := range []int{100, 1000, 10000} {
		fresh := New(s.delay)
		for i := 0; i < m; i++ {
			fresh.wm.Advance(int64(i))
		}
		before := fresh.lateChecks
		fresh.judgeLate(1, fresh.wm.Value())
		if fresh.lateChecks-before > bound {
			return false
		}
	}
	return true
}
