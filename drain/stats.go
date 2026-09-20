package drain

import (
	"sync"
	"time"
)

// Stats 是 Gate 的计数快照，各字段语义见包注释。
//
// Admitted 累计放行次数，Rejected 累计拒绝次数，InFlight 为当前未 release
// 的在途数量，Done 表示停机流程是否已经结束（成功或超时均算结束）。
type Stats struct {
	Admitted int
	Rejected int
	InFlight int
	Done     bool
}

// Gate 是一个支持优雅停机的在途请求闸门。零值不可用，必须通过 New 创建。
type Gate struct {
	mu       sync.Mutex
	now      func() time.Time
	admitted int
	rejected int
	inFlight int

	shuttingDown bool
	done         bool
	result       error
	deadline     time.Time
	hasDeadline  bool

	// zeroCh 在每次 inFlight 归零时关闭一次；随后被替换为新 channel。
	zeroCh chan struct{}
	// doneCh 在停机结束（成功或超时）时关闭，且只关闭一次。
	doneCh chan struct{}
}
