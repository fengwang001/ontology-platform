// Package lease 实现单个租约的栅栏令牌、持锁者与到期判定。不依赖其他包。
package lease

import "errors"

// 可判定的哨兵错误。
var (
	ErrStaleToken = errors.New("lease: stale token")   // token 不是当前令牌，被栅栏
	ErrExpired    = errors.New("lease: lease expired") // 已到期，不能续期
)

// Lease 是单个命名租约的状态。
type Lease struct {
	token  int
	owner  string
	expiry int
}

// New 构造一个租约：持锁者 owner、栅栏令牌 token、到期时刻 expiry。
func New(owner string, token, expiry int) *Lease {
	return &Lease{token: token, owner: owner, expiry: expiry}
}

func (l *Lease) Token() int    { return l.token }
func (l *Lease) Owner() string { return l.owner }
func (l *Lease) Expiry() int   { return l.expiry }

// Renew 心跳续期：先验栅栏令牌，再验存活期，全部通过才把 expiry 延长为 now+ttl。
// 任一校验失败都直接返回，不改任何状态（失败不留痕）。
func (l *Lease) Renew(token, now, ttl int) error {
	if token != l.token {
		return ErrStaleToken
	}
	if now >= l.expiry {
		return ErrExpired
	}
	l.expiry = now + ttl
	return nil
}

// Expired 左闭判定：now 恰好等于 expiry 也算到期。
func (l *Lease) Expired(now int) bool { return now >= l.expiry }
