package drain

import "sync/atomic"

// atomicBool 用于保证每个 release 回调至多生效一次，
// 即使被多个 goroutine 并发调用。
type atomicBool struct {
	v atomic.Bool
}

func (b *atomicBool) cas(old, new bool) bool {
	return b.v.CompareAndSwap(old, new)
}
