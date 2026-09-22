package serve

import "errors"

// 三类资源上限超限错误，彼此可判定（用 errors.Is 即可区分），
// 且与语法错误、不可满足错误也互不相同。
var (
	// ErrTooManyRanges：归一化前的区间个数超过 MaxRanges。
	ErrTooManyRanges = errors.New("serve: range count exceeds configured maximum")
	// ErrResponseTooLarge：响应总字节超过 MaxResponseBytes。
	ErrResponseTooLarge = errors.New("serve: response size exceeds configured maximum")
	// ErrBoundaryAttempts：边界串生成在 MaxBoundaryAttempts 次内未成功。
	ErrBoundaryAttempts = errors.New("serve: boundary generation exceeded attempt limit")
)

// Config 是组装器的资源上限配置。零值字段表示采用默认值。
type Config struct {
	// MaxRanges 限制 Range 头中区间元素的个数；<=0 用默认 100。
	MaxRanges int
	// MaxResponseBytes 限制一次响应的总字节（含封装开销）；<=0 用默认 16<<20。
	MaxResponseBytes int64
	// MaxBoundaryAttempts 限制边界串生成重试次数；<=0 用默认 16。
	MaxBoundaryAttempts int
}

const (
	defaultMaxRanges          = 100
	defaultMaxResponseBytes   = int64(16 << 20)
	defaultMaxBoundaryAttempt = 16
)

func (c Config) withDefaults() Config {
	if c.MaxRanges <= 0 {
		c.MaxRanges = defaultMaxRanges
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = defaultMaxResponseBytes
	}
	if c.MaxBoundaryAttempts <= 0 {
		c.MaxBoundaryAttempts = defaultMaxBoundaryAttempt
	}
	return c
}
