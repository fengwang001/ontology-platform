package gateway

import (
	"time"

	"ontology/record"
)

// ExecFunc 是真正的写入执行函数：入参为请求体，返回结果或错误。
// 对同一个幂等键，它保证至多在首次提交 / 失败重试时被调用。
type ExecFunc func(body []byte) ([]byte, error)

// Config 构造网关时注入的依赖。
type Config struct {
	// Now 是注入时钟；必填，代码中不会自行调用 time.Now。
	Now func() time.Time
	// TTL 是记录存活时长；从记录发起执行的时刻起算，左闭右开。
	TTL time.Duration
	// OnJoin 在一次提交合流到「执行中」记录、决定等待前被回调（持有内部锁）。
	// 可为空；主要用于测试/演示对并发合流做确定性观测。
	OnJoin func(key string)
}

// Outcome 是一次提交对外暴露的结果。
type Outcome struct {
	// Result 是首次（或成功那一次）执行返回的结果，回放时完全相同。
	Result []byte
	// Err 是执行函数原样返回的错误；成功为 nil。
	Err error
	// Replayed 为 false 表示本次真正执行；true 表示回放已有结果 / 合流等待。
	Replayed bool
	// State 是提交返回后该键所处的状态。
	State record.State
}

// Info 是某键的只读查询结果。
type Info struct {
	// Known 为 false 表示键不存在或已过期，其余字段均为零值。
	Known bool
	// State 是当前状态。
	State record.State
	// Remaining 是按注入时钟计算的剩余存活时长。
	Remaining time.Duration
	// HasResult 表示是否已固定可回放的成功结果。
	HasResult bool
}
