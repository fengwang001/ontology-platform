package review

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReviewView 评审的只读视图（深拷贝，含推导状态）。
type ReviewView struct {
	ID             int64
	ApplicantID    int64
	N              int
	GroupMins      map[string]int
	Status         Status
	Result         *Outcome
	Rounds         int
	Members        []int64
	Versions       []PanelVersion
	Votes          []VoteRecord
	Trail          []TrailEntry
	Objection      *Objection
	PublicityStart *time.Time
	PublicityEnd   *time.Time
}

// ReviewView 返回评审视图；第二个返回值表示评审是否存在。
func (s *Service) ReviewView(reviewID int64) (ReviewView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reviews[reviewID]
	if !ok {
		return ReviewView{}, false
	}
	view := ReviewView{
		ID:          r.ID,
		ApplicantID: r.ApplicantID,
		N:           r.N,
		GroupMins:   make(map[string]int, len(r.GroupMins)),
		Status:      s.statusOf(r, s.now),
		Rounds:      r.rounds,
		Members:     slices.Clone(r.members()),
	}
	for g, m := range r.GroupMins {
		view.GroupMins[g] = m
	}
	for _, v := range r.Versions {
		view.Versions = append(view.Versions, PanelVersion{
			Index:   v.Index,
			Members: slices.Clone(v.Members),
			Reason:  v.Reason,
			At:      v.At,
		})
	}
	for _, v := range r.Votes {
		view.Votes = append(view.Votes, *v)
	}
	for _, e := range r.Trail {
		view.Trail = append(view.Trail, *e)
	}
	if r.Objection != nil {
		o := *r.Objection
		view.Objection = &o
	}
	if fin := r.finalEntry(); fin != nil {
		o := fin.Outcome
		view.Result = &o
		start := fin.At
		end := fin.At.Add(PublicityDuration)
		view.PublicityStart = &start
		view.PublicityEnd = &end
	}
	return view, true
}

// VoteRecords 返回评审的全部投票记录（含已作废票），按发生顺序。
func (s *Service) VoteRecords(reviewID int64) ([]VoteRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reviews[reviewID]
	if !ok {
		return nil, newErr(ErrNotFound, "VoteRecords", "review %d not found", reviewID)
	}
	out := make([]VoteRecord, 0, len(r.Votes))
	for _, v := range r.Votes {
		out = append(out, *v)
	}
	return out, nil
}

// EffectiveVotesAtVersion 按任一历史评委构成版本还原当时的有效票集合：
// 该版本成员所投、投票时间不晚于该版本、且在该版本期间尚未作废的票。
func (s *Service) EffectiveVotesAtVersion(reviewID int64, version int) ([]VoteRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reviews[reviewID]
	if !ok {
		return nil, newErr(ErrNotFound, "EffectiveVotesAtVersion", "review %d not found", reviewID)
	}
	if version < 0 || version >= len(r.Versions) {
		return nil, newErr(ErrInvalidParam, "EffectiveVotesAtVersion", "version %d out of range", version)
	}
	members := map[int64]bool{}
	for _, m := range r.Versions[version].Members {
		members[m] = true
	}
	out := []VoteRecord{}
	for _, v := range r.Votes {
		if !members[v.ExpertID] {
			continue
		}
		if v.Version > version {
			continue
		}
		if v.VoidedAt >= 0 && v.VoidedAt <= version {
			continue
		}
		out = append(out, *v)
	}
	return out, nil
}

// Snapshot 返回全量确定性快照，用于重放等价与模型对照验证。
func (s *Service) Snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d\n", s.now.UnixNano())
	b.WriteString("experts:")
	for _, id := range s.expertOrder {
		e := s.experts[id]
		fmt.Fprintf(&b, " %d=%s/%s", id, e.Unit, e.Group)
	}
	b.WriteString("\napplicants:")
	for _, id := range sortedKeysApplicant(s.applicants) {
		a := s.applicants[id]
		fmt.Fprintf(&b, " %d=%s rel=%v pend=%v acc=%v void=%v",
			id, a.Unit, sortedKeys(a.Relations), sortedKeys(a.RecusalPending),
			sortedKeys(a.RecusalAccepted), sortedKeys(a.VoidedExperts))
	}
	b.WriteString("\nreviews:\n")
	for _, rid := range s.reviewOrder {
		r := s.reviews[rid]
		fmt.Fprintf(&b, " id=%d app=%d n=%d mins=%s status=%s aborted=%v voided=%v rounds=%d\n",
			r.ID, r.ApplicantID, r.N, formatMins(r.GroupMins), s.statusOf(r, s.now),
			r.Aborted, r.Voided, r.rounds)
		for _, v := range r.Versions {
			fmt.Fprintf(&b, "  ver%d members=%v at=%d reason=%q\n", v.Index, v.Members, v.At.UnixNano(), v.Reason)
		}
		for _, v := range r.Votes {
			fmt.Fprintf(&b, "  vote e=%d r=%d c=%d ver=%d voidedAt=%d at=%d\n",
				v.ExpertID, v.Round, int(v.Choice), v.Version, v.VoidedAt, v.At.UnixNano())
		}
		for _, e := range r.Trail {
			fmt.Fprintf(&b, "  trail r=%d o=%s final=%v ver=%d sup=%v at=%d\n",
				e.Round, e.Outcome, e.Final, e.Version, e.Superseded, e.At.UnixNano())
		}
		if r.Objection != nil {
			o := r.Objection
			fmt.Fprintf(&b, "  objection ruled=%v upheld=%v filed=%d ruledAt=%d reason=%q\n",
				o.Ruled, o.Upheld, o.FiledAt.UnixNano(), o.RuledAt.UnixNano(), o.Reason)
		}
	}
	return b.String()
}

func sortedKeysApplicant(m map[int64]*Applicant) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func formatMins(mins map[string]int) string {
	keys := make([]string, 0, len(mins))
	for k := range mins {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, mins[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
