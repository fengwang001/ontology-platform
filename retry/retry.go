package retry

import "errors"

// ErrConfig 表示重试预算配置非法。
var ErrConfig = errors.New("retry: invalid policy config")

// Policy 是重试预算：最大尝试数、退避初值与退避上限。
type Policy struct {
	MaxAttempts int
	Initial     int64
	Cap         int64
}

// New 校验并构造 Policy。M∈[1,20]，d0∈[1,1e6]，cap∈[d0,1e9]。
func New(m int, d0, cap int64) (Policy, error) {
	if m < 1 || m > 20 || d0 < 1 || d0 > 1_000_000 || cap < d0 || cap > 1_000_000_000 {
		return Policy{}, ErrConfig
	}
	return Policy{MaxAttempts: m, Initial: d0, Cap: cap}, nil
}

// Backoff 返回第 k 次尝试失败后的退避时长 min(d0*2^(k-1), cap)。
// k∈[1,20]；中间值最大为 1e6*2^19，远小于 int64 上限。
func (p Policy) Backoff(k int) int64 {
	d := p.Initial
	for i := 1; i < k; i++ {
		if d >= p.Cap || d > p.Cap/2 {
			return p.Cap
		}
		d *= 2
	}
	if d > p.Cap {
		return p.Cap
	}
	return d
}

// CanRetry 报告第 k 次尝试失败后是否还在预算内（k<M）。
func (p Policy) CanRetry(k int) bool { return k < p.MaxAttempts }
