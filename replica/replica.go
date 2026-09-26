// Package replica 维护副本状态：版本号与 map，负责 delta 的去重/乱序/应用判定。
package replica

import (
	"errors"
	"sync"

	"ontology/delta"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrNegativeVersion = errors.New("replica: negative version")
	ErrInvalidRange    = errors.New("replica: To <= From")
	ErrEmptyKey        = errors.New("replica: empty change key")
	ErrGap             = errors.New("replica: out-of-order delta (gap)")
)

// Replica 是一个内存副本：一张 map 状态加单调递增的版本号。
type Replica struct {
	mu      sync.RWMutex
	version int
	state   map[string]int
	// lastReads 记录最近一次 Apply 为判断去重/乱序而读取的「已应用版本记录」个数。
	// 非导出，不出现在任何公开接口里。
	lastReads int
}

// New 返回版本 0、空状态的副本。
func New() *Replica {
	return &Replica{state: make(map[string]int)}
}

// Apply 按序应用一个 delta。重复（From<version）幂等跳过；乱序与非法 delta
// 整体拒绝且状态不变。所有校验先于任何状态修改。
func (r *Replica) Apply(d delta.Delta) error {
	if d.From < 0 || d.To < 0 {
		return ErrNegativeVersion
	}
	if d.To <= d.From {
		return ErrInvalidRange
	}
	for _, c := range d.Changes {
		if c.Key == "" {
			return ErrEmptyKey
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 去重/乱序判定：只读取当前版本号这一条记录，O(1)。
	r.lastReads = 1
	switch {
	case d.From > r.version:
		return ErrGap
	case d.From < r.version:
		return nil // 已应用，幂等跳过
	}
	for _, c := range d.Changes {
		delta.ApplyChange(r.state, c)
	}
	r.version = d.To
	return nil
}

// Version 返回当前版本号，并发安全。
func (r *Replica) Version() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// State 返回当前状态的副本，并发安全；调用方修改不影响内部状态。
func (r *Replica) State() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int, len(r.state))
	for k, v := range r.state {
		out[k] = v
	}
	return out
}
