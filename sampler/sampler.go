// Package sampler 实现合格计数采样与连续失败暂停状态机。
//
// 本包不自有锁，也不做时间合法性校验；调用方（mirror 包）负责
// 串行化并保证 now 单调非降且在合法范围内。
package sampler

import "fmt"

// Sampler 按 1/k 对合格请求确定性采样，并在连续失败达到阈值后暂停。
type Sampler struct {
	k int64 // 采样间隔，1..1e6
	e int64 // 连续失败阈值，1..1e6
	p int64 // 暂停时长（毫秒），1..1e9

	c           int64 // 合格计数
	s           int64 // 当前连续失败数
	pausedUntil int64 // 暂停截止时刻，初值 0
}

// New 构造 Sampler，校验 k、e、p 的取值范围。
func New(k, e, p int64) (*Sampler, error) {
	if k < 1 || k > 1e6 {
		return nil, fmt.Errorf("sampler: k=%d 超出 [1,1e6]", k)
	}
	if e < 1 || e > 1e6 {
		return nil, fmt.Errorf("sampler: e=%d 超出 [1,1e6]", e)
	}
	if p < 1 || p > 1e9 {
		return nil, fmt.Errorf("sampler: p=%d 超出 [1,1e9]", p)
	}
	return &Sampler{k: k, e: e, p: p}, nil
}

// Admit 判定一个合格请求是否被采中。
// 若 now 处于暂停期，返回 paused=true 且不推进 c；
// 否则 c 加 1，当 c mod k == 0 时 sampled=true。
func (s *Sampler) Admit(now int64) (sampled, paused bool) {
	if now < s.pausedUntil {
		return false, true
	}
	s.c++
	return s.c%s.k == 0, false
}

// OnError 记录一次镜像错误。暂停期间（now < pausedUntil）不累计；
// 否则 s 加 1，达到阈值时令 pausedUntil = now+p 并把 s 清零。
func (s *Sampler) OnError(now int64) {
	if now < s.pausedUntil {
		return
	}
	s.s++
	if s.s >= s.e {
		s.pausedUntil = now + s.p
		s.s = 0
	}
}

// OnSuccess 记录一次镜像成功。仅在非暂停期间（now >= pausedUntil）清零 s。
func (s *Sampler) OnSuccess(now int64) {
	if now >= s.pausedUntil {
		s.s = 0
	}
}

// PausedUntil 返回当前暂停截止时刻。
func (s *Sampler) PausedUntil() int64 { return s.pausedUntil }

// Qualified 返回合格计数 c。
func (s *Sampler) Qualified() int64 { return s.c }

// ConsecFails 返回当前连续失败数 s。
func (s *Sampler) ConsecFails() int64 { return s.s }
