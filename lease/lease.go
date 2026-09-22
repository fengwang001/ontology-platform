// Package lease 实现单个资源的租约状态机：授予、续期、过期、主动释放。
//
// 时间只来自注入的时钟 now，过期判定在每次被操作时惰性完成，
// 不使用任何后台定时器。过期区间为左闭右开：now == 到期时间 即算过期。
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/fence"
)

// 可判定的哨兵错误，配合 errors.Is 使用。
var (
	// ErrHeld 表示租约被他人持有（Acquire 冲突）。
	ErrHeld = errors.New("lease: held by another client")
	// ErrNotHolder 表示操作者不是当前持有者（Renew/Release 越权或已过期）。
	ErrNotHolder = errors.New("lease: not the holder")
	// ErrNotHeld 表示资源当前无人持有。
	ErrNotHeld = errors.New("lease: not held")
)

// ConflictError 是 Acquire 冲突时返回的错误，携带当前持有者与剩余时长。
type ConflictError struct {
	Holder    string
	Remaining time.Duration
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("lease: held by %q for %s more", e.Holder, e.Remaining)
}

// Is 使 errors.Is(err, ErrHeld) 成立。
func (e *ConflictError) Is(target error) bool { return target == ErrHeld }

// Info 是租约状态的只读快照。无人持有时 Holder 为空、Remaining 为 0、Token 为 0。
type Info struct {
	Held      bool
	Holder    string
	Remaining time.Duration
	Token     fence.Token
}

// Lease 是单个资源的租约状态机，并发安全。
type Lease struct {
	now   func() time.Time
	fence *fence.Fence

	mu     sync.Mutex
	held   bool
	holder string
	expiry time.Time
	token  fence.Token
}

// New 以注入时钟 now 与围栏 f 创建租约状态机。
func New(now func() time.Time, f *fence.Fence) *Lease {
	return &Lease{now: now, fence: f}
}

// expireLocked 惰性过期：now 不严格早于到期时间即失效并清零状态。
// 调用者必须持有 l.mu。
func (l *Lease) expireLocked() {
	if l.held && !l.now().Before(l.expiry) {
		l.held = false
		l.holder = ""
		l.token = 0
	}
}

// Acquire 尝试获取租约。成功时分配新的围栏令牌并返回；
// 租约被他人有效持有时返回 *ConflictError。
func (l *Lease) Acquire(holder string, ttl time.Duration) (fence.Token, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked()
	if l.held {
		return 0, &ConflictError{Holder: l.holder, Remaining: l.expiry.Sub(l.now())}
	}
	l.held = true
	l.holder = holder
	l.expiry = l.now().Add(ttl)
	l.token = l.fence.Issue()
	return l.token, nil
}

// Renew 由持有者续期，到期时间重置为 now+ttl（不累加），令牌不变。
// 非持有者（含已过期）返回 ErrNotHolder，且不改变任何状态。
func (l *Lease) Renew(holder string, ttl time.Duration) (fence.Token, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked()
	if !l.held {
		return 0, ErrNotHolder
	}
	if l.holder != holder {
		return 0, ErrNotHolder
	}
	l.expiry = l.now().Add(ttl)
	return l.token, nil
}

// Release 由持有者主动释放，租约立即失效。
// 非持有者返回 ErrNotHolder；无人持有（含重复释放）返回 ErrNotHeld。
func (l *Lease) Release(holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked()
	if !l.held {
		return ErrNotHeld
	}
	if l.holder != holder {
		return ErrNotHolder
	}
	l.held = false
	l.holder = ""
	l.token = 0
	return nil
}

// CheckWrite 校验一次携带令牌的写入：先判定租约是否仍然有效，
// 资源无人持有（含过期未重占）时返回 ErrNotHeld；否则交给围栏按水位
// 判定，令牌过旧返回 fence.ErrStaleToken。被拒绝的写入无任何副作用，
// 尤其不会移动围栏水位。
func (l *Lease) CheckWrite(token fence.Token) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked()
	if !l.held {
		return ErrNotHeld
	}
	return l.fence.Check(token)
}

// Info 返回当前状态的只读快照，无人持有时为零值，不残留上一任信息。
func (l *Lease) Info() Info {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked()
	if !l.held {
		return Info{}
	}
	return Info{
		Held:      true,
		Holder:    l.holder,
		Remaining: l.expiry.Sub(l.now()),
		Token:     l.token,
	}
}
