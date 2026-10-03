// Package report 定义采样器的丢弃摘要及其交付原因。
package report

import "errors"

// Reason 说明丢弃计数是通过哪条路径交付的。
type Reason int

const (
	// Rolled 表示条目滚动到新窗口，旧窗口的丢弃计数随新记录交付。
	Rolled Reason = iota + 1
	// Evicted 表示键表满导致条目被淘汰。
	Evicted
	// Closed 表示 Flush 显式关闭已结束窗口的未交付计数。
	Closed
)

// Summary 汇报某个 (租户, 键) 在某一窗口内被丢弃的日志条数。
type Summary struct {
	Tenant  string
	Key     string
	Window  int64
	Dropped int64
	Reason  Reason
}

var (
	// ErrInvalidArgument 表示参数非法。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrClockSkew 表示 now 小于已接受的最大时间。
	ErrClockSkew = errors.New("clock skew")
	// ErrTenantLimit 表示租户数超过 Tmax。
	ErrTenantLimit = errors.New("tenant limit reached")
)

// Sink 接收 Flush 交付的摘要；返回错误会立即中止 Flush。
type Sink func(Summary) error
