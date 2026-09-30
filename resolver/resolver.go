package resolver

import (
	"errors"
	"sync"
)

// ErrEmptyName 在名字为空时返回，优先级最高的拒绝原因。
var ErrEmptyName = errors.New("resolver: empty name")

// ErrCycle 在缓存与上游合起来构成别名环时返回。
var ErrCycle = errors.New("resolver: alias cycle detected")

// ErrChainTooLong 在别名记录超过 MaxAliasRecords 条时返回。
var ErrChainTooLong = errors.New("resolver: alias chain too long")

// Logger 接收解析过程日志：输入、输出与每一步的判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// Clock 返回单调毫秒时间；默认使用 time.Now。测试可注入固定时钟。
type Clock func() int64

// Config 构造解析缓存的参数。
type Config struct {
	Capacity int
	Upstream Upstream
	Clock    Clock
	Logger   Logger
}

// Cache 是带别名链解析能力的并发安全缓存。
type Cache struct {
	capacity int
	upstream Upstream
	now      Clock
	log      Logger

	mu sync.Mutex
	// store 按名字保存唯一条目（同名替换）。
	store map[Name]*entry
	// rootCalls 合并对同一根名字的并发 Resolve 调用。
	rootCalls map[Name]*inflightCall
	// upCalls 合并同一名字上的并发上游查询。
	upCalls map[Name]*inflightUpstream
}

type inflightCall struct {
	done chan struct{}
	res  *Result
	err  error
}

type inflightUpstream struct {
	done chan struct{}
	ans  Answer
	err  error
}

// New 创建容量为 C 的解析缓存。
func New(cfg Config) *Cache {
	c := &Cache{
		capacity:  cfg.Capacity,
		upstream:  cfg.Upstream,
		now:       cfg.Clock,
		log:       cfg.Logger,
		store:     make(map[Name]*entry),
		rootCalls: make(map[Name]*inflightCall),
		upCalls:   make(map[Name]*inflightUpstream),
	}
	if c.now == nil {
		c.now = defaultClock
	}
	return c
}

// Len 返回当前缓存条目数（含可能已到期但尚未清除的条目）。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.store)
}
