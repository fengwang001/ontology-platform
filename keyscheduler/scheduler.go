// Package keyscheduler 实现令牌验证密钥集合的四步轮换调度器。
package keyscheduler

import (
	"log"
	"os"
	"sync"
)

// Phase 是密钥在轮换生命周期中的状态。
type Phase int

const (
	// Published 已发布：可验证、不可签发，等待启用时刻。
	Published Phase = iota
	// Active 活跃：唯一可签发密钥，同时可验证。
	Active
	// SignOff 停签：只验证、不签发，等待退役时刻。
	SignOff
	// Retired 已退役：已从验证集合移除，仅保留标识以防复用。
	Retired
)

func (p Phase) String() string {
	switch p {
	case Published:
		return "published"
	case Active:
		return "active"
	case SignOff:
		return "sign-off"
	case Retired:
		return "retired"
	default:
		return "unknown"
	}
}

type errInvalidParam struct{}

func (errInvalidParam) Error() string {
	return "keyscheduler: C and T must be positive and S must be non-negative"
}

type errClockMovedBackwards struct{}

func (errClockMovedBackwards) Error() string {
	return "keyscheduler: injected clock is earlier than the largest clock seen"
}

type errKeyIDExists struct{}

func (errKeyIDExists) Error() string {
	return "keyscheduler: key id already exists (retired ids must not be reused)"
}

type errPendingActivation struct{}

func (errPendingActivation) Error() string {
	return "keyscheduler: a published key is already pending activation"
}

type errKeyNotFound struct{}

func (errKeyNotFound) Error() string {
	return "keyscheduler: key id not found"
}

type errNotSignOff struct{}

func (errNotSignOff) Error() string {
	return "keyscheduler: key is not in sign-off phase"
}

// ErrInvalidParam 在创建参数非法时返回（C、T 非正或 S 为负）。
var ErrInvalidParam error = errInvalidParam{}

// ErrClockMovedBackwards 在注入时钟早于已见最大读数时返回。
var ErrClockMovedBackwards error = errClockMovedBackwards{}

// ErrKeyIDExists 在轮换的新标识已存在（含已退役）时返回。
var ErrKeyIDExists error = errKeyIDExists{}

// ErrPendingActivation 在已有待启用的新密钥时拒绝轮换。
var ErrPendingActivation error = errPendingActivation{}

// ErrKeyNotFound 在退役未知标识时返回。
var ErrKeyNotFound error = errKeyNotFound{}

// ErrNotSignOff 在密钥状态不是停签时拒绝退役。
var ErrNotSignOff error = errNotSignOff{}

// EarlyRetirementError 表示未到最早退役时刻；Earliest 为该时刻，恰到点即可退役。
type EarlyRetirementError struct {
	Earliest int64
}

func (e *EarlyRetirementError) Error() string {
	return "keyscheduler: key has not reached its earliest retirement time"
}

// Logger 接收操作日志（输入、输出、判定依据）。
type Logger interface {
	Printf(format string, args ...any)
}

// KeyInfo 是一把密钥的对外快照。尚未发生的时刻为 -1。
type KeyInfo struct {
	ID        string
	Phase     Phase
	Published int64
	Activated int64
	SignOffAt int64
	RetiresAt int64
	RetiredAt int64
}

// Scheduler 是并发安全的轮换调度器。
type Scheduler struct {
	mu       sync.Mutex
	cacheTTL int64
	tokenTTL int64
	skew     int64
	maxClock int64
	order    []string
	keys     map[string]*KeyInfo
	logger   Logger
}

// New 创建调度器。initialID 的初始密钥在 now 时刻直接活跃。
// C=cacheTTL、T=tokenTTL 必须为正，S=skew 不可为负，否则返回 ErrInvalidParam。
func New(cacheTTL, tokenTTL, skew int64, initialID string, now int64, logger Logger) (*Scheduler, error) {
	if cacheTTL <= 0 || tokenTTL <= 0 || skew < 0 {
		if logger != nil {
			logger.Printf("New 拒绝: 输入{C:%d T:%d S:%d id:%q now:%d} 判定: C、T 必须为正且 S 不可为负 输出: err=%v",
				cacheTTL, tokenTTL, skew, initialID, now, ErrInvalidParam)
		}
		return nil, ErrInvalidParam
	}
	if logger == nil {
		logger = log.New(os.Stdout, "[keyscheduler] ", log.LstdFlags|log.Lmicroseconds)
	}
	s := &Scheduler{
		cacheTTL: cacheTTL,
		tokenTTL: tokenTTL,
		skew:     skew,
		maxClock: now,
		order:    []string{initialID},
		keys: map[string]*KeyInfo{
			initialID: {
				ID:        initialID,
				Phase:     Active,
				Published: now,
				Activated: now,
				SignOffAt: -1,
				RetiresAt: -1,
				RetiredAt: -1,
			},
		},
		logger: logger,
	}
	logger.Printf("New 成功: 输入{C:%d T:%d S:%d id:%q now:%d} 判定: 初始密钥直接活跃 输出: active=%q",
		cacheTTL, tokenTTL, skew, initialID, now, initialID)
	return s, nil
}

// Rotate 在 now 时刻请求轮换，newID 立即发布（仅可验证），返回启用时刻 now+C。
func (s *Scheduler) Rotate(newID string, now int64) (activateAt int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logf("Rotate 请求: 输入{id:%q now:%d}", newID, now)
	if now < s.maxClock {
		s.logf("Rotate 拒绝: 判定: 时钟回退 now=%d < maxClock=%d 输出: err=%v", now, s.maxClock, ErrClockMovedBackwards)
		return 0, ErrClockMovedBackwards
	}
	// 每个操作先完成所有已到点的启用；惰性推进只由注入时钟决定，不属于被拒绝操作自身的改动。
	s.advanceLocked(now)

	if _, exists := s.keys[newID]; exists {
		s.logf("Rotate 拒绝: 判定: 标识 %q 已存在（含已退役，不可复用） 输出: err=%v 状态不变", newID, ErrKeyIDExists)
		return 0, ErrKeyIDExists
	}
	for _, id := range s.order {
		if s.keys[id].Phase == Published {
			s.logf("Rotate 拒绝: 判定: 已有待启用密钥 %q（启用时刻 %d） 输出: err=%v 状态不变",
				id, s.keys[id].Activated, ErrPendingActivation)
			return 0, ErrPendingActivation
		}
	}

	activateAt = now + s.cacheTTL
	s.keys[newID] = &KeyInfo{
		ID:        newID,
		Phase:     Published,
		Published: now,
		Activated: activateAt,
		SignOffAt: -1,
		RetiresAt: -1,
		RetiredAt: -1,
	}
	s.order = append(s.order, newID)
	s.maxClock = now
	s.logf("Rotate 成功: 判定: 新密钥立即发布（仅可验证），启用时刻恒为 now+C=%d+%d=%d 输出: activateAt=%d 验证集合=%v",
		now, s.cacheTTL, activateAt, activateAt, s.verificationIDsLocked())
	return activateAt, nil
}

// Retire 在 now 时刻退役一把停签密钥；未到点时返回 *EarlyRetirementError。
func (s *Scheduler) Retire(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logf("Retire 请求: 输入{id:%q now:%d}", id, now)
	if now < s.maxClock {
		s.logf("Retire 拒绝: 判定: 时钟回退 now=%d < maxClock=%d 输出: err=%v", now, s.maxClock, ErrClockMovedBackwards)
		return ErrClockMovedBackwards
	}
	s.advanceLocked(now)

	key, exists := s.keys[id]
	if !exists {
		s.logf("Retire 拒绝: 判定: 密钥 %q 不复存在 输出: err=%v 状态不变", id, ErrKeyNotFound)
		return ErrKeyNotFound
	}
	if key.Phase != SignOff {
		s.logf("Retire 拒绝: 判定: 密钥 %q 状态为 %s，不是停签 输出: err=%v 状态不变", id, key.Phase, ErrNotSignOff)
		return ErrNotSignOff
	}
	if now < key.RetiresAt {
		s.logf("Retire 拒绝: 判定: now=%d 早于最早退役时刻 %d（停签时刻 %d + T %d + S %d） 输出: err=*EarlyRetirementError{Earliest:%d} 状态不变",
			now, key.RetiresAt, key.SignOffAt, s.tokenTTL, s.skew, key.RetiresAt)
		return &EarlyRetirementError{Earliest: key.RetiresAt}
	}

	key.Phase = Retired
	key.RetiredAt = now
	s.maxClock = now
	s.logf("Retire 成功: 判定: now=%d >= 最早退役时刻 %d，恰到点即可退役并移出验证集合 输出: 验证集合=%v",
		now, key.RetiresAt, s.verificationIDsLocked())
	return nil
}

// Advance 惰性推进到 now：完成所有已到点的启用，返回当前活跃密钥标识。
func (s *Scheduler) Advance(now int64) (activeID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logf("Advance 请求: 输入{now:%d}", now)
	if now < s.maxClock {
		s.logf("Advance 拒绝: 判定: 时钟回退 now=%d < maxClock=%d 输出: err=%v", now, s.maxClock, ErrClockMovedBackwards)
		return "", ErrClockMovedBackwards
	}
	s.advanceLocked(now)
	s.maxClock = now
	activeID = s.activeIDLocked()
	s.logf("Advance 成功: 输出: active=%q 验证集合=%v", activeID, s.verificationIDsLocked())
	return activeID, nil
}

// Sign 返回 now 时刻用于签发的活跃密钥标识。先惰性完成已到点的启用。
func (s *Scheduler) Sign(now int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logf("Sign 请求: 输入{now:%d}", now)
	if now < s.maxClock {
		s.logf("Sign 拒绝: 判定: 时钟回退 now=%d < maxClock=%d 输出: err=%v", now, s.maxClock, ErrClockMovedBackwards)
		return "", ErrClockMovedBackwards
	}
	s.advanceLocked(now)
	s.maxClock = now
	id := s.activeIDLocked()
	s.logf("Sign 成功: 判定: 取当前唯一活跃密钥 输出: active=%q", id)
	return id, nil
}

// ActiveID 返回当前活跃密钥标识（不推进时钟）。
func (s *Scheduler) ActiveID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeIDLocked()
}

// VerificationSet 返回当前验证集合（已发布、活跃、停签三态，按标识引入顺序）。
func (s *Scheduler) VerificationSet(now int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logf("VerificationSet 请求: 输入{now:%d}", now)
	if now < s.maxClock {
		s.logf("VerificationSet 拒绝: 判定: 时钟回退 now=%d < maxClock=%d 输出: err=%v", now, s.maxClock, ErrClockMovedBackwards)
		return nil, ErrClockMovedBackwards
	}
	s.advanceLocked(now)
	s.maxClock = now
	set := s.verificationIDsLocked()
	s.logf("VerificationSet 成功: 输出: %v", set)
	return set, nil
}

// Key 返回一把密钥的快照（含已退役）；不存在返回 ErrKeyNotFound。
func (s *Scheduler) Key(id string) (KeyInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, exists := s.keys[id]
	if !exists {
		s.logf("Key 查询: 输入{id:%q} 输出: err=%v", id, ErrKeyNotFound)
		return KeyInfo{}, ErrKeyNotFound
	}
	s.logf("Key 查询: 输入{id:%q} 输出: phase=%s published=%d activated=%d signOff=%d retires=%d retired=%d",
		id, key.Phase, key.Published, key.Activated, key.SignOffAt, key.RetiresAt, key.RetiredAt)
	return *key, nil
}

// advanceLocked 完成所有 Activated <= now 的待启用密钥：
// 新密钥转为活跃，旧活跃同时转为停签；停签时刻取启用时刻而非被观察到的时刻。
func (s *Scheduler) advanceLocked(now int64) {
	for _, id := range s.order {
		key := s.keys[id]
		if key.Phase == Published && key.Activated <= now {
			oldActive := s.keys[s.activeIDLocked()]
			key.Phase = Active
			oldActive.Phase = SignOff
			oldActive.SignOffAt = key.Activated
			oldActive.RetiresAt = key.Activated + s.tokenTTL + s.skew
			s.logf("惰性启用: now=%d 到点 判定: %q 于启用时刻 %d 成为唯一活跃，%q 同时停签（停签时刻记为 %d，最早退役 %d）",
				now, key.ID, key.Activated, oldActive.ID, oldActive.SignOffAt, oldActive.RetiresAt)
		}
	}
}

func (s *Scheduler) activeIDLocked() string {
	for _, id := range s.order {
		if s.keys[id].Phase == Active {
			return id
		}
	}
	return ""
}

// verificationIDsLocked 返回已发布、活跃、停签三态密钥；已退役密钥已从集合移除。
func (s *Scheduler) verificationIDsLocked() []string {
	set := make([]string, 0, len(s.order))
	for _, id := range s.order {
		switch s.keys[id].Phase {
		case Published, Active, SignOff:
			set = append(set, id)
		}
	}
	return set
}

// Snapshot 在同一把锁内返回当前验证集合的密钥快照（供原子校验；测试与监控使用）。
func (s *Scheduler) Snapshot(now int64) ([]KeyInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.maxClock {
		return nil, ErrClockMovedBackwards
	}
	s.advanceLocked(now)
	s.maxClock = now
	infos := make([]KeyInfo, 0, len(s.order))
	for _, id := range s.order {
		key := s.keys[id]
		if key.Phase == Published || key.Phase == Active || key.Phase == SignOff {
			infos = append(infos, *key)
		}
	}
	return infos, nil
}

func (s *Scheduler) logf(format string, args ...any) {
	s.logger.Printf(format, args...)
}
