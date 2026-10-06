package sigchain

import "sync"

// Service 为提交签名验证链服务。所有方法可任意并发调用，
// 结果等价于某个串行顺序；一次裁决基于某一瞬间的完整登记状态。
type Service struct {
	mu         sync.RWMutex
	commits    map[string]Commit
	tags       map[string]Tag
	reg        *registry
	anchorTag  string
	anchorRoot string
	hasAnchor  bool
	policy     Policy
	verifier   Verifier
}

// NewService 创建服务，verifier 为注入的签名校验器，不得为 nil。
func NewService(verifier Verifier) *Service {
	if verifier == nil {
		panic("sigchain: verifier 不得为 nil")
	}
	return &Service{
		commits:  make(map[string]Commit),
		tags:     make(map[string]Tag),
		reg:      newRegistry(),
		verifier: verifier,
	}
}

func validSigShape(s *Signature) bool {
	return s == nil || (s.KeyID != "" && s.Time >= 0)
}

// AddCommit 登记一个提交。父提交必须已登记，保证第一父链可完整遍历。
func (s *Service) AddCommit(c Commit) error {
	if c.ID == "" || c.AuthorTime < 0 || !validSigShape(c.Sig) {
		return ErrInvalidParam
	}
	for _, p := range c.Parents {
		if p == "" {
			return ErrInvalidParam
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.commits[c.ID]; dup {
		return ErrInvalidParam
	}
	for _, p := range c.Parents {
		if _, ok := s.commits[p]; !ok {
			return ErrCommitNotFound
		}
	}
	s.commits[c.ID] = c
	return nil
}

// AddTag 登记一个标签，指向的提交必须已登记。
func (s *Service) AddTag(t Tag) error {
	if t.Name == "" || t.CommitID == "" || !validSigShape(t.Sig) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.tags[t.Name]; dup {
		return ErrInvalidParam
	}
	if _, ok := s.commits[t.CommitID]; !ok {
		return ErrCommitNotFound
	}
	s.tags[t.Name] = t
	return nil
}

// RegisterKeys 原子地批量登记密钥：任一校验失败则整体拒绝，
// 不改变任何已有登记。重复登记同一密钥仅允许补设吊销时刻，
// 其余登记信息必须与原登记一致。
//
// 错误次序：参数非法 → 密钥不存在（背书者）→ 背书成环 → 吊销提前。
func (s *Service) RegisterKeys(keys ...Key) error {
	if len(keys) == 0 {
		return ErrInvalidParam
	}
	batch := make(map[string]Key, len(keys))
	for _, k := range keys {
		if err := validateKeyParams(k); err != nil {
			return err
		}
		if _, dup := batch[k.ID]; dup {
			return ErrInvalidParam
		}
		batch[k.ID] = k
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 重复登记一致性（参数层面）：除补设吊销外不得改变已登记信息。
	for _, k := range keys {
		old, ok := s.reg.keys[k.ID]
		if !ok {
			continue
		}
		if !sameKeyShape(old, k) {
			return ErrInvalidParam
		}
		if old.RevokedAt != nil {
			if k.RevokedAt == nil || *k.RevokedAt > *old.RevokedAt {
				return ErrInvalidParam // 吊销不可撤销也不可推迟
			}
		}
	}
	// 背书者必须已登记或在本批次内。
	for _, k := range keys {
		if k.Endorsement == nil {
			continue
		}
		en := k.Endorsement.Endorser
		if _, ok := s.reg.keys[en]; !ok {
			if _, inBatch := batch[en]; !inBatch {
				return ErrKeyNotFound
			}
		}
	}
	// 背书不得成环，成环则整体拒绝。
	for _, k := range keys {
		if hasCycleFrom(k.ID, batch, s.reg.keys) {
			return ErrEndorsementCycle
		}
	}
	// 吊销时刻不得提前：不得早于生效时刻，也不得早于已设吊销时刻。
	for _, k := range keys {
		if k.RevokedAt == nil {
			continue
		}
		if *k.RevokedAt < k.ValidFrom {
			return ErrRevokeEarlier
		}
		if old, ok := s.reg.keys[k.ID]; ok && old.RevokedAt != nil &&
			*k.RevokedAt < *old.RevokedAt {
			return ErrRevokeEarlier
		}
	}
	for _, k := range keys {
		s.reg.keys[k.ID] = k
	}
	s.reg.resetMemo()
	return nil
}

// RevokeKey 吊销一把密钥。吊销时刻一旦设置不可撤销也不可提前。
func (s *Service) RevokeKey(id string, t Time) error {
	if id == "" || t < 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.reg.keys[id]
	if !ok {
		return ErrKeyNotFound
	}
	if old.RevokedAt != nil {
		switch {
		case t == *old.RevokedAt:
			return nil // 幂等
		case t < *old.RevokedAt:
			return ErrRevokeEarlier
		default:
			return ErrInvalidParam // 推迟已设吊销等同于撤销，拒绝
		}
	}
	if t < old.ValidFrom {
		return ErrRevokeEarlier
	}
	old.RevokedAt = &t
	s.reg.keys[id] = old
	s.reg.resetMemo()
	return nil
}

// SetAnchor 设置可信锚点：一个带签名的标签，其签署密钥为根密钥。
// 管理员同时声明期望的根密钥标识，服务校验其与标签签名一致且已登记。
func (s *Service) SetAnchor(tagName, rootKeyID string) error {
	if tagName == "" || rootKeyID == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tag, ok := s.tags[tagName]
	if !ok {
		return ErrTagNotFound
	}
	if tag.Sig == nil || tag.Sig.KeyID != rootKeyID {
		return ErrInvalidParam // 锚点标签必须带签名且签署者即声明的根密钥
	}
	if _, ok := s.reg.keys[rootKeyID]; !ok {
		return ErrKeyNotFound
	}
	s.anchorTag = tagName
	s.anchorRoot = rootKeyID
	s.hasAnchor = true
	s.reg.rootKeyID = rootKeyID
	s.reg.hasRoot = true
	s.reg.resetMemo()
	return nil
}

// SetPolicy 修改链路策略。
func (s *Service) SetPolicy(p Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = p
}
