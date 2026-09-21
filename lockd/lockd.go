// Package lockd 是对外网关：管理多个资源的租约，注入时钟，并暴露
// 携带围栏令牌的写入校验。时间完全来自构造时注入的 now 函数。
package lockd

import (
	"sync"
	"time"

	"ontology/fence"
	"ontology/lease"
)

// 便于调用方判定的错误别名。
var (
	// ErrNotHeld 表示资源当前无人持有（与令牌过旧相区分）。
	ErrNotHeld = lease.ErrNotHeld
	// ErrStaleToken 表示写入令牌低于该资源的围栏水位。
	ErrStaleToken = fence.ErrStale
)

// Status 是某资源租约的只读快照。
type Status = lease.Status

// Daemon 管理多个资源的租约，并发安全。
type Daemon struct {
	mu     sync.Mutex
	now    func() time.Time
	leases map[string]*lease.Lease
}

// New 创建一个网关，now 为注入时钟。
func New(now func() time.Time) *Daemon {
	return &Daemon{now: now, leases: make(map[string]*lease.Lease)}
}

// leaseFor 返回指定资源的租约，不存在则惰性创建。
func (d *Daemon) leaseFor(resource string) *lease.Lease {
	d.mu.Lock()
	defer d.mu.Unlock()
	l, ok := d.leases[resource]
	if !ok {
		l = lease.New(d.now)
		d.leases[resource] = l
	}
	return l
}

// Acquire 尝试获取 resource 的独占租约，成功时返回新的围栏令牌。
func (d *Daemon) Acquire(resource, holder string, ttl time.Duration) (fence.Token, error) {
	return d.leaseFor(resource).Acquire(holder, ttl)
}

// Renew 由持有者续期到 now+ttl，令牌不变。
func (d *Daemon) Renew(resource, holder string, ttl time.Duration) error {
	return d.leaseFor(resource).Renew(holder, ttl)
}

// Release 由持有者主动释放租约。
func (d *Daemon) Release(resource, holder string) error {
	return d.leaseFor(resource).Release(holder)
}

// Write 校验一次携带令牌的写入：资源无人持有时返回 ErrNotHeld，
// 令牌低于水位时返回 ErrStaleToken，二者可区分。
func (d *Daemon) Write(resource string, token fence.Token, _ []byte) error {
	return d.leaseFor(resource).AcceptWrite(token)
}

// Status 返回 resource 的租约快照；无人持有时 Holder 为空、
// Remaining 为 0，不残留上一任的值。
func (d *Daemon) Status(resource string) Status {
	return d.leaseFor(resource).Status()
}
