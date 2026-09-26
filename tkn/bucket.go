// Package tkn 是令牌桶本体：保存容量、速率与当前令牌数，
// 不依赖 lim/api 中的任何包。
package tkn

// Bucket 是单个令牌桶。并发安全由上层 lim 保证。
type Bucket struct {
	capacity int64
	rate     int64
	tokens   int64

	// refillSteps 记录最近一次 Refill 中「逐令牌递增」的次数。
	// 正确实现用一次乘法完成补令牌，因此恒为 0。非导出，仅供包内测试读取。
	refillSteps int64
}

// New 创建一个初始满桶：tokens 等于 capacity。
// capacity、rate 的合法性由上层 api 校验，此处假定均 >= 1。
func New(capacity, rate int64) *Bucket {
	return &Bucket{
		capacity: capacity,
		rate:     rate,
		tokens:   capacity,
	}
}

// Refill 补入 elapsed*rate 个令牌并按 capacity 封顶。
// 用一次乘法完成，O(1)，refillSteps 恒为 0；elapsed 由上层保证 >= 0。
func (b *Bucket) Refill(elapsed int64) {
	b.refillSteps = 0
	if elapsed <= 0 {
		return
	}
	tokens := b.tokens + elapsed*b.rate
	if tokens > b.capacity {
		tokens = b.capacity
	}
	b.tokens = tokens
}

// TryConsume 尝试消费 need 个令牌：令牌足够则扣减并返回 true，
// 否则令牌数不变并返回 false。need 合法性由上层校验。
func (b *Bucket) TryConsume(need int64) bool {
	if b.tokens >= need {
		b.tokens -= need
		return true
	}
	return false
}

// Tokens 返回当前令牌数。
func (b *Bucket) Tokens() int64 {
	return b.tokens
}
