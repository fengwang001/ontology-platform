package review

// GetReview 返回评审的只读快照（不存在报 ErrNotFound）。
func (s *Service) GetReview(reviewID int) (*ReviewView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reviewID <= 0 {
		return nil, errInvalid("评审编号须为正整数: %d", reviewID)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return nil, errNotFound("评审不存在: %d", reviewID)
	}
	return s.viewOf(r), nil
}

func (s *Service) viewOf(r *reviewState) *ReviewView {
	v := &ReviewView{
		ID:            r.id,
		ApplicantID:   r.applicant,
		Status:        r.status,
		N:             r.n,
		MinGroups:     cloneMinGroups(r.minGroups),
		CurrentRound:  r.currentRound,
		Version:       r.version,
		Panel:         sortedPanel(r.panel),
		History:       append([]RoundResult(nil), r.results...),
		AnnouncedAt:   r.announcedAt,
		PublicityEnd:  r.publicityEnd,
		Objection:     r.objection,
		ObjectionTime: r.objectionTime,
	}
	return v
}

// Versions 返回该评审全部历史评委构成版本（升序，不可变副本）。
func (s *Service) Versions(reviewID int) ([]PanelVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reviewID <= 0 {
		return nil, errInvalid("评审编号须为正整数: %d", reviewID)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return nil, errNotFound("评审不存在: %d", reviewID)
	}
	out := make([]PanelVersion, 0, len(r.versions))
	for _, pv := range r.versions {
		cp := PanelVersion{
			Version:   pv.Version,
			Reviewers: append([]int(nil), pv.Reviewers...),
			CreatedAt: pv.CreatedAt,
			Reason:    pv.Reason,
			Left:      append([]int(nil), pv.Left...),
		}
		out = append(out, cp)
	}
	return out, nil
}

// VoteRecords 返回某评委在某评审的全部投票留痕（含已作废票与旧复议幕票）。
// reviewerID <= 0 表示返回该评审全部评委的记录。
func (s *Service) VoteRecords(reviewID, reviewerID int) ([]VoteRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reviewID <= 0 {
		return nil, errInvalid("评审编号须为正整数: %d", reviewID)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return nil, errNotFound("评审不存在: %d", reviewID)
	}
	var out []VoteRecord
	for roundIdx := range r.votes {
		for _, v := range r.votes[roundIdx] {
			if reviewerID > 0 && v.reviewer != reviewerID {
				continue
			}
			out = append(out, VoteRecord{
				ReviewID:     r.id,
				ReviewerID:   v.reviewer,
				Round:        roundIdx + 1,
				Choice:       v.choice,
				VotedAt:      v.at,
				PanelVersion: v.panelVersion,
				Voided:       v.voided,
			})
		}
	}
	return out, nil
}

// EffectiveVotesAtVersion 按指定评委构成版本还原该轮当时的有效票集合。
// 规则：投票者属于该版本评委名单即有效。票的作废发生在评委退出之后，
// 因此退出者不在旧版本名单内；在其仍在组的历史版本中，其票按当时
// 事实有效，可精确还原。老评委的票跨版本延续至其后继版本。
// 复议轮额外要求票属于当前可见的最后一幕（如需查看旧幕，请用
// VoteRecords 查询全部留痕）。
func (s *Service) EffectiveVotesAtVersion(reviewID, version, round int) ([]VoteRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reviewID <= 0 || version <= 0 || round < 1 || round > 2 {
		return nil, errInvalid("参数非法: 评审=%d 版本=%d 轮次=%d", reviewID, version, round)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return nil, errNotFound("评审不存在: %d", reviewID)
	}
	if version > r.version || version > len(r.versions) {
		return nil, errInvalid("评审 %d 不存在版本 %d", reviewID, version)
	}
	onPanel := map[int]struct{}{}
	for _, id := range r.versions[version-1].Reviewers {
		onPanel[id] = struct{}{}
	}
	var out []VoteRecord
	for _, v := range r.votes[round-1] {
		if _, ok := onPanel[v.reviewer]; !ok {
			continue
		}
		out = append(out, VoteRecord{
			ReviewID:     r.id,
			ReviewerID:   v.reviewer,
			Round:        round,
			Choice:       v.choice,
			VotedAt:      v.at,
			PanelVersion: v.panelVersion,
		})
	}
	return out, nil
}
