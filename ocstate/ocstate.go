// Package ocstate 维护单台下游服务器的过载状态：
// 当前通告的百分比 p、欠额 D、到期时刻 e、在途数与已接受的最大 seq。
// 到期失效是时间的纯函数，读取时惰性求值，不扫描其他服务器。
package ocstate

import "errors"

var (
	// ErrStaleReport 表示通告过期：seq 未严格大于已接受的最大 seq（相等也算）。
	ErrStaleReport = errors.New("ocstate: stale report")
	// ErrNoInFlight 表示在途数为 0 时仍调用 Done。
	ErrNoInFlight = errors.New("ocstate: no in-flight message")
)

// Server 为单台下游服务器的过载与在途状态。
type Server struct {
	ID       int64
	P        int64 // 当前通告的减载百分比（未到期才有效）
	D        int64 // 欠额（未到期才有效）
	Expiry   int64 // 限制到期时刻，now >= Expiry 即失效
	InFlight int64 // 在途消息数
	MaxSeq   int64 // 已接受通告的最大 seq

	Arrivals  int64 // 到达数（在途已满被跳过的不计）
	Forwarded int64 // 转发数
	Dropped   int64 // 减载数
}

// NewServer 创建一台无限制、无在途的服务器状态。
func NewServer(id int64) *Server {
	return &Server{ID: id}
}

func (s *Server) expired(now int64) bool { return now >= s.Expiry }

// EffectiveP 返回 now 时刻有效的 p；到期后视为 0。
func (s *Server) EffectiveP(now int64) int64 {
	if s.expired(now) {
		return 0
	}
	return s.P
}

// EffectiveD 返回 now 时刻有效的欠额；到期后视为 0。
func (s *Server) EffectiveD(now int64) int64 {
	if s.expired(now) {
		return 0
	}
	return s.D
}

// Report 接受一条过载通告。percent、validity 的取值校验由调用方完成。
// validity=0 表示撤销限制：p 置 0、欠额 D 清零；
// 否则 p=percent、e=now+validity，D 保留（此前已到期的部分自然归零）。
func (s *Server) Report(seq, percent, validity, now int64) error {
	if seq <= s.MaxSeq {
		return ErrStaleReport
	}
	s.MaxSeq = seq
	if validity == 0 {
		s.P, s.D, s.Expiry = 0, 0, 0
		return nil
	}
	s.D = s.EffectiveD(now)
	s.P = percent
	s.Expiry = now + validity
	return nil
}

// Done 将在途数减 1；在途为 0 时报 ErrNoInFlight。
func (s *Server) Done() error {
	if s.InFlight == 0 {
		return ErrNoInFlight
	}
	s.InFlight--
	return nil
}
