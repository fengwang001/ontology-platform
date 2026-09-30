// Package loginlimit 按账号与来源两个维度统计登录失败并实施渐进式锁定。
package loginlimit

import (
	"sync"
	"time"
)

// Reason 描述一次尝试被拒绝的可区分原因。
type Reason string

const (
	// ReasonInvalidConfig 创建限流器时参数非法。
	ReasonInvalidConfig Reason = "invalid_config"
	// ReasonEmptyAccount 尝试时账号为空。
	ReasonEmptyAccount Reason = "empty_account"
	// ReasonEmptySource 尝试时来源为空。
	ReasonEmptySource Reason = "empty_source"
	// ReasonClockBackwards 时钟读数早于此前已见到的最大读数。
	ReasonClockBackwards Reason = "clock_backwards"
	// ReasonLocked 任一键正处于锁定期。
	ReasonLocked Reason = "locked"
	// ReasonBadPassword 未锁定但口令错误。
	ReasonBadPassword Reason = "bad_password"
)

// Dimension 表示发生锁定的维度。
type Dimension string

const (
	// DimensionAccount 账号维度。
	DimensionAccount Dimension = "account"
	// DimensionSource 来源维度。
	DimensionSource Dimension = "source"
)

// Config 是限流器的参数。
type Config struct {
	// Window 失败计数的滑动窗口长度 W。
	Window time.Duration
	// Threshold 触发一次锁定所需的窗口内失败数 K。
	Threshold int
	// BaseLock 首次锁定时长 B。
	BaseLock time.Duration
	// MaxLock 单次锁定时长上限 M。
	MaxLock time.Duration
	// Cooldown 距上次锁定截止超过该时长 R 后级别归零。
	Cooldown time.Duration
}

// Result 是一次尝试的结果。
type Result struct {
	// Allowed 为 true 表示放行（未锁定且口令正确）。
	Allowed bool
	// Reason 为拒绝原因；放行时为空字符串。
	Reason Reason
	// UnlockAt 被锁定维度的解锁时刻；取两个被锁维度中较晚的截止时刻。
	UnlockAt time.Time
	// LockedDimensions 当前处于锁定期的维度（去重）。
	LockedDimensions []Dimension
}

// keyState 记录单个键（账号或来源）的状态。
type keyState struct {
	// failures 窗口内的失败时刻（追加后按需清理过期项）。
	failures []time.Time
	// lockUntil 本次锁定的截止时刻；零值表示未在锁定。
	lockUntil time.Time
	// level 已触发的锁定级别 j（0 表示从未触发或已被冷却归零）。
	level int
}

func (s *keyState) locked(at time.Time) bool {
	return at.Before(s.lockUntil)
}

// prune 删除窗口外的失败记录：仅保留严格晚于 at-Window 的时刻。
func (s *keyState) prune(at time.Time, window time.Duration) {
	cutoff := at.Add(-window)
	keep := s.failures[:0]
	for _, f := range s.failures {
		if f.After(cutoff) {
			keep = append(keep, f)
		}
	}
	s.failures = keep
}

// recordFailure 记录一次失败；若窗口内失败数达到 threshold 则触发锁定，
// 返回触发后的级别 j，未触发返回 0。
func (s *keyState) recordFailure(at time.Time, cfg Config) int {
	s.prune(at, cfg.Window)
	s.failures = append(s.failures, at)
	if len(s.failures) < cfg.Threshold {
		return 0
	}

	// 距上次锁定截止已过冷却期，级别归零。
	if s.level > 0 && !at.Before(s.lockUntil.Add(cfg.Cooldown)) {
		s.level = 0
	}
	s.level++

	dur := cfg.BaseLock
	for i := 1; i < s.level; i++ {
		dur *= 2
		if dur >= cfg.MaxLock {
			dur = cfg.MaxLock
			break
		}
	}
	s.lockUntil = at.Add(dur)
	s.failures = s.failures[:0]
	return s.level
}

// Limiter 是并发安全的登录失败限制器。
type Limiter struct {
	cfg Config

	mu      sync.Mutex
	maxSeen time.Time
	keys    map[string]*keyState
}

// New 创建限制器；参数非正或 MaxLock 小于 BaseLock 时返回错误。
func New(cfg Config) (*Limiter, error) {
	if cfg.Window <= 0 || cfg.Threshold <= 0 || cfg.BaseLock <= 0 ||
		cfg.MaxLock <= 0 || cfg.Cooldown <= 0 || cfg.MaxLock < cfg.BaseLock {
		return nil, &ConfigError{Reason: ReasonInvalidConfig}
	}
	return &Limiter{cfg: cfg, keys: make(map[string]*keyState)}, nil
}

// Attempt 处理一次登录尝试。passwordOK 由调用方判定口令是否正确。
func (l *Limiter) Attempt(account, source string, at time.Time, passwordOK bool) Result {
	// 输入校验：账号、来源、时钟回退，按顺序只报第一个原因，且不改任何状态。
	if account == "" {
		return Result{Reason: ReasonEmptyAccount}
	}
	if source == "" {
		return Result{Reason: ReasonEmptySource}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if at.Before(l.maxSeen) {
		return Result{Reason: ReasonClockBackwards}
	}
	l.maxSeen = at

	accountKey := l.keys["a:"+account]
	if accountKey == nil {
		accountKey = &keyState{}
		l.keys["a:"+account] = accountKey
	}
	sourceKey := l.keys["s:"+source]
	if sourceKey == nil {
		sourceKey = &keyState{}
		l.keys["s:"+source] = sourceKey
	}

	// 任一键在锁定中即拒绝；不计失败、不延长锁定。
	accountLocked := accountKey.locked(at)
	sourceLocked := sourceKey.locked(at)
	if accountLocked || sourceLocked {
		res := Result{Reason: ReasonLocked}
		if accountLocked {
			res.LockedDimensions = append(res.LockedDimensions, DimensionAccount)
			res.UnlockAt = accountKey.lockUntil
		}
		if sourceLocked {
			res.LockedDimensions = append(res.LockedDimensions, DimensionSource)
			if sourceKey.lockUntil.After(res.UnlockAt) {
				res.UnlockAt = sourceKey.lockUntil
			}
		}
		return res
	}

	if passwordOK {
		// 成功只清账号键的失败记录与级别；来源键不动。
		// lockUntil 保留，作为上次锁定截止供冷却判定使用。
		accountKey.failures = accountKey.failures[:0]
		accountKey.level = 0
		return Result{Allowed: true}
	}

	accountKey.recordFailure(at, l.cfg)
	sourceKey.recordFailure(at, l.cfg)
	// 即使本次触发了锁定，这次尝试仍只报口令错误。
	return Result{Reason: ReasonBadPassword}
}

// ConfigError 表示创建限制器时参数非法。
type ConfigError struct {
	Reason Reason
}

func (e *ConfigError) Error() string {
	return string(e.Reason)
}
