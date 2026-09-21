// Package lease 实现单个资源的租约状态机：授予、续期、过期与主动
// 释放。时间完全来自构造时注入的 now 函数，不使用任何后台定时器。
package lease

import (
	"sync"
	"time"

	"ontology/fence"
)

// Status 是租约的只读快照。无人持有时 Holder 为空、Remaining 为 0，
// Token 仍如实报告该资源最近授予的令牌（未授予过则为 0）。
type Status struct {
	Held      bool
	Holder    string
	Remaining time.Duration
	Token     fence.Token
}

// Lease 是单个资源的租约，并发安全。
type Lease struct {
	mu     sync.Mutex
	now    func() time.Time
	fence  *fence.Fence
	holder string
	expiry time.Time
	token  fence.Token
}

// New 创建一个租约，now 为注入时钟。
func New(now func() time.Time) *Lease {
	return &Lease{now: now, fence: fence.New()}
}

// held 报告在时刻 now 租约是否有效：左闭右开，now 等于到期时间即过期。
func (l *Lease) held(now time.Time) bool {
	return l.holder != "" && now.Before(l.expiry)
}

// Acquire 尝试获取租约。成功时授予新的围栏令牌并返回；租约仍被他人
// 有效持有时返回 *HeldError（errors.Is 判定 ErrHeld）。
func (l *Lease) Acquire(holder string, ttl time.Duration) (fence.Token, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.held(now) {
		return 0, &HeldError{Holder: l.holder, Remaining: l.expiry.Sub(now)}
	}
	l.token = l.fence.Grant()
	l.holder = holder
	l.expiry = now.Add(ttl)
	return l.token, nil
}

// Renew 由持有者把到期时间推迟到 now+ttl（不累加），令牌不变。
// 非持有者或租约已过期时返回错误且不改变任何状态。
func (l *Lease) Renew(holder string, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.held(now) {
		return ErrNotHeld
	}
	if l.holder != holder {
		return ErrNotHolder
	}
	l.expiry = now.Add(ttl)
	return nil
}

// Release 由持有者主动释放租约，立即生效。非持有者或无人持有时
// 返回错误且不改变状态。
func (l *Lease) Release(holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held(l.now()) {
		return ErrNotHeld
	}
	if l.holder != holder {
		return ErrNotHolder
	}
	l.holder = ""
	l.expiry = time.Time{}
	return nil
}

// AcceptWrite 校验一次携带令牌的写入：租约须被有效持有，且令牌不
// 得低于该资源的围栏水位。
func (l *Lease) AcceptWrite(token fence.Token) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held(l.now()) {
		return ErrNotHeld
	}
	return l.fence.Accept(token)
}

// Status 返回租约的只读快照，过期判定按注入时钟在查询时完成。
func (l *Lease) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.held(now) {
		return Status{
			Held:      true,
			Holder:    l.holder,
			Remaining: l.expiry.Sub(now),
			Token:     l.token,
		}
	}
	return Status{Token: l.token}
}
