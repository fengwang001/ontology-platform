package review

import (
	"sort"
	"time"
)

// publicityDuration 为自宣布时刻起整整七个自然日。
const publicityDuration = 7 * 24 * time.Hour

// CreateReview 为申报人抽取评委组并创建评审。
// 抽取失败（评委不足）不占用任何评委，也不产生评审。
func (s *Service) CreateReview(at time.Time, applicantID, n int, minGroups map[string]int) (int, []int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 参数非法优先于一切（含时钟）。
	if applicantID <= 0 {
		return 0, nil, errInvalid("申报人编号须为正整数: %d", applicantID)
	}
	if err := validateDrawParams(n, minGroups); err != nil {
		return 0, nil, err
	}
	if err := s.checkClock(at); err != nil {
		return 0, nil, err
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return 0, nil, errNotFound("申报人不存在: %d", applicantID)
	}

	panel, err := s.drawPanel(app, n, minGroups)
	if err != nil {
		return 0, nil, err // 评委不足：尚未占用任何人
	}
	s.advanceClock(at)

	id := s.nextReviewID
	s.nextReviewID++
	r := &reviewState{
		id:           id,
		applicant:    applicantID,
		n:            n,
		minGroups:    cloneMinGroups(minGroups),
		status:       StatusVoting,
		currentRound: 1,
		version:      1,
		panel:        map[int]int{},
	}
	for _, rid := range panel {
		r.panel[rid] = 1
		s.occupied[rid] = id
	}
	r.versions = append(r.versions, &PanelVersion{
		Version:   1,
		Reviewers: append([]int(nil), panel...),
		CreatedAt: at,
		Reason:    "抽取",
	})
	s.reviews[id] = r
	return id, append([]int(nil), panel...), nil
}

// Vote 由评委在指定轮次投票。每位评委一轮一票、不可更改；
// 非当前版本评委无权限；已退出评委的历史票不在此方法处理。
func (s *Service) Vote(at time.Time, reviewID, reviewerID int, round int, choice Choice) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 错误优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许 > 无权限 > 重复投票。
	if reviewID <= 0 || reviewerID <= 0 || round < 1 || round > 2 ||
		(choice != Approve && choice != Oppose && choice != Abstain) {
		return errInvalid("投票参数非法: 评审=%d 评委=%d 轮次=%d 选择=%d", reviewID, reviewerID, round, choice)
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return errNotFound("评审不存在: %d", reviewID)
	}
	if r.status != StatusVoting {
		return errState("评审 %d 当前状态 %s 不接受投票", reviewID, statusName(r.status))
	}
	if round != r.currentRound {
		return errState("评审 %d 当前为第 %d 轮投票，不能投第 %d 轮", reviewID, r.currentRound, round)
	}
	if round == 2 && !r.round2Open {
		return errState("评审 %d 尚未进入复议", reviewID)
	}
	ver, onPanel := r.panel[reviewerID]
	if !onPanel {
		return errPerm("评委 %d 不属于评审 %d 当前评委构成", reviewerID, reviewID)
	}
	// 注：不在本评审构成的评委（含库中不存在的编号）一律按无权限处理，
	// 因为对"非本评审评委"这一事实，无权限优先于库实体存在性细节。
	for _, v := range r.votes[round-1] {
		if v.reviewer == reviewerID {
			if round == 2 && v.episode != r.round2Episode {
				break // 属被推翻的旧复议幕，不视为重复投票
			}
			if v.voided {
				// 已退出者不可能仍在 panel 中；防御性分支。
				return errPerm("评委 %d 已回避退出评审 %d", reviewerID, reviewID)
			}
			return errDup("评委 %d 在评审 %d 第 %d 轮已投过票", reviewerID, reviewID, round)
		}
	}

	r.votes[round-1] = append(r.votes[round-1], voteEntry{
		reviewer:     reviewerID,
		choice:       choice,
		at:           at,
		panelVersion: ver,
		episode:      r.round2Episode,
	})
	s.advanceClock(at)

	// 票齐则自动结算当前轮（时间即最后一票时刻，确定性推进）。
	if len(s.validVotes(r, round)) == len(r.panel) {
		s.settle(r, at)
	}
	return nil
}

// validVotes 返回某轮在当前构成下有效（未作废且投票者仍在组）的票。
// 调用时持锁。
func (s *Service) validVotes(r *reviewState, round int) []voteEntry {
	out := make([]voteEntry, 0, len(r.votes[round-1]))
	for _, v := range r.votes[round-1] {
		if round == 2 && v.episode != r.round2Episode {
			continue
		}
		if v.voided {
			continue
		}
		if _, on := r.panel[v.reviewer]; !on {
			continue
		}
		out = append(out, v)
	}
	return out
}

// settle 对当前轮按当前版本评委构成结算，并推进评审状态。
// 调用时持锁，要求当前轮票已补齐。
func (s *Service) settle(r *reviewState, at time.Time) {
	round := r.currentRound
	valid := s.validVotes(r, round)
	total := len(r.panel)
	var approve, oppose, abstain int
	for _, v := range valid {
		switch v.choice {
		case Approve:
			approve++
		case Oppose:
			oppose++
		case Abstain:
			abstain++
		}
	}

	var outcome Outcome
	if round == 1 {
		outcome = settleRound1(total, approve)
	} else {
		// 复议：严格大于半数方为通过。
		if approve*2 > total {
			outcome = OutcomePass
		} else {
			outcome = OutcomeFail
		}
	}
	reason := decisionReason(round, total, approve, outcome)

	r.results = append(r.results, RoundResult{
		Round: round, Version: r.version, Total: total,
		Approve: approve, Oppose: oppose, Abstain: abstain,
		Outcome: outcome, DecidedAt: at, Reason: reason,
	})

	switch outcome {
	case OutcomeReconsider:
		r.currentRound = 2
		r.round2Open = true
		r.round2Episode++ // 每次进入复议开启新一幕
	case OutcomePass:
		s.announce(r, at, true)
	case OutcomeFail:
		s.announce(r, at, false)
	}
}

// settleRound1 是第一轮门槛的唯一权威定义，便于边界测试对照。
// 通过：赞成 >= ceil(2n/3)；不通过：赞成 <= floor(n/2)；否则复议。
func settleRound1(total, approve int) Outcome {
	if approve >= ceilDiv(2*total, 3) {
		return OutcomePass
	}
	if approve <= total/2 {
		return OutcomeFail
	}
	return OutcomeReconsider
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// decisionReason 输出与门槛公式一一对应的判定依据，供留痕与复算。
func decisionReason(round, total, approve int, o Outcome) string {
	switch round {
	case 1:
		passLine := ceilDiv(2*total, 3)
		failLine := total / 2
		switch o {
		case OutcomePass:
			return sprintf("第一轮：赞成 %d ≥ ceil(2×%d/3)=%d，通过", approve, total, passLine)
		case OutcomeFail:
			return sprintf("第一轮：赞成 %d ≤ floor(%d/2)=%d，不通过", approve, total, failLine)
		default:
			return sprintf("第一轮：floor(%d/2)=%d < 赞成 %d < ceil(2×%d/3)=%d，进入复议",
				total, failLine, approve, total, passLine)
		}
	default:
		if o == OutcomePass {
			return sprintf("复议：赞成 %d 严格大于 %d/2=%g，通过", approve, total, float64(total)/2)
		}
		return sprintf("复议：赞成 %d 不严格大于 %d/2=%g，不通过", approve, total, float64(total)/2)
	}
}

// announce 在票决结论产生时立即宣布并进入公示。
// pass 为结论；调用时持锁。
func (s *Service) announce(r *reviewState, at time.Time, pass bool) {
	r.announcedAt = at
	r.publicityEnd = at.Add(publicityDuration)
	r.status = StatusPublicity
	if pass {
		r.tentative = StatusFinalPass
	} else {
		r.tentative = StatusFinalFail
	}
}

func statusName(st ReviewStatus) string {
	switch st {
	case StatusVoting:
		return "投票中"
	case StatusPublicity:
		return "公示中"
	case StatusFinalPass:
		return "终局通过"
	case StatusFinalFail:
		return "终局不通过"
	case StatusVoid:
		return "作废"
	case StatusAborted:
		return "中止"
	default:
		return "未知"
	}
}

func cloneMinGroups(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortedPanel(p map[int]int) []int {
	ids := make([]int, 0, len(p))
	for id := range p {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
