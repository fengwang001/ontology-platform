package settlement

import "sync"

// Engine 是全部作业状态的并发安全容器。
//
// 时钟模型：时间为单调递增整数。引擎维护全局 lastTick；每个公开操作携带
// 调用方认定的当前时刻 now，now < lastTick 即报时钟回退。被拒绝的操作
// 不会推进 lastTick（见各操作方法），因此“被拒绝的操作不得推进任何时钟”。
//
// 并发模型：单一互斥锁串行化所有变更，所有调用结果等价于某个串行顺序。
// 读操作（LastTick / 结算结果快照）同样取锁以保证内存可见性。
type Engine struct {
	mu          sync.Mutex
	assignments map[string]*Assignment
	lastTick    int
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{assignments: map[string]*Assignment{}}
}

// LastTick 返回引擎最近一次接受操作的时刻；无任何被接受操作时为 0。
func (e *Engine) LastTick() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastTick
}

// checkClock 校验时钟单调性并在合法时推进全局时钟。
// 调用方必须在状态校验全部通过后才调用本方法；这样被拒绝的操作
// 不会推进时钟。
func (e *Engine) checkClock(now int) *ClassifiedError {
	if now < e.lastTick {
		return ErrClockRollback
	}
	e.lastTick = now
	return nil
}

// rollback 只检查时钟回退而不推进时钟。用于在“已关闭/状态”等
// 更低优先级错误之前锁定时钟回退（固定优先级的第 2 位）。
func (e *Engine) rollback(now int) *ClassifiedError {
	if now < e.lastTick {
		return ErrClockRollback
	}
	return nil
}
