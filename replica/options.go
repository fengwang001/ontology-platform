package replica

import "io"

type config struct {
	logWriter io.Writer
}

// Option 配置 NewSyncSet 的可选项。
type Option func(*config)

// WithLogWriter 将逐步输入、同步副本集与判定依据日志写入 w。
// 默认为 os.Stderr；传入 nil 等价于丢弃日志。
func WithLogWriter(w io.Writer) Option {
	return func(c *config) { c.logWriter = w }
}
