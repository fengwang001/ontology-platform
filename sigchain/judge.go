package sigchain

// classifyLocked 对单个提交做签名判定，返回七类结果之一
// （外加未签名/来自未来/时刻倒置）。调用方须持有读锁或写锁。
//
// 优先级：来自未来 > 时刻倒置 > 吊销追溯降级 > 校验器回答
// （伪造/密钥未知）> 签署时刻不在受信区间的四种细分。
func (s *Service) classifyLocked(c Commit, now Time) Reason {
	sig := c.Sig
	if sig == nil {
		return ReasonUnsigned
	}
	if sig.Time > now {
		return ReasonFromFuture
	}
	if sig.Time < c.AuthorTime {
		return ReasonTimeInversion
	}
	k, known := s.reg.keys[sig.KeyID]
	if known && s.policy.RevocationRetroactive &&
		k.RevokedAt != nil && *k.RevokedAt < now {
		return ReasonRevoked // 吊销追溯：不追问签署时刻是否仍受信
	}
	switch s.verifier.Verify(c.ID, *sig) {
	case CryptoInvalid:
		return ReasonForged
	case CryptoKeyUnknown:
		return ReasonUnknownKey
	}
	if !known {
		return ReasonUnknownKey // 校验器认可但密钥未登记，同样视为未知
	}
	if sig.Time < k.ValidFrom {
		return ReasonNotYetValid
	}
	if k.ValidUntil != nil && sig.Time >= *k.ValidUntil {
		return ReasonExpired
	}
	if k.RevokedAt != nil && sig.Time >= *k.RevokedAt {
		return ReasonRevoked
	}
	if !s.reg.chainTrusted(sig.KeyID) {
		return ReasonBrokenEndorsement
	}
	return ReasonTrusted
}

// VerifyBranch 对分支做链路裁决：从锚点标签所指提交沿第一父方向
// 走到 tip 指定的顶端，途中每个提交都必须签名可信。
//
// 错误次序：参数非法 → 提交不存在 → 标签不存在（锚点未设置）。
// 链路裁决本身不是错误，不可信以 Verdict 形式返回。
func (s *Service) VerifyBranch(tip string, now Time) (Verdict, error) {
	if tip == "" || now < 0 {
		return Verdict{}, ErrInvalidParam
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.commits[tip]; !ok {
		return Verdict{}, ErrCommitNotFound
	}
	if !s.hasAnchor {
		return Verdict{}, ErrTagNotFound
	}
	anchorCommit := s.tags[s.anchorTag].CommitID
	// 自顶端沿第一父下行收集路径，直到锚点提交；提交只引用更早登记的
	// 提交，第一父链必然终止。
	var path []string
	cur := tip
	for {
		path = append(path, cur)
		if cur == anchorCommit {
			break
		}
		c := s.commits[cur]
		if len(c.Parents) == 0 {
			return Verdict{Reason: ReasonAnchorNotOnChain}, nil
		}
		cur = c.Parents[0]
	}
	// 自锚点向顶端逐个判定，报告首个不可信提交。
	checked := 0
	for i := len(path) - 1; i >= 0; i-- {
		c := s.commits[path[i]]
		if s.policy.MergeExempt && len(c.Parents) >= 2 {
			continue // 合并提交豁免：自身不需签名，第二父链本就不在第一父路径上
		}
		checked++
		if r := s.classifyLocked(c, now); r != ReasonTrusted {
			return Verdict{CommitID: c.ID, Reason: r, Checked: checked}, nil
		}
	}
	return Verdict{Trusted: true, Reason: ReasonTrusted, Checked: checked}, nil
}
