package review

import (
	"fmt"
	"time"
)

// effectiveVotes 返回当前评委构成下某一轮的有效票（投票者为现任评委且票未作废）。
func (s *Service) effectiveVotes(r *Review, round int) []*VoteRecord {
	cur := map[int64]bool{}
	for _, m := range r.members() {
		cur[m] = true
	}
	out := []*VoteRecord{}
	for _, v := range r.Votes {
		if v.Round == round && v.VoidedAt < 0 && cur[v.ExpertID] {
			out = append(out, v)
		}
	}
	return out
}

func countApprove(votes []*VoteRecord) int {
	n := 0
	for _, v := range votes {
		if v.Choice == ChoiceApprove {
			n++
		}
	}
	return n
}

// settleRound1 第一轮：赞成 >= ceil(2n/3) 通过；赞成 <= floor(n/2) 不通过；
// 介于两者之间进入复议。
func settleRound1(approve, n int) Outcome {
	if approve >= (2*n+2)/3 {
		return OutcomePass
	}
	if approve <= n/2 {
		return OutcomeFail
	}
	return OutcomeRevote
}

// settleRound2 复议：赞成严格大于总人数的一半方为通过，否则不通过。
func settleRound2(approve, n int) Outcome {
	if approve > n/2 {
		return OutcomePass
	}
	return OutcomeFail
}

type settlement struct {
	round   int
	outcome Outcome
	final   bool
}

// chain 从当前有效票集合重新推导应成立的结算链。
func (s *Service) chain(r *Review) []settlement {
	eff1 := s.effectiveVotes(r, 1)
	if len(eff1) < r.N {
		return nil
	}
	o1 := settleRound1(countApprove(eff1), r.N)
	ch := []settlement{{round: 1, outcome: o1, final: o1 != OutcomeRevote}}
	if o1 != OutcomeRevote {
		return ch
	}
	if r.rounds < 2 {
		return ch
	}
	eff2 := s.effectiveVotes(r, 2)
	if len(eff2) < r.N {
		return ch
	}
	o2 := settleRound2(countApprove(eff2), r.N)
	return append(ch, settlement{round: 2, outcome: o2, final: true})
}

// settleAndTrail 在投票或替补后重算结算链并留痕：
// 与现存未取代前缀不一致的旧条目标记 Superseded，新结论追加记录。
func (s *Service) settleAndTrail(r *Review, at time.Time) {
	if r.Aborted || r.Voided {
		return
	}
	ch := s.chain(r)
	if len(ch) > 0 && ch[0].outcome == OutcomeRevote && r.rounds < 2 {
		r.rounds = 2
	}
	active := []*TrailEntry{}
	for _, e := range r.Trail {
		if !e.Superseded {
			active = append(active, e)
		}
	}
	i := 0
	for i < len(active) && i < len(ch) &&
		active[i].Round == ch[i].round && active[i].Outcome == ch[i].outcome {
		i++
	}
	for j := i; j < len(active); j++ {
		active[j].Superseded = true
	}
	ver := len(r.Versions) - 1
	for j := i; j < len(ch); j++ {
		r.Trail = append(r.Trail, &TrailEntry{
			At:      at,
			Round:   ch[j].round,
			Outcome: ch[j].outcome,
			Final:   ch[j].final,
			Version: ver,
		})
	}
}

// recheckApplicant 在回避条件变化（新受理申请、新登记关系、评审作废）后，
// 检查该申报人所有进行中评审的评委构成，必要时替补或中止。
func (s *Service) recheckApplicant(app *Applicant, at time.Time) {
	for _, rid := range s.reviewOrder {
		r := s.reviews[rid]
		if r.ApplicantID != app.ID || r.Aborted || r.Voided {
			continue
		}
		if r.finalEntry() != nil && s.isEffective(r, at) {
			continue
		}
		for {
			out := int64(-1)
			for _, m := range r.members() {
				if s.mustRecuse(app, m) {
					out = m
					break
				}
			}
			if out < 0 {
				break
			}
			s.substitute(r, app, out, at)
			if r.Aborted {
				break
			}
		}
		s.settleAndTrail(r, at)
	}
}

// substitute 处理一次评委退出：该评委在本评审的全部已投票作废，
// 以最小编号的合格替补者补入，形成新的评委构成版本。
func (s *Service) substitute(r *Review, app *Applicant, out int64, at time.Time) {
	newVerIdx := len(r.Versions)
	for _, v := range r.Votes {
		if v.ExpertID == out && v.VoidedAt < 0 {
			v.VoidedAt = newVerIdx
		}
	}
	cur := r.members()
	counts := map[string]int{}
	curSet := map[int64]bool{}
	for _, m := range cur {
		if m != out {
			counts[s.experts[m].Group]++
			curSet[m] = true
		}
	}
	sub, ok := s.findSubstitute(app, curSet, counts, r.GroupMins)
	members := []int64{}
	for _, m := range cur {
		if m != out {
			members = append(members, m)
		}
	}
	if !ok {
		r.Aborted = true
		r.Versions = append(r.Versions, &PanelVersion{
			Index:   newVerIdx,
			Members: members,
			Reason:  fmt.Sprintf("expert %d exited (recusal); no substitute available, review aborted", out),
			At:      at,
		})
		return
	}
	members = insertSorted(members, sub)
	r.Versions = append(r.Versions, &PanelVersion{
		Index:   newVerIdx,
		Members: members,
		Reason:  fmt.Sprintf("expert %d exited (recusal); substituted by %d", out, sub),
		At:      at,
	})
}

// isEffective 判定评审结果是否已终局生效：
// 公示期已满且（无异议或异议已裁定不成立）。
func (s *Service) isEffective(r *Review, now time.Time) bool {
	fin := r.finalEntry()
	if fin == nil {
		return false
	}
	if now.Before(fin.At.Add(PublicityDuration)) {
		return false
	}
	return r.Objection == nil || (r.Objection.Ruled && !r.Objection.Upheld)
}

// statusOf 由留痕与异议状态推导评审当前状态。
func (s *Service) statusOf(r *Review, now time.Time) Status {
	if r.Aborted {
		return StatusAborted
	}
	if r.Voided {
		return StatusVoid
	}
	if r.finalEntry() == nil {
		return StatusActive
	}
	if s.isEffective(r, now) {
		return StatusEffective
	}
	return StatusAnnounced
}
