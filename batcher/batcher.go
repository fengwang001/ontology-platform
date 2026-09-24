package batcher

import "time"

// Reason 为攒批触发原因。
type Reason int

const (
	None Reason = iota
	Count // 条数达到 MaxCount
	Bytes // 累计字节达到 MaxBytes（含超大单条单独成批）
	Timer // 等待 MaxWait 到期，或 MaxWait<=0 的立即成批
)

// NewTimer 用注入方式造定时器，返回信号通道与停止函数。生产用 time.NewTimer，
// 测试用手动通道。
type NewTimer func(d time.Duration) (<-chan time.Time, func())

// Config 为攒批参数。MaxWait<=0 表示每条立即成批。
type Config struct {
	MaxCount int
	MaxBytes int
	MaxWait  time.Duration
	NewTimer NewTimer
}

// DefaultTimer 是生产环境的定时器构造。
func DefaultTimer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, t.Stop
}

// Batcher 是单个待发批次的攒批状态机。非并发安全，由组提交循环独占。
type Batcher struct {
	cfg   Config
	n     int
	bytes int
	c     <-chan time.Time
	stop  func()
}

func New(cfg Config) *Batcher {
	if cfg.NewTimer == nil {
		cfg.NewTimer = DefaultTimer
	}
	return &Batcher{cfg: cfg}
}

// Len 为当前批次条数。
func (b *Batcher) Len() int { return b.n }

// Bytes 为当前批次累计载荷字节。
func (b *Batcher) Bytes() int { return b.bytes }

// Wait 返回当前批次的超时信号通道；无待发条目时为 nil（select 永不命中）。
func (b *Batcher) Wait() <-chan time.Time { return b.c }

// Add 收入一条 size 字节的请求，返回触发原因（None 表示继续攒）。
// 首批首条进入时启动等待定时器；MaxWait<=0 时该条立即成批。
func (b *Batcher) Add(size int) Reason {
	if b.n == 0 {
		if b.cfg.MaxWait > 0 {
			b.c, b.stop = b.cfg.NewTimer(b.cfg.MaxWait)
		} else {
			b.c, b.stop = nil, nil
		}
	}
	b.n++
	b.bytes += size
	if b.cfg.MaxWait <= 0 {
		return Timer
	}
	if b.cfg.MaxCount > 0 && b.n >= b.cfg.MaxCount {
		return Count
	}
	if b.cfg.MaxBytes > 0 && b.bytes >= b.cfg.MaxBytes {
		return Bytes
	}
	return None
}

// Reset 在批次发出后清空状态并停止定时器。
func (b *Batcher) Reset() {
	if b.stop != nil {
		b.stop()
	}
	b.n, b.bytes = 0, 0
	b.c, b.stop = nil, nil
}
