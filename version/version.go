// Package version 提供缓存失效传播所用的单调版本号。
// 零值表示"无版本"，任何非零版本都新于零值。
package version

import "sync"

// Version 是单调递增的版本号，零值表示无版本。
type Version uint64

// None 是零值版本，表示"无版本"。
const None Version = 0

// IsZero 报告是否为无版本。
func (v Version) IsZero() bool { return v == None }

// Compare 比较两个版本：a 较新返回 1，相等返回 0，a 较旧返回 -1。
func Compare(a, b Version) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// After 报告 v 是否严格新于 o。
func (v Version) After(o Version) bool { return v > o }

// Before 报告 v 是否严格旧于 o。
func (v Version) Before(o Version) bool { return v < o }

// Equal 报告两个版本是否相等。
func (v Version) Equal(o Version) bool { return v == o }

// Max 返回两者中较新的版本。
func Max(a, b Version) Version {
	if a > b {
		return a
	}
	return b
}

// Allocator 分配单调递增的版本号，并发安全。
type Allocator struct {
	mu   sync.Mutex
	next uint64
}

// NewAllocator 返回从版本 1 开始分配的分配器。
func NewAllocator() *Allocator { return &Allocator{} }

// Next 分配下一个版本号，严格单调递增，永不返回零值。
func (a *Allocator) Next() Version {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.next++
	return Version(a.next)
}
