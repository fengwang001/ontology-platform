package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Policy 控制链路裁决的可选策略。
type Policy struct {
	RevocationRetroactive bool
	MergeExempt           bool
}

// CommitVerdict 是单个提交的签名裁决。
type CommitVerdict struct {
	CommitID string
	Trusted  bool
	Reason   Reason
}

// BranchVerdict 是分支第一父链的裁决结果（裁决本身不是错误）。
type BranchVerdict struct {
	Trusted bool
	First   *CommitVerdict
	Reason  Reason
	// CommitsTouched 是本次第一父遍历触碰的提交数，用于性能界的可验证测试。
	CommitsTouched int
}

// Service 是提交签名验证链服务。
type Service struct {
	mu        sync.RWMutex
	reg       registry
	st        store
	validator SignatureValidator
	anchor    string
	policy    Policy
}

func NewService(v SignatureValidator) *Service {
	return &Service{reg: *newRegistry(), st: *newStore(), validator: v}
}

func nonnegTS(ts int64) bool { return ts >= 0 }

// validateCommit 执行参数合法性检查（错误次序第一位）。
func validateCommit(c Commit) error {
	if c.ID == "" || !nonnegTS(c.AuthorTime) {
		return ErrInvalidArgument
	}
	if c.Sig != nil {
		if c.Sig.KeyID == "" || !nonnegTS(c.Sig.SignedAt) {
			return ErrInvalidArgument
		}
	}
	for _, p := range c.Parents {
		if p == "" {
			return ErrInvalidArgument
		}
	}
	return nil
}

// AddCommit 登记提交。错误次序：参数非法 → 提交不存在（父）。
// 注：父缺失在本题允许独立登记，故这里不强制；裁决沿链时报不存在。
func (s *Service) AddCommit(c Commit) error {
	if err := validateCommit(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.addCommit(c)
}

func validateTag(t Tag) error {
	if t.ID == "" || t.Commit == "" {
		return ErrInvalidArgument
	}
	if t.Sig != nil && (t.Sig.KeyID == "" || !nonnegTS(t.Sig.SignedAt)) {
		return ErrInvalidArgument
	}
	return nil
}

// AddTag 登记标签。错误次序：参数非法 → 提交不存在。
func (s *Service) AddTag(t Tag) error {
	if err := validateTag(t); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.getCommit(t.Commit); !ok {
		return ErrCommitNotFound
	}
	return s.st.addTag(t)
}

func validateKey(id string, validFrom int64, validUntil *int64, end *Endorsement) error {
	if id == "" || !nonnegTS(validFrom) {
		return ErrInvalidArgument
	}
	if validUntil != nil && (*validUntil <= validFrom || *validUntil < 0) {
		return ErrInvalidArgument
	}
	if end != nil && (end.EndorserID == "" || !nonnegTS(end.SignedAt)) {
		return ErrInvalidArgument
	}
	return nil
}

// RegisterKey 登记密钥（可带背书）。
// 错误次序：参数非法 → 密钥不存在（背书者）→ 背书成环。
// 校验全部先于状态变更，被拒绝的调用不改动任何登记。
func (s *Service) RegisterKey(id string, validFrom int64, validUntil *int64, end *Endorsement) error {
	if err := validateKey(id, validFrom, validUntil, end); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reg.register(id, validFrom, validUntil, end); err != nil {
		return err
	}
	s.reg.rebuild(s.anchorRootLocked())
	return nil
}

// AddEndorsement 追加背书。错误次序：参数非法 → 密钥不存在 → 成环。
func (s *Service) AddEndorsement(id string, end Endorsement) error {
	if id == "" || end.EndorserID == "" || !nonnegTS(end.SignedAt) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reg.addEndorsement(id, end); err != nil {
		return err
	}
	s.reg.rebuild(s.anchorRootLocked())
	return nil
}

// RevokeKey 吊销密钥。错误次序：参数非法 → 密钥不存在 → 吊销提前。
func (s *Service) RevokeKey(id string, at int64) error {
	if id == "" || !nonnegTS(at) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reg.revoke(id, at); err != nil {
		return err
	}
	s.reg.rebuild(s.anchorRootLocked())
	return nil
}

// anchorRootLocked 返回当前锚点标签签名所用密钥标识（调用方持锁）。
func (s *Service) anchorRootLocked() string {
	if s.anchor == "" {
		return ""
	}
	if t, ok := s.st.getTag(s.anchor); ok && t.Sig != nil {
		return t.Sig.KeyID
	}
	return ""
}

// SetAnchor 指定带签名的可信锚点标签。
// 错误次序：参数非法 → 标签不存在 → 密钥不存在；
// 同时要求标签确有签名且签名有效、根密钥在签署时刻受信（自身有效）。
func (s *Service) SetAnchor(tagID string) error {
	if tagID == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.st.getTag(tagID)
	if !ok {
		return ErrTagNotFound
	}
	if t.Sig == nil {
		return ErrInvalidArgument
	}
	k, ok := s.reg.get(t.Sig.KeyID)
	if !ok {
		return ErrKeyNotFound
	}
	if s.validator.Verify(t.ID, *t.Sig) != SigValid ||
		!s.reg.trustedAt(k, t.Sig.SignedAt, t.Sig.KeyID) {
		return ErrInvalidArgument
	}
	s.anchor = tagID
	s.reg.rebuild(t.Sig.KeyID)
	return nil
}

// SetPolicy 修改链路策略（两开关的一次原子替换）。
func (s *Service) SetPolicy(p Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = p
}

// judgeSignature 对提交签名给出七类互斥结果之一，Trusted 为可信。
// 全部判定基于同一把读锁下的完整快照。
func (s *Service) judgeSignature(c *Commit, verifyAt int64) (bool, Reason) {
	if c.Sig == nil {
		return false, ReasonUnsigned
	}
	sig := *c.Sig
	// 两条时间轴：验证时刻优先于提交时刻的倒置检查。
	if sig.SignedAt > verifyAt {
		return false, ReasonFuture
	}
	if sig.SignedAt < c.AuthorTime {
		return false, ReasonInvertedTime
	}
	switch s.validator.Verify(c.ID, sig) {
	case SigInvalid:
		return false, ReasonForged
	case SigUnknownKey:
		return false, ReasonUnknownKey
	}
	k, ok := s.reg.get(sig.KeyID)
	if !ok {
		return false, ReasonUnknownKey
	}
	// 吊销追溯：开启后，凡在验证时刻之前（严格早于）被吊销的密钥，
	// 其在吊销前签署的提交也一并降级，且吊销不影响“来自未来”等更高优先项。
	if s.policy.RevocationRetroactive && k.revokedAt != nil && *k.revokedAt < verifyAt {
		return false, ReasonRevoked
	}
	if reason := k.statusAt(sig.SignedAt); reason != "" {
		return false, reason
	}
	root := s.anchorRootLocked()
	if !s.reg.trustedAt(k, sig.SignedAt, root) {
		return false, ReasonBrokenEndorsement
	}
	return true, ""
}

// VerifyCommit 对单个提交在验证时刻 verifyAt 做签名裁决。
// 错误次序：参数非法 → 提交不存在。裁决结果本身不是错误。
func (s *Service) VerifyCommit(commitID string, verifyAt int64) (CommitVerdict, error) {
	if commitID == "" || !nonnegTS(verifyAt) {
		return CommitVerdict{}, ErrInvalidArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.st.getCommit(commitID)
	if !ok {
		return CommitVerdict{}, ErrCommitNotFound
	}
	trusted, reason := s.judgeSignature(c, verifyAt)
	return CommitVerdict{CommitID: commitID, Trusted: trusted, Reason: reason}, nil
}

// VerifyBranch 从锚点标签所指提交沿第一父方向走到顶端。
// 错误次序：参数非法 → 提交不存在（顶端）→ 标签不存在（无锚点时）。
// 性能：仅跟随第一父指针，访问提交数 = 链长，与第二父引入的提交无关。
func (s *Service) VerifyBranch(tipID string, verifyAt int64) (BranchVerdict, error) {
	if tipID == "" || !nonnegTS(verifyAt) {
		return BranchVerdict{}, ErrInvalidArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tip, ok := s.st.getCommit(tipID)
	if !ok {
		return BranchVerdict{}, ErrCommitNotFound
	}
	if s.anchor == "" {
		return BranchVerdict{}, ErrTagNotFound
	}
	tag, ok := s.st.getTag(s.anchor)
	if !ok {
		return BranchVerdict{}, ErrTagNotFound
	}

	// 收集第一父链（顶端 → 锚点方向），途中任何对象缺失都是不可信链。
	chain := []*Commit{}
	cur := tip
	touched := 0
	for {
		chain = append(chain, cur)
		touched++
		if cur.ID == tag.Commit {
			break
		}
		if len(cur.Parents) == 0 {
			return BranchVerdict{
				Trusted: false,
				Reason:  ReasonAnchorNotOnChain,
			}, nil
		}
		parent, ok := s.st.getCommit(cur.Parents[0])
		if !ok {
			return BranchVerdict{}, ErrCommitNotFound
		}
		cur = parent
	}

	// 沿锚点 → 顶端逐个核验，报告首个不可信提交。
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		if s.policy.MergeExempt && len(c.Parents) > 1 {
			continue // 合并提交自身豁免；第二及以后父所引入提交不沿入
		}
		if trusted, reason := s.judgeSignature(c, verifyAt); !trusted {
			return BranchVerdict{
				Trusted: false,
				First:   &CommitVerdict{CommitID: c.ID, Trusted: false, Reason: reason},
				Reason:  reason,
			}, nil
		}
	}
	return BranchVerdict{Trusted: true, CommitsTouched: touched}, nil
}

// dumpState 返回登记状态的规范字符串，供测试验证拒绝调用不产生副作用。
func (s *Service) dumpState() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.reg.keys))
	for id := range s.reg.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := ""
	for _, id := range ids {
		k := s.reg.keys[id]
		until, rev, end := "-", "-", "-"
		if k.validUntil != nil {
			until = fmt.Sprintf("%d", *k.validUntil)
		}
		if k.revokedAt != nil {
			rev = fmt.Sprintf("%d", *k.revokedAt)
		}
		if k.endorsedBy != nil {
			end = fmt.Sprintf("%s@%d", k.endorsedBy.EndorserID, k.endorsedBy.SignedAt)
		}
		out += fmt.Sprintf("%s{%d,%s,%s,%s,ch=%v};", id, k.validFrom, until, rev, end, k.chainOk)
	}
	return out + "anchor=" + s.anchor
}
