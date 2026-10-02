// Package keyring 实现签名密钥集合的四步轮换调度器：
// 发布（仅验证）→ 启用（唯一活跃，可签发）→ 停签（仅验证）→ 退役（移出验证集合）。
//
// 参数：C 为验证方缓存最长保留时间，T 为令牌寿命，S 为验证方接受的时钟偏差。
// 在时刻 t 请求轮换：新密钥立即发布，启用时刻恒为 t+C；届时新密钥成为唯一活跃者，
// 旧活跃者同时转为停签，停签时刻记为启用时刻。停签密钥的最早退役时刻为
// 停签时刻+T+S，恰到点即可退役，退役即从验证集合移除且标识永不复用。
//
// 状态随注入时钟惰性推进：每个操作先完成所有已到点的启用，再执行自身逻辑。
// 所有操作可并发调用，相同的操作与时钟序列得到相同状态。
package keyring

import (
	"errors"
	"sort"
	"sync"
)

// State 表示密钥所处的轮换阶段。
type State int

const (
	// StatePublished 已发布：仅可验证，等待启用。
	StatePublished State = iota
	// StateActive 活跃：唯一可签发密钥，同时在验证集合中。
	StateActive
	// StateDeactivated 停签：不再签发，仅验证，等待退役。
	StateDeactivated
)

func (s State) String() string {
	switch s {
	case StatePublished:
		return "published"
	case StateActive:
		return "active"
	case StateDeactivated:
		return "deactivated"
	default:
		return "unknown"
	}
}

// 可区分的拒绝原因。各操作按文档列出的顺序只报告第一个命中的原因。
var (
	ErrNonPositiveCacheTTL = errors.New("keyring: 缓存保留时间 C 必须为正")
	ErrNonPositiveTokenTTL = errors.New("keyring: 令牌寿命 T 必须为正")
	ErrNegativeSkew        = errors.New("keyring: 时钟偏差 S 不得为负")
	ErrClockRegression     = errors.New("keyring: 时钟读数早于已见最大读数")
	ErrDuplicateKeyID      = errors.New("keyring: 密钥标识已存在（含已退役，标识不得复用）")
	ErrRotationPending     = errors.New("keyring: 已有待启用的新密钥")
	ErrKeyNotFound         = errors.New("keyring: 密钥不存在")
	ErrKeyNotDeactivated   = errors.New("keyring: 密钥不在停签状态")
)

// RetireTooEarlyError 表示退役请求早于最早退役时刻，并携带该时刻。
type RetireTooEarlyError struct {
	ID       string
	Now      int64
	Earliest int64
}

func (e *RetireTooEarlyError) Error() string {
	return "keyring: 未到最早退役时刻"
}

// KeyInfo 是密钥状态与各最早时刻的快照。
type KeyInfo struct {
	ID               string
	State            State
	PublishedAt      int64
	ActivateAt       int64
	DeactivatedAt    int64
	EarliestRetireAt int64
}

type key struct {
	id            string
	state         State
	publishedAt   int64
	activateAt    int64
	deactivatedAt int64
}

// Scheduler 是并发安全的密钥轮换调度器。
type Scheduler struct {
	mu        sync.Mutex
	cacheTTL  int64
	tokenTTL  int64
	skew      int64
	maxSeen   int64
	keys      map[string]*key
	used      map[string]bool
	activeID  string
	pendingID string
}

// New 创建调度器。C、T 必须为正，S 不得为负；初始密钥直接为活跃。
func New(cacheTTL, tokenTTL, skew int64, initialID string, now int64) (*Scheduler, error) {
	if cacheTTL <= 0 {
		return nil, ErrNonPositiveCacheTTL
	}
	if tokenTTL <= 0 {
		return nil, ErrNonPositiveTokenTTL
	}
	if skew < 0 {
		return nil, ErrNegativeSkew
	}
	k := &key{id: initialID, state: StateActive, publishedAt: now, activateAt: now}
	return &Scheduler{
		cacheTTL: cacheTTL,
		tokenTTL: tokenTTL,
		skew:     skew,
		maxSeen:  now,
		keys:     map[string]*key{initialID: k},
		used:     map[string]bool{initialID: true},
		activeID: initialID,
	}, nil
}

// Rotate 在时刻 now 请求轮换：新密钥立即发布，返回启用时刻 now+C。
// 拒绝顺序：时钟回拨 → 标识已存在（含已退役）→ 已有待启用的新密钥。
func (s *Scheduler) Rotate(id string, now int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	undo := s.advanceLocked(now)
	if s.used[id] {
		undo()
		return 0, ErrDuplicateKeyID
	}
	if s.pendingID != "" {
		undo()
		return 0, ErrRotationPending
	}
	s.maxSeen = now
	k := &key{id: id, state: StatePublished, publishedAt: now, activateAt: now + s.cacheTTL}
	s.keys[id] = k
	s.used[id] = true
	s.pendingID = id
	return k.activateAt, nil
}

// Retire 在时刻 now 退役一把停签密钥，将其移出验证集合。
// 拒绝顺序：时钟回拨 → 密钥不存在 → 不在停签状态 → 早于最早退役时刻。
func (s *Scheduler) Retire(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	undo := s.advanceLocked(now)
	k, ok := s.keys[id]
	if !ok {
		undo()
		return ErrKeyNotFound
	}
	if k.state != StateDeactivated {
		undo()
		return ErrKeyNotDeactivated
	}
	earliest := k.deactivatedAt + s.tokenTTL + s.skew
	if now < earliest {
		undo()
		return &RetireTooEarlyError{ID: id, Now: now, Earliest: earliest}
	}
	s.maxSeen = now
	delete(s.keys, id)
	return nil
}

// Sign 返回时刻 now 用于签发令牌的当前活跃密钥标识。
func (s *Scheduler) Sign(now int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	s.advanceLocked(now)
	s.maxSeen = now
	return s.activeID, nil
}

// VerificationSet 返回时刻 now 的验证集合（已发布、活跃、停签三态）。
func (s *Scheduler) VerificationSet(now int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.advanceLocked(now)
	s.maxSeen = now
	ids := make([]string, 0, len(s.keys))
	for id := range s.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Snapshot 返回时刻 now 全部未退役密钥的状态快照，按标识排序。
func (s *Scheduler) Snapshot(now int64) ([]KeyInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.advanceLocked(now)
	s.maxSeen = now
	infos := make([]KeyInfo, 0, len(s.keys))
	for _, k := range s.keys {
		info := KeyInfo{
			ID:            k.id,
			State:         k.state,
			PublishedAt:   k.publishedAt,
			ActivateAt:    k.activateAt,
			DeactivatedAt: k.deactivatedAt,
		}
		if k.state == StateDeactivated {
			info.EarliestRetireAt = k.deactivatedAt + s.tokenTTL + s.skew
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, nil
}

func (s *Scheduler) checkClock(now int64) error {
	if now < s.maxSeen {
		return ErrClockRegression
	}
	return nil
}

// advanceLocked 应用所有到点（启用时刻 <= now）的启用，并返回回滚函数，
// 供随后被拒绝的操作恢复原状。至多一把待启用密钥，故至多推进一次。
func (s *Scheduler) advanceLocked(now int64) (undo func()) {
	if s.pendingID == "" {
		return func() {}
	}
	pending := s.keys[s.pendingID]
	if pending.activateAt > now {
		return func() {}
	}
	old := s.keys[s.activeID]
	savedOld, savedPending := *old, *pending
	savedActiveID, savedPendingID := s.activeID, s.pendingID
	old.state = StateDeactivated
	old.deactivatedAt = pending.activateAt
	pending.state = StateActive
	s.activeID = pending.id
	s.pendingID = ""
	return func() {
		*old = savedOld
		*pending = savedPending
		s.activeID = savedActiveID
		s.pendingID = savedPendingID
	}
}
