package retry

import (
	"math"
	"time"
)

// Policy 描述一次重试调度的全部参数。
type Policy struct {
	MaxAttempts int           // 总尝试次数上限（含第一次），<=0 视为 1
	Base        time.Duration // 首次退避
	Factor      int           // 每次乘的倍数，<=1 视为不增长
	Cap         time.Duration // 单次退避上限，<=0 表示不设上限
	JitterPct   int           // 抖动百分比 0..100
}

// maxAttempts 返回归一化后的最大尝试次数，至少为 1。
func (p Policy) maxAttempts() int {
	if p.MaxAttempts <= 0 {
		return 1
	}
	return p.MaxAttempts
}

// baseDelay 返回第 k 次等待（k 从 1 起）未加抖动的基准间隔：
// Base*Factor^(k-1)，超过 Cap 则取 Cap；Factor<=1 时恒为 Base。
func (p Policy) baseDelay(k int) time.Duration {
	d := p.Base
	if d < 0 {
		d = 0
	}
	if p.Factor <= 1 {
		return d
	}
	factor := time.Duration(p.Factor)
	for i := 1; i < k; i++ {
		if p.Cap > 0 && d >= p.Cap {
			return p.Cap
		}
		if d > time.Duration(math.MaxInt64)/factor {
			d = time.Duration(math.MaxInt64)
			break
		}
		d *= factor
	}
	return d
}

// delay 返回第 k 次等待的实际间隔。JitterPct>0 时按
// d*(1±p/100) 做对称抖动，且每次等待只调用一次 rnd。
func (p Policy) delay(k int, rnd func() float64) time.Duration {
	d := p.baseDelay(k)
	jitter := p.JitterPct
	if jitter <= 0 {
		return d
	}
	if jitter > 100 {
		jitter = 100
	}
	r := rnd()
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		r = math.Nextafter(1, 0)
	}
	factor := 1 + r*float64(jitter)/100
	out := time.Duration(float64(d) * factor)
	if out < 0 {
		return 0
	}
	return out
}
