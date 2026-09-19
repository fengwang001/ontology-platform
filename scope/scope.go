package scope

import (
	"sync"
	"time"
)

// Scope 是一棵带截止时间与取消传播的作用域树上的一个节点。
//
// 一个作用域一旦结束即为终态：Err/Reason/Origin 永远保持第一次
// 结束时的判定结果，结束信号会向所有后代传播。
type Scope struct {
	mu       sync.Mutex
	now      func() time.Time
	parent   *Scope
	deadline time.Time

	children map[*Scope]struct{}
	hooks    []func()

	done   chan struct{}
	ended  bool
	err    error
	reason Reason
	origin *Scope
}

// 各方法按职责拆分在同包的其他文件中：
//   - lifecycle.go: NewRoot / Child / 结束与传播
//   - state.go:     Deadline / Done / Err / Reason / Origin
//   - cancel.go:    Cancel
//   - tick.go:      Tick
//   - hooks.go:     OnDone
