package ontology

// discardLogger 默认丢弃日志。
type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

// Cache 是由 CDC 栅栏驱动的维表查询缓存（骨架，后续填充）。
type Cache struct {
	log Logger
}

// Option 配置 Cache。
type Option func(*Cache)

// WithLogger 注入日志钩子。
func WithLogger(l Logger) Option {
	return func(c *Cache) { c.log = l }
}

// WithTrackedKeyLimit 设置跟踪键数上限（骨架，后续生效）。
func WithTrackedKeyLimit(n int) Option {
	return func(c *Cache) { _ = n }
}

func NewCache(src *Source, opts ...Option) *Cache {
	c := &Cache{log: discardLogger{}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
