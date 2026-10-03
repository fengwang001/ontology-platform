// Package retry 计算指数退避并判定重试预算。
package retry

import "errors"

// ErrConfig 表示重试配置非法。
var ErrConfig = errors.New("retry: invalid config")

// Policy 描述重试与退避参数。
type Policy struct {
	MaxAttempts int   // M：最大尝试数，1..20
	Initial     int64 // d0：初始退避，1..1e6
	Cap         int64 // cap：退避上限，d0..1e9
}

// Validate 校验参数取值范围。
func (p Policy) Validate() error {
	if p.MaxAttempts < 1 || p.MaxAttempts > 20 {
		return ErrConfig
	}
	if p.Initial < 1 || p.Initial > 1_000_000 {
		return ErrConfig
	}
	if p.Cap < p.Initial || p.Cap > 1_000_000_000 {
		return ErrConfig
	}
	return nil
}

// Backoff 返回第 k 次尝试失败后的退避时长。
func Backoff(p Policy, k int) int64 {
	d := p.Initial
	for i := 1; i < k; i++ {
		if d >= p.Cap {
			return p.Cap
		}
		d *= 2
		if d > p.Cap || d < 0 {
			return p.Cap
		}
	}
	if d > p.Cap {
		return p.Cap
	}
	return d
}

// CanRetry 报告第 k 次失败后是否还有重试预算。
func CanRetry(p Policy, k int) bool {
	return k < p.MaxAttempts
}

// WithinGlobal 报告新排队时刻 g 是否落在总时限之内（恰等不算在内）。
func WithinGlobal(g, t0, sc int64, scSet bool) bool {
	return !scSet || g < t0+sc
}
