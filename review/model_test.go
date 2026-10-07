package review

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// 朴素模型：独立编写的参照实现。语义与正式实现相同，但刻意采用
// 最直接、显然正确的写法——回避判定线性扫描全部历史评审，
// 抽取暴力枚举全部子集，结算每次从头推导，用于随机对照验证。

type nExpert struct {
	id          int64
	unit, group string
}

type nApplicant struct {
	id        int64
	unit      string
	relations []int64
	pending   []int64
	accepted  []int64
}

type nVersion struct {
	members []int64
	reason  string
	at      time.Time
}

type nVote struct {
	expert   int64
	round    int
	choice   Choice
	version  int
	at       time.Time
	voidedAt int
}

type nTrailEntry struct {
	at         time.Time
	round      int
	outcome    Outcome
	final      bool
	version    int
	superseded bool
}

type nObjection struct {
	reason  string
	filedAt time.Time
	ruled   bool
	upheld  bool
	ruledAt time.Time
}

type nReview struct {
	id, appID int64
	n         int
	mins      map[string]int
	versions  []nVersion
	votes     []nVote
	trail     []nTrailEntry
	objection *nObjection
	aborted   bool
	voided    bool
	rounds    int
}

func (r *nReview) members() []int64 { return r.versions[len(r.versions)-1].members }

func (r *nReview) final() *nTrailEntry {
	for i := len(r.trail) - 1; i >= 0; i-- {
		if r.trail[i].final && !r.trail[i].superseded {
			return &r.trail[i]
		}
	}
	return nil
}

type naive struct {
	now        time.Time
	experts    []nExpert
	applicants []*nApplicant
	reviews    []*nReview
	nextID     int64
}

func newNaive(start time.Time) *naive {
	return &naive{now: start, nextID: 1}
}

func hasInt(xs []int64, x int64) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (n *naive) findExpert(id int64) *nExpert {
	for i := range n.experts {
		if n.experts[i].id == id {
			return &n.experts[i]
		}
	}
	return nil
}

func (n *naive) findApplicant(id int64) *nApplicant {
	for _, a := range n.applicants {
		if a.id == id {
			return a
		}
	}
	return nil
}

func (n *naive) findReview(id int64) *nReview {
	for _, r := range n.reviews {
		if r.id == id {
			return r
		}
	}
	return nil
}

// recused 朴素回避判定：线性扫描全部历史评审（故意低效，与正式实现对照）。
func (n *naive) recused(appID, expertID int64) bool {
	e := n.findExpert(expertID)
	a := n.findApplicant(appID)
	if e.unit == a.unit {
		return true
	}
	if hasInt(a.relations, expertID) || hasInt(a.accepted, expertID) {
		return true
	}
	for _, r := range n.reviews {
		if r.appID != appID || !r.voided {
			continue
		}
		for _, v := range r.versions {
			if hasInt(v.members, expertID) {
				return true
			}
		}
	}
	return false
}

// draw 朴素抽取：暴力枚举全部子集，取满足条件且字典序最小者。
func (n *naive) draw(appID int64, size int, mins map[string]int) ([]int64, bool) {
	var cand []int64
	for _, e := range n.experts {
		if !n.recused(appID, e.id) {
			cand = append(cand, e.id)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i] < cand[j] })
	var best []int64
	var rec func(start int, cur []int64)
	rec = func(start int, cur []int64) {
		if len(cur) == size {
			counts := map[string]int{}
			for _, id := range cur {
				counts[n.findExpert(id).group]++
			}
			ok := true
			for g, m := range mins {
				if counts[g] < m {
					ok = false
					break
				}
			}
			if ok && (best == nil || lessInts(cur, best)) {
				best = append([]int64(nil), cur...)
			}
			return
		}
		if len(cur)+len(cand)-start < size {
			return
		}
		for i := start; i < len(cand); i++ {
			nc := append(append([]int64(nil), cur...), cand[i])
			rec(i+1, nc)
		}
	}
	rec(0, nil)
	return best, best != nil
}

func (n *naive) isEffective(r *nReview, now time.Time) bool {
	fin := r.final()
	if fin == nil {
		return false
	}
	if now.Before(fin.at.Add(PublicityDuration)) {
		return false
	}
	return r.objection == nil || (r.objection.ruled && !r.objection.upheld)
}

func (n *naive) statusOf(r *nReview) Status {
	if r.aborted {
		return StatusAborted
	}
	if r.voided {
		return StatusVoid
	}
	if r.final() == nil {
		return StatusActive
	}
	if n.isEffective(r, n.now) {
		return StatusEffective
	}
	return StatusAnnounced
}

func (n *naive) effVotes(r *nReview, round int) []nVote {
	cur := map[int64]bool{}
	for _, m := range r.members() {
		cur[m] = true
	}
	out := []nVote{}
	for _, v := range r.votes {
		if v.round == round && v.voidedAt < 0 && cur[v.expert] {
			out = append(out, v)
		}
	}
	return out
}

// settle 朴素结算：每次从有效票集合从头推导结论链，再与留痕对账。
func (n *naive) settle(r *nReview, at time.Time) {
	if r.aborted || r.voided {
		return
	}
	type st struct {
		round   int
		outcome Outcome
		final   bool
	}
	chain := []st{}
	eff1 := n.effVotes(r, 1)
	if len(eff1) >= r.n {
		ap := 0
		for _, v := range eff1 {
			if v.choice == ChoiceApprove {
				ap++
			}
		}
		var o1 Outcome
		switch {
		case ap >= (2*r.n+2)/3:
			o1 = OutcomePass
		case ap <= r.n/2:
			o1 = OutcomeFail
		default:
			o1 = OutcomeRevote
		}
		chain = append(chain, st{1, o1, o1 != OutcomeRevote})
		if o1 == OutcomeRevote {
			if r.rounds < 2 {
				r.rounds = 2
			}
			eff2 := n.effVotes(r, 2)
			if len(eff2) >= r.n {
				ap2 := 0
				for _, v := range eff2 {
					if v.choice == ChoiceApprove {
						ap2++
					}
				}
				o2 := OutcomeFail
				if ap2 > r.n/2 {
					o2 = OutcomePass
				}
				chain = append(chain, st{2, o2, true})
			}
		}
	}
	active := []int{}
	for i := range r.trail {
		if !r.trail[i].superseded {
			active = append(active, i)
		}
	}
	i := 0
	for i < len(active) && i < len(chain) &&
		r.trail[active[i]].round == chain[i].round && r.trail[active[i]].outcome == chain[i].outcome {
		i++
	}
	for j := i; j < len(active); j++ {
		r.trail[active[j]].superseded = true
	}
	ver := len(r.versions) - 1
	for j := i; j < len(chain); j++ {
		r.trail = append(r.trail, nTrailEntry{
			at:      at,
			round:   chain[j].round,
			outcome: chain[j].outcome,
			final:   chain[j].final,
			version: ver,
		})
	}
}

// substitute 朴素替补：按编号升序扫描全部评委，取首个合格者。
func (n *naive) substitute(r *nReview, appID, out int64, at time.Time) {
	newVer := len(r.versions)
	for i := range r.votes {
		if r.votes[i].expert == out && r.votes[i].voidedAt < 0 {
			r.votes[i].voidedAt = newVer
		}
	}
	cur := r.members()
	counts := map[string]int{}
	curSet := map[int64]bool{}
	for _, m := range cur {
		if m != out {
			counts[n.findExpert(m).group]++
			curSet[m] = true
		}
	}
	ids := []int64{}
	for _, e := range n.experts {
		ids = append(ids, e.id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sub := int64(-1)
	for _, id := range ids {
		if curSet[id] || n.recused(appID, id) {
			continue
		}
		g := n.findExpert(id).group
		counts[g]++
		ok := true
		for mg, m := range r.mins {
			if counts[mg] < m {
				ok = false
				break
			}
		}
		counts[g]--
		if ok {
			sub = id
			break
		}
	}
	members := []int64{}
	for _, m := range cur {
		if m != out {
			members = append(members, m)
		}
	}
	if sub < 0 {
		r.aborted = true
		r.versions = append(r.versions, nVersion{
			members: members,
			reason:  fmt.Sprintf("expert %d exited (recusal); no substitute available, review aborted", out),
			at:      at,
		})
		return
	}
	members = append(members, sub)
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	r.versions = append(r.versions, nVersion{
		members: members,
		reason:  fmt.Sprintf("expert %d exited (recusal); substituted by %d", out, sub),
		at:      at,
	})
}

// recheck 朴素复查：扫描该申报人全部评审。
func (n *naive) recheck(appID int64, at time.Time) {
	for _, r := range n.reviews {
		if r.appID != appID || r.aborted || r.voided {
			continue
		}
		if r.final() != nil && n.isEffective(r, at) {
			continue
		}
		for {
			out := int64(-1)
			for _, m := range r.members() {
				if n.recused(appID, m) {
					out = m
					break
				}
			}
			if out < 0 {
				break
			}
			n.substitute(r, appID, out, at)
			if r.aborted {
				break
			}
		}
		n.settle(r, at)
	}
}

func (n *naive) addExpert(at time.Time, id int64, unit, group string) error {
	if unit == "" || group == "" {
		return newErr(ErrInvalidParam, "AddExpert", "empty unit/group")
	}
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "AddExpert", "rollback")
	}
	if n.findExpert(id) != nil {
		return newErr(ErrStateNotAllowed, "AddExpert", "exists")
	}
	n.experts = append(n.experts, nExpert{id: id, unit: unit, group: group})
	n.now = at
	return nil
}

func (n *naive) addApplicant(at time.Time, id int64, unit string) error {
	if unit == "" {
		return newErr(ErrInvalidParam, "AddApplicant", "empty unit")
	}
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "AddApplicant", "rollback")
	}
	if n.findApplicant(id) != nil {
		return newErr(ErrStateNotAllowed, "AddApplicant", "exists")
	}
	n.applicants = append(n.applicants, &nApplicant{id: id, unit: unit})
	n.now = at
	return nil
}

func (n *naive) addRelation(at time.Time, appID, expertID int64) error {
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "AddRelation", "rollback")
	}
	a := n.findApplicant(appID)
	if a == nil {
		return newErr(ErrNotFound, "AddRelation", "applicant")
	}
	if n.findExpert(expertID) == nil {
		return newErr(ErrNotFound, "AddRelation", "expert")
	}
	if hasInt(a.relations, expertID) {
		return newErr(ErrStateNotAllowed, "AddRelation", "duplicate")
	}
	a.relations = append(a.relations, expertID)
	n.recheck(appID, at)
	n.now = at
	return nil
}

func (n *naive) applyRecusal(at time.Time, appID, expertID int64) error {
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "ApplyRecusal", "rollback")
	}
	a := n.findApplicant(appID)
	if a == nil {
		return newErr(ErrNotFound, "ApplyRecusal", "applicant")
	}
	if n.findExpert(expertID) == nil {
		return newErr(ErrNotFound, "ApplyRecusal", "expert")
	}
	if hasInt(a.pending, expertID) || hasInt(a.accepted, expertID) {
		return newErr(ErrStateNotAllowed, "ApplyRecusal", "exists")
	}
	a.pending = append(a.pending, expertID)
	n.now = at
	return nil
}

func (n *naive) acceptRecusal(at time.Time, appID, expertID int64) error {
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "AcceptRecusal", "rollback")
	}
	a := n.findApplicant(appID)
	if a == nil {
		return newErr(ErrNotFound, "AcceptRecusal", "applicant")
	}
	if n.findExpert(expertID) == nil {
		return newErr(ErrNotFound, "AcceptRecusal", "expert")
	}
	if !hasInt(a.pending, expertID) {
		return newErr(ErrStateNotAllowed, "AcceptRecusal", "no pending")
	}
	np := []int64{}
	for _, x := range a.pending {
		if x != expertID {
			np = append(np, x)
		}
	}
	a.pending = np
	a.accepted = append(a.accepted, expertID)
	n.recheck(appID, at)
	n.now = at
	return nil
}

func (n *naive) createReview(at time.Time, appID int64, size int, mins map[string]int) (int64, []int64, error) {
	if size <= 0 || size%2 == 0 {
		return 0, nil, newErr(ErrInvalidParam, "CreateReview", "n")
	}
	sum := 0
	for g, m := range mins {
		if g == "" || m < 0 {
			return 0, nil, newErr(ErrInvalidParam, "CreateReview", "mins")
		}
		sum += m
	}
	if sum > size {
		return 0, nil, newErr(ErrInvalidParam, "CreateReview", "sum>n")
	}
	if at.Before(n.now) {
		return 0, nil, newErr(ErrClockRollback, "CreateReview", "rollback")
	}
	if n.findApplicant(appID) == nil {
		return 0, nil, newErr(ErrNotFound, "CreateReview", "applicant")
	}
	panel, ok := n.draw(appID, size, mins)
	if !ok {
		return 0, nil, newErr(ErrInsufficientExperts, "CreateReview", "infeasible")
	}
	cp := make(map[string]int, len(mins))
	for g, m := range mins {
		cp[g] = m
	}
	r := &nReview{
		id:       n.nextID,
		appID:    appID,
		n:        size,
		mins:     cp,
		versions: []nVersion{{members: panel, reason: "initial draw", at: at}},
		rounds:   1,
	}
	n.nextID++
	n.reviews = append(n.reviews, r)
	n.now = at
	return r.id, append([]int64(nil), panel...), nil
}

func (n *naive) vote(at time.Time, reviewID, expertID int64, round int, choice Choice) error {
	if round < 1 || round > 2 {
		return newErr(ErrInvalidParam, "Vote", "round")
	}
	if !choice.valid() {
		return newErr(ErrInvalidParam, "Vote", "choice")
	}
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "Vote", "rollback")
	}
	r := n.findReview(reviewID)
	if r == nil {
		return newErr(ErrNotFound, "Vote", "review")
	}
	if r.aborted || r.voided || r.final() != nil {
		return newErr(ErrStateNotAllowed, "Vote", "closed")
	}
	if round > r.rounds {
		return newErr(ErrStateNotAllowed, "Vote", "round not open")
	}
	if !hasInt(r.members(), expertID) {
		return newErr(ErrNoPermission, "Vote", "not a panelist")
	}
	for _, v := range r.votes {
		if v.round == round && v.expert == expertID && v.voidedAt < 0 {
			return newErr(ErrDuplicateVote, "Vote", "duplicate")
		}
	}
	r.votes = append(r.votes, nVote{
		expert:   expertID,
		round:    round,
		choice:   choice,
		version:  len(r.versions) - 1,
		at:       at,
		voidedAt: -1,
	})
	n.settle(r, at)
	n.now = at
	return nil
}

func (n *naive) fileObjection(at time.Time, reviewID int64, reason string) error {
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "FileObjection", "rollback")
	}
	r := n.findReview(reviewID)
	if r == nil {
		return newErr(ErrNotFound, "FileObjection", "review")
	}
	if r.aborted || r.voided {
		return newErr(ErrStateNotAllowed, "FileObjection", "closed")
	}
	fin := r.final()
	if fin == nil {
		return newErr(ErrStateNotAllowed, "FileObjection", "no result")
	}
	if !at.Before(fin.at.Add(PublicityDuration)) {
		return newErr(ErrStateNotAllowed, "FileObjection", "publicity ended")
	}
	if r.objection != nil {
		return newErr(ErrStateNotAllowed, "FileObjection", "exists")
	}
	r.objection = &nObjection{reason: reason, filedAt: at}
	n.now = at
	return nil
}

func (n *naive) ruleObjection(at time.Time, reviewID int64, upheld bool) error {
	if at.Before(n.now) {
		return newErr(ErrClockRollback, "RuleObjection", "rollback")
	}
	r := n.findReview(reviewID)
	if r == nil {
		return newErr(ErrNotFound, "RuleObjection", "review")
	}
	if r.aborted || r.voided {
		return newErr(ErrStateNotAllowed, "RuleObjection", "closed")
	}
	if r.objection == nil || r.objection.ruled {
		return newErr(ErrStateNotAllowed, "RuleObjection", "no pending objection")
	}
	r.objection.ruled = true
	r.objection.upheld = upheld
	r.objection.ruledAt = at
	if upheld {
		r.voided = true
		// 朴素模型无需维护回避索引：recused 直接扫描作废评审历史。
		n.recheck(r.appID, at)
	}
	n.now = at
	return nil
}

// snapshot 生成与 Service.Snapshot 完全同构的确定性快照。
func (n *naive) snapshot() string {
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d\n", n.now.UnixNano())
	b.WriteString("experts:")
	ids := []int64{}
	for _, e := range n.experts {
		ids = append(ids, e.id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		e := n.findExpert(id)
		fmt.Fprintf(&b, " %d=%s/%s", id, e.unit, e.group)
	}
	b.WriteString("\napplicants:")
	appIDs := []int64{}
	for _, a := range n.applicants {
		appIDs = append(appIDs, a.id)
	}
	sort.Slice(appIDs, func(i, j int) bool { return appIDs[i] < appIDs[j] })
	for _, id := range appIDs {
		a := n.findApplicant(id)
		voided := []int64{}
		for _, r := range n.reviews {
			if r.appID != id || !r.voided {
				continue
			}
			for _, v := range r.versions {
				for _, m := range v.members {
					if !hasInt(voided, m) {
						voided = append(voided, m)
					}
				}
			}
		}
		sort.Slice(voided, func(i, j int) bool { return voided[i] < voided[j] })
		rel := sortedCopy(a.relations)
		pend := sortedCopy(a.pending)
		acc := sortedCopy(a.accepted)
		fmt.Fprintf(&b, " %d=%s rel=%v pend=%v acc=%v void=%v", id, a.unit, rel, pend, acc, voided)
	}
	b.WriteString("\nreviews:\n")
	for _, r := range n.reviews {
		fmt.Fprintf(&b, " id=%d app=%d n=%d mins=%s status=%s aborted=%v voided=%v rounds=%d\n",
			r.id, r.appID, r.n, formatMins(r.mins), n.statusOf(r), r.aborted, r.voided, r.rounds)
		for i, v := range r.versions {
			fmt.Fprintf(&b, "  ver%d members=%v at=%d reason=%q\n", i, v.members, v.at.UnixNano(), v.reason)
		}
		for _, v := range r.votes {
			fmt.Fprintf(&b, "  vote e=%d r=%d c=%d ver=%d voidedAt=%d at=%d\n",
				v.expert, v.round, int(v.choice), v.version, v.voidedAt, v.at.UnixNano())
		}
		for _, e := range r.trail {
			fmt.Fprintf(&b, "  trail r=%d o=%s final=%v ver=%d sup=%v at=%d\n",
				e.round, e.outcome, e.final, e.version, e.superseded, e.at.UnixNano())
		}
		if r.objection != nil {
			o := r.objection
			fmt.Fprintf(&b, "  objection ruled=%v upheld=%v filed=%d ruledAt=%d reason=%q\n",
				o.ruled, o.upheld, o.filedAt.UnixNano(), o.ruledAt.UnixNano(), o.reason)
		}
	}
	return b.String()
}

func sortedCopy(xs []int64) []int64 {
	out := append([]int64{}, xs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---- 随机操作序列对照驱动 ----

func kindOfErr(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(*Error); ok {
		return e.Kind.String()
	}
	return fmt.Sprintf("unknown(%T)", err)
}

// TestRandomizedAgainstNaiveModel 以大量随机操作序列对照正式实现与朴素模型，
// 每步打印输入、输出与判定依据（错误类别即拒绝依据），并比对全量快照。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed_%02d", seed), func(t *testing.T) {
			runRandomSequence(t, seed, 150)
		})
	}
}

func runRandomSequence(t *testing.T, seed int64, steps int) {
	rnd := rand.New(rand.NewSource(seed))
	svc := NewService(t0)
	mdl := newNaive(t0)
	cur := t0
	var reviewIDs []int64
	var pendingRecusals [][2]int64

	// 预置均衡的评委池与申报人，保证评审流程能够推进。
	for i := int64(1); i <= 6; i++ {
		cur = cur.Add(time.Minute)
		u := fmt.Sprintf("U%d", i)
		g := "G1"
		if i%2 == 0 {
			g = "G2"
		}
		if err := svc.AddExpert(cur, i, u, g); err != nil {
			t.Fatal(err)
		}
		if err := mdl.addExpert(cur, i, u, g); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{100, 101} {
		cur = cur.Add(time.Minute)
		if err := svc.AddApplicant(cur, id, "UA"); err != nil {
			t.Fatal(err)
		}
		if err := mdl.addApplicant(cur, id, "UA"); err != nil {
			t.Fatal(err)
		}
	}

	units := []string{"U1", "U2", "U3"}
	groups := []string{"G1", "G2"}
	pickS := func(xs []string) string { return xs[rnd.Intn(len(xs))] }
	expertID := func() int64 { return int64(1 + rnd.Intn(8)) }
	applicantID := func() int64 { return int64(100 + rnd.Intn(3)) }
	reviewID := func() int64 {
		if len(reviewIDs) > 0 && rnd.Intn(4) > 0 {
			return reviewIDs[rnd.Intn(len(reviewIDs))]
		}
		return int64(1 + rnd.Intn(20))
	}
	genMins := func() map[string]int {
		switch rnd.Intn(9) {
		case 0:
			return nil
		case 1:
			return map[string]int{"G1": 1}
		case 2:
			return map[string]int{"G2": 1}
		case 3:
			return map[string]int{"G1": 1, "G2": 1}
		case 4:
			return map[string]int{"G1": 2, "G2": 2}
		case 5:
			return map[string]int{"G1": -1}
		case 6:
			return map[string]int{"": 1}
		case 7:
			return map[string]int{"G1": 3, "G2": 3}
		default:
			return map[string]int{"G2": 2}
		}
	}

	for step := 0; step < steps; step++ {
		// 生成操作时间：多数前进，偶尔原地，偶尔大跳，偶尔回退（应被拒绝）。
		var at time.Time
		switch rnd.Intn(20) {
		case 0:
			at = cur.Add(-time.Hour)
		case 1, 2:
			at = cur
		case 3:
			at = cur.Add(8 * 24 * time.Hour)
		default:
			at = cur.Add(time.Duration(rnd.Intn(36)) * time.Hour)
		}
		if !at.Before(cur) {
			cur = at
		}

		var desc, out string
		var svcErr, mdlErr error
		switch rnd.Intn(12) {
		case 0:
			id, u, g := expertID(), pickS(units), pickS(groups)
			desc = fmt.Sprintf("AddExpert(%d,%s,%s)", id, u, g)
			svcErr = svc.AddExpert(at, id, u, g)
			mdlErr = mdl.addExpert(at, id, u, g)
		case 1:
			id, u := applicantID(), pickS(units)
			desc = fmt.Sprintf("AddApplicant(%d,%s)", id, u)
			svcErr = svc.AddApplicant(at, id, u)
			mdlErr = mdl.addApplicant(at, id, u)
		case 2:
			a, e := applicantID(), expertID()
			// 智能关系登记：一半概率指向进行中评审的现任评委，触发替补。
			if rnd.Intn(2) == 0 && len(reviewIDs) > 0 {
				if v, ok := svc.ReviewView(reviewIDs[rnd.Intn(len(reviewIDs))]); ok && v.Status == StatusActive && len(v.Members) > 0 {
					a, e = v.ApplicantID, v.Members[rnd.Intn(len(v.Members))]
				}
			}
			desc = fmt.Sprintf("AddRelation(%d,%d)", a, e)
			svcErr = svc.AddRelation(at, a, e)
			mdlErr = mdl.addRelation(at, a, e)
		case 3:
			a, e := applicantID(), expertID()
			if rnd.Intn(2) == 0 && len(reviewIDs) > 0 {
				if v, ok := svc.ReviewView(reviewIDs[rnd.Intn(len(reviewIDs))]); ok && v.Status == StatusActive && len(v.Members) > 0 {
					a, e = v.ApplicantID, v.Members[rnd.Intn(len(v.Members))]
				}
			}
			desc = fmt.Sprintf("ApplyRecusal(%d,%d)", a, e)
			svcErr = svc.ApplyRecusal(at, a, e)
			mdlErr = mdl.applyRecusal(at, a, e)
			if svcErr == nil {
				pendingRecusals = append(pendingRecusals, [2]int64{a, e})
			}
		case 4:
			a, e := applicantID(), expertID()
			// 智能受理：优先受理真实存在的待受理申请。
			if len(pendingRecusals) > 0 && rnd.Intn(4) > 0 {
				i := rnd.Intn(len(pendingRecusals))
				a, e = pendingRecusals[i][0], pendingRecusals[i][1]
			}
			desc = fmt.Sprintf("AcceptRecusal(%d,%d)", a, e)
			svcErr = svc.AcceptRecusal(at, a, e)
			mdlErr = mdl.acceptRecusal(at, a, e)
			if svcErr == nil {
				np := pendingRecusals[:0]
				for _, p := range pendingRecusals {
					if p != [2]int64{a, e} {
						np = append(np, p)
					}
				}
				pendingRecusals = np
			}
		case 5, 6:
			a, size, mins := applicantID(), []int{0, 1, 2, 3, 5, 5, 5}[rnd.Intn(7)], genMins()
			desc = fmt.Sprintf("CreateReview(%d,n=%d,mins=%v)", a, size, mins)
			ridS, panelS, errS := svc.CreateReview(at, a, size, mins)
			ridM, panelM, errM := mdl.createReview(at, a, size, mins)
			svcErr, mdlErr = errS, errM
			if errS == nil && errM == nil {
				if ridS != ridM || !reflect.DeepEqual(panelS, panelM) {
					t.Fatalf("step %d %s: svc=(%d,%v) model=(%d,%v)", step, desc, ridS, panelS, ridM, panelM)
				}
				reviewIDs = append(reviewIDs, ridS)
				out = fmt.Sprintf(" rid=%d panel=%v", ridS, panelS)
			}
		case 7, 8:
			// 智能投票：选取进行中评审里尚未投票的现任评委，推动结算。
			rid, e, rd, ch := reviewID(), expertID(), rnd.Intn(4), Choice(rnd.Intn(5))
			if len(reviewIDs) > 0 {
				cand := reviewIDs[rnd.Intn(len(reviewIDs))]
				if v, ok := svc.ReviewView(cand); ok && v.Status == StatusActive {
					voted := map[[2]int]bool{}
					for _, rec := range v.Votes {
						if rec.VoidedAt < 0 {
							voted[[2]int{rec.Round, int(rec.ExpertID)}] = true
						}
					}
				outer:
					for _, round := range []int{1, 2} {
						if round > v.Rounds {
							break
						}
						for _, m := range v.Members {
							if !voted[[2]int{round, int(m)}] {
								// 票型偏向赞成，使第一轮更常落入复议区间。
								c := ChoiceApprove
								switch rnd.Intn(5) {
								case 0:
									c = ChoiceReject
								case 1:
									c = ChoiceAbstain
								}
								rid, e, rd, ch = cand, m, round, c
								break outer
							}
						}
					}
				}
			}
			desc = fmt.Sprintf("Vote(rid=%d,e=%d,round=%d,choice=%d)", rid, e, rd, int(ch))
			svcErr = svc.Vote(at, rid, e, rd, ch)
			mdlErr = mdl.vote(at, rid, e, rd, ch)
		case 9:
			rid, e, rd, ch := reviewID(), expertID(), rnd.Intn(4), Choice(rnd.Intn(5))
			desc = fmt.Sprintf("Vote(rid=%d,e=%d,round=%d,choice=%d)", rid, e, rd, int(ch))
			svcErr = svc.Vote(at, rid, e, rd, ch)
			mdlErr = mdl.vote(at, rid, e, rd, ch)
		case 10:
			// 智能异议：优先选择已宣布结果的评审。
			rid := reviewID()
			for _, id := range reviewIDs {
				if v, ok := svc.ReviewView(id); ok && v.Status == StatusAnnounced && v.Objection == nil {
					rid = id
					break
				}
			}
			desc = fmt.Sprintf("FileObjection(%d)", rid)
			svcErr = svc.FileObjection(at, rid, "random-objection")
			mdlErr = mdl.fileObjection(at, rid, "random-objection")
		case 11:
			// 智能裁定：优先选择有待裁定异议的评审。
			rid, upheld := reviewID(), rnd.Intn(2) == 0
			for _, id := range reviewIDs {
				if v, ok := svc.ReviewView(id); ok && v.Objection != nil && !v.Objection.Ruled {
					rid = id
					break
				}
			}
			desc = fmt.Sprintf("RuleObjection(%d,upheld=%v)", rid, upheld)
			svcErr = svc.RuleObjection(at, rid, upheld)
			mdlErr = mdl.ruleObjection(at, rid, upheld)
		}

		ks, km := kindOfErr(svcErr), kindOfErr(mdlErr)
		t.Logf("step %03d: %-38s => svc=%-20s model=%-20s%s", step, desc, ks, km, out)
		if ks != km {
			t.Fatalf("step %d %s: svc=%s model=%s", step, desc, ks, km)
		}
		snapS, snapM := svc.Snapshot(), mdl.snapshot()
		if snapS != snapM {
			t.Fatalf("step %d %s: snapshot diverged\n--- svc ---\n%s\n--- model ---\n%s", step, desc, snapS, snapM)
		}
	}
	t.Logf("final snapshot:\n%s", svc.Snapshot())
}
