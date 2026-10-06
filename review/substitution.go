package review

import "time"

// HandleRecusal 表示评审进行中发现某评委应回避（新受理申请或新登记关系）。
// 该评委立即退出、其在本评审已投全部票作废；按唯一性规则选替补并形成
// 新的评委构成版本。无法替补时评审中止。
//
// 前提：评委此刻确实命中至少一条回避来源（同单位/登记关系/回避申请/
// 作废前科），且评审处于投票阶段；公示阶段的争议走异议程序。
func (s *Service) HandleRecusal(at time.Time, reviewID, reviewerID int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许 > 无权限 > 评委不足。
	if reviewID <= 0 || reviewerID <= 0 {
		return 0, errInvalid("编号须为正整数: 评审=%d 评委=%d", reviewID, reviewerID)
	}
	if err := s.checkClock(at); err != nil {
		return 0, err
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return 0, errNotFound("评审不存在: %d", reviewID)
	}
	rev, isReviewer := s.reviewers[reviewerID]
	if !isReviewer {
		return 0, errNotFound("评委不存在: %d", reviewerID)
	}
	if r.status != StatusVoting {
		return 0, errState("评审 %d 已不在投票阶段（%s），不能中途回避", reviewID, statusName(r.status))
	}
	ver, onPanel := r.panel[reviewerID]
	if !onPanel {
		return 0, errState("评委 %d 不在评审 %d 的当前构成中", reviewerID, reviewID)
	}
	app := s.applicants[r.applicant]
	if len(s.recusalSources(app, rev)) == 0 {
		return 0, errState("评委 %d 对申报人 %d 无任何回避事由", reviewerID, r.applicant)
	}

	leftGroup := rev.Group

	// 1) 退出：其全部已投票作废（记录保留，仅置作废标记）。
	s.advanceClock(at)
	delete(r.panel, reviewerID)
	for i := range r.votes {
		for j := range r.votes[i] {
			if r.votes[i][j].reviewer == reviewerID {
				r.votes[i][j].voided = true
			}
		}
	}

	// 2) 若第一轮已有结算（投票阶段只可能是"进入复议"），该结算被推翻，
	// 回到第一轮等待替补补投；旧复议轮作废（以"幕次"隔离其选票）。
	round1Done := false
	for k := len(r.results) - 1; k >= 0; k-- {
		if r.results[k].Round == 1 {
			r.results[k].Superseded = true
			round1Done = true
			break
		}
	}

	// 3) 选替补（唯一性规则，编号最小可行者）。
	sub, found := s.pickSubstitute(app, r, leftGroup)
	if !found {
		// 中止：释放全部占用；历史版本与留痕保留可查。
		for id := range r.panel {
			delete(s.occupied, id)
		}
		delete(s.occupied, reviewerID)
		r.status = StatusAborted
		return 0, errShort("评委 %d 回避后无可行替补，评审 %d 中止", reviewerID, reviewID)
	}

	// 4) 替补加入，形成不可变新版本。
	newVersion := r.version + 1
	r.version = newVersion
	r.panel[sub] = newVersion
	s.occupied[sub] = reviewID
	delete(s.occupied, reviewerID)
	r.versions = append(r.versions, &PanelVersion{
		Version:   newVersion,
		Reviewers: sortedPanel(r.panel),
		CreatedAt: at,
		Reason:    "回避替补",
		Left:      []int{reviewerID},
	})

	if round1Done {
		// 旧复议轮整体隔离：回到第一轮，替补补投后重新结算；
		// 重新结算若仍为复议，将开启新一幕复议，所有评委重新投票。
		r.currentRound = 1
		r.round2Open = false
		r.round2Episode++
	}

	_ = ver
	return sub, nil
}
