// Package keep 持有保活状态：空闲判定、探测推进与半开判死规则。
// 不依赖任何其他包，逻辑时钟 now 单调递增（int64）。
package keep

import "errors"

// 可判定哨兵错误，三者互不相同。
var (
	// ErrDead 连接已被判死后仍收到 Activity。
	ErrDead = errors.New("keep: connection already judged dead")
	// ErrClockBack 逻辑时钟回退（now 比上一次调用小）。
	ErrClockBack = errors.New("keep: logical clock moved backwards")
	// ErrInvalidParam idleTimeout/probeInterval/maxProbes 任一非正。
	ErrInvalidParam = errors.New("keep: parameter must be positive")
)

// State 是一条连接的保活状态，非并发安全，由上层 probe 加锁。
type State struct {
	idleTimeout   int64
	probeInterval int64
	maxProbes     int64

	lastActive int64 // 最后一次 Activity 的时刻；探测不算活动，永不被 Tick 改写
	lastProbe  int64 // 最近一次探测发出的时刻
	probes     int   // 连续无响应的探测次数（整数计数，非列表）
	dead       bool

	seen    bool  // 是否已有过时钟调用
	lastNow int64 // 上一次调用的 now，用于回退判定

	// inspected 是最近一次 Tick 检查过的探测记录个数。
	// 非导出：只能由同包测试直接读取，绝不出现在任何公开接口里。
	// 探测以整数 probes 计数推进，每次 Tick 至多查看当前这一条，恒为 0 或 1。
	inspected int
}

// New 创建保活状态；参数非法返回 ErrInvalidParam，且不留任何状态。
func New(idleTimeout, probeInterval, maxProbes int64) (*State, error) {
	if idleTimeout <= 0 || probeInterval <= 0 || maxProbes <= 0 {
		return nil, ErrInvalidParam
	}
	return &State{
		idleTimeout:   idleTimeout,
		probeInterval: probeInterval,
		maxProbes:     maxProbes,
	}, nil
}

// clockOK 必须在任何状态写入之前调用：时钟只进不退。
func (s *State) clockOK(now int64) error {
	if s.seen && now < s.lastNow {
		return ErrClockBack
	}
	return nil
}

// stamp 记录本次时钟；仅在操作被接受时调用。
func (s *State) stamp(now int64) {
	s.seen, s.lastNow = true, now
}

// Activity 表示对端有响应：dead 拒绝（ErrDead）；否则锚定活动时刻并把
// 连续无响应计数清零——任何时刻到达的 Activity 都能中止判死过程。
func (s *State) Activity(now int64) error {
	if err := s.clockOK(now); err != nil {
		return err
	}
	if s.dead {
		return ErrDead
	}
	s.lastActive = now
	s.probes = 0
	s.stamp(now)
	return nil
}

// Tick 推进逻辑时钟并判定，返回本次是否发出探测、是否刚刚判死。
// 空闲门与间隔门均为左闭（差 >= 阈值即触发）；已死或未空闲时 no-op。
func (s *State) Tick(now int64) (probeSent bool, becameDead bool, err error) {
	if err = s.clockOK(now); err != nil {
		return false, false, err
	}
	s.stamp(now)
	s.inspected = 0
	if s.dead {
		return false, false, nil
	}
	if now-s.lastActive < s.idleTimeout {
		return false, false, nil // 尚未空闲
	}
	// 已空闲：每次只看“当前这一条”待响应探测，不回溯历史，O(1)。
	switch {
	case s.probes == 0:
		s.inspected = 1
		s.probes, s.lastProbe = 1, now // 发第 1 次探测
		return true, false, nil
	case int64(s.probes) < s.maxProbes:
		if now-s.lastProbe < s.probeInterval {
			return false, false, nil // 还在等上一次探测的响应
		}
		s.inspected = 1
		s.probes++ // 上一次无响应，发下一次
		s.lastProbe = now
		return true, false, nil
	default: // probes == maxProbes
		if now-s.lastProbe < s.probeInterval {
			return false, false, nil // 最后一次探测的响应窗口尚未关
		}
		s.inspected = 1
		s.dead = true // 第 maxProbes 次探测后又过满一个间隔仍无响应
		return false, true, nil
	}
}

// InspectionBoundHolds 令连接空闲并连续发出 m 次探测，报告其间每次 Tick
// 检查过的探测记录数是否都不超过 1（即推进/判死为 O(1)，不随 m 线性扫描）。
// 只回传成败布尔，不回传计数器数值；m 必须为正。
func InspectionBoundHolds(m int) bool {
	if m <= 0 {
		return false
	}
	st, err := New(1, 1, int64(m)+1) // 连续 m 次探测都不触达判死
	if err != nil {
		return false
	}
	if err = st.Activity(0); err != nil {
		return false
	}
	for t := int64(1); t <= int64(m); t++ {
		sent, _, err := st.Tick(t) // 每拍空闲且到间隔，应恰好发一次探测
		if err != nil || !sent || st.inspected > 1 {
			return false
		}
	}
	return st.probes == m
}

// Probes 返回连续无响应的探测次数。
func (s *State) Probes() int { return s.probes }

// Dead 返回连接是否已被判死。
func (s *State) Dead() bool { return s.dead }

// LastActive 返回最后一次活动时刻。
func (s *State) LastActive() int64 { return s.lastActive }
