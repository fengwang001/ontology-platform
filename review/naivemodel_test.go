package review

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"
)

// naiveModel 是与 Service 完全独立、按需求文字直接重写的朴素参考模型。
// 它不复用 production 的任何算法函数（抽取、阈值、结算均自行实现），
// 以直白结构保存全部事实，供随机差分测试对照。
type naiveModel struct {
	t          time.Time
	reviewers  map[int]nRev
	applicants map[int]string
	relations  map[[2]int]bool
	requests   map[[2]int]bool
	autoAvoid  map[[2]int]bool
	reviews    map[int]*nReview
	occupied   map[int]int
	nextReview int
}

type nRev struct{ unit, group string }

type nReview struct {
	app           int
	n             int
	minGroups     map[string]int
	status        string // voting/publicity/pass/fail/void/aborted
	panel         map[int]int
	votes         map[[3]int]Choice // (轮次,评委,幕次) -> 票型；作废删除键
	voidedVotes   map[[3]int]bool
	episode       int
	round         int
	round2Open    bool
	round1Settled bool
	tentativePass bool
	pubEnd        time.Time
	objection     bool
	adjudged      bool
	version       int
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		reviewers:  map[int]nRev{},
		applicants: map[int]string{},
		relations:  map[[2]int]bool{},
		requests:   map[[2]int]bool{},
		autoAvoid:  map[[2]int]bool{},
		reviews:    map[int]*nReview{},
		occupied:   map[int]int{},
		nextReview: 1,
	}
}

func (m *naiveModel) tick() time.Time {
	m.t = m.t.Add(time.Minute)
	return m.t
}

func (m *naiveModel) recused(appID, revID int) bool {
	return m.applicants[appID] == m.reviewers[revID].unit ||
		m.relations[[2]int{appID, revID}] ||
		m.requests[[2]int{appID, revID}] ||
		m.autoAvoid[[2]int{appID, revID}]
}

func nCeilDiv(a, b int) int { return (a + b - 1) / b }

// drawNaive 朴素枚举全部 n 人组合，返回字典序最小可行者。
func (m *naiveModel) drawNaive(appID int, n int, minG map[string]int) []int {
	var cand []int
	for id := range m.reviewers {
		if _, busy := m.occupied[id]; busy {
			continue
		}
		if !m.recused(appID, id) {
			cand = append(cand, id)
		}
	}
	sort.Ints(cand)
	var best []int
	var comb func(int, []int)
	comb = func(start int, pick []int) {
		if len(pick) == n {
			cnt := map[string]int{}
			for _, id := range pick {
				cnt[m.reviewers[id].group]++
			}
			for g, need := range minG {
				if cnt[g] < need {
					return
				}
			}
			cp := append([]int(nil), pick...)
			if best == nil || lessInts(cp, best) {
				best = cp
			}
			return
		}
		for i := start; i < len(cand); i++ {
			comb(i+1, append(pick, cand[i]))
		}
	}
	comb(0, nil)
	return best
}

func grpName(g int) string {
	if g%2 == 0 {
		return "B"
	}
	return "A"
}

var _ = strconv.Itoa
var _ = fmt.Sprintf

// diffLog 控制差分测试逐行日志（默认关闭，设置 REVIEW_DIFF_LOG=1 打开）。
var diffLog = os.Getenv("REVIEW_DIFF_LOG") == "1"

func dlog(format string, args ...any) {
	if diffLog {
		fmt.Printf("    [diff] "+format+"\n", args...)
	}
}

// Op 是差分测试中的一步操作。
type Op struct {
	name string
	args []int
}

func (o Op) String() string {
	return o.name + fmt.Sprint(o.args)
}

// apply 执行一步，返回 (结果摘要, 错误类别中文名；成功时错误为 "")。
func (m *naiveModel) apply(op Op) (string, string) {
	t := m.tick()
	switch op.name {
	case "addReviewer":
		id, u, g := op.args[0], op.args[1], op.args[2]
		if id <= 0 || u <= 0 || g <= 0 {
			return "", "参数非法"
		}
		if _, ok := m.reviewers[id]; ok {
			return "", "参数非法"
		}
		m.reviewers[id] = nRev{unit: "U" + strconv.Itoa(u), group: grpName(g)}
		return "ok", ""
	case "addApplicant":
		id, u := op.args[0], op.args[1]
		if id <= 0 || u <= 0 {
			return "", "参数非法"
		}
		if _, ok := m.applicants[id]; ok {
			return "", "参数非法"
		}
		m.applicants[id] = "U" + strconv.Itoa(u)
		return "ok", ""
	case "relation", "recusalRequest":
		a, r := op.args[0], op.args[1]
		if a <= 0 || r <= 0 {
			return "", "参数非法"
		}
		if _, ok := m.applicants[a]; !ok {
			return "", "申报人或评审不存在"
		}
		if _, ok := m.reviewers[r]; !ok {
			return "", "申报人或评审不存在"
		}
		if op.name == "relation" {
			m.relations[[2]int{a, r}] = true
		} else {
			m.requests[[2]int{a, r}] = true
		}
		return "ok", ""
	case "create":
		a, n, reqB := op.args[0], op.args[1], op.args[2]
		if a <= 0 || n <= 0 || n%2 == 0 {
			return "", "参数非法"
		}
		if _, ok := m.applicants[a]; !ok {
			return "", "申报人或评审不存在"
		}
		minG := map[string]int{}
		if reqB == 1 {
			minG["B"] = nCeilDiv(n, 2)
		}
		panel := m.drawNaive(a, n, minG)
		if panel == nil {
			return "", "评委不足"
		}
		id := m.nextReview
		m.nextReview++
		pm := map[int]int{}
		for _, rid := range panel {
			pm[rid] = 1
			m.occupied[rid] = id
		}
		m.reviews[id] = &nReview{
			app: a, n: n, minGroups: minG, status: "voting",
			panel: pm, votes: map[[3]int]Choice{}, voidedVotes: map[[3]int]bool{},
			round: 1, version: 1,
		}
		return fmt.Sprintf("review=%d panel=%v", id, panel), ""
	}
	return m.applyPhase2(t, op)
}

func (m *naiveModel) applyPhase2(t time.Time, op Op) (string, string) {
	switch op.name {
	case "vote":
		rid, judge, rnd, ch := op.args[0], op.args[1], op.args[2], op.args[3]
		if rid <= 0 || judge <= 0 || rnd < 1 || rnd > 2 || ch < 1 || ch > 3 {
			return "", "参数非法"
		}
		r, ok := m.reviews[rid]
		if !ok {
			return "", "申报人或评审不存在"
		}
		if r.status != "voting" {
			return "", "状态不允许"
		}
		if rnd != r.round || (rnd == 2 && !r.round2Open) {
			return "", "状态不允许"
		}
		if _, on := r.panel[judge]; !on {
			return "", "无权限"
		}
		key := [3]int{rnd, judge, r.episode}
		if _, dup := r.votes[key]; dup {
			return "", "重复投票"
		}
		r.votes[key] = Choice(ch)
		m.settleIfComplete(t, r)
		return "ok", ""
	case "recuse":
		rid, judge := op.args[0], op.args[1]
		if rid <= 0 || judge <= 0 {
			return "", "参数非法"
		}
		r, ok := m.reviews[rid]
		if !ok {
			return "", "申报人或评审不存在"
		}
		if r.status != "voting" {
			return "", "状态不允许"
		}
		if _, on := r.panel[judge]; !on {
			return "", "状态不允许"
		}
		if !m.recused(r.app, judge) {
			return "", "状态不允许"
		}
		leftGroup := m.reviewers[judge].group
		delete(r.panel, judge)
		for k := range r.votes {
			if k[1] == judge {
				delete(r.votes, k)
				r.voidedVotes[k] = true
			}
		}
		hadRound1 := r.round1Settled
		rem := map[string]int{}
		for id := range r.panel {
			rem[m.reviewers[id].group]++
		}
		needGroup := ""
		if rem[leftGroup] < r.minGroups[leftGroup] {
			needGroup = leftGroup
		}
		sub := -1
		for id := 1; id <= 10000; id++ {
			rev, exists := m.reviewers[id]
			if !exists {
				continue
			}
			if _, on := r.panel[id]; on {
				continue
			}
			if holder, busy := m.occupied[id]; busy && holder != rid {
				continue
			}
			if m.recused(r.app, id) {
				continue
			}
			if needGroup != "" && rev.group != needGroup {
				continue
			}
			sub = id
			break
		}
		if sub < 0 {
			for id := range r.panel {
				delete(m.occupied, id)
			}
			delete(m.occupied, judge)
			r.status = "aborted"
			return "", "评委不足"
		}
		r.panel[sub] = 1
		r.version++
		m.occupied[sub] = rid
		delete(m.occupied, judge)
		if hadRound1 {
			r.round1Settled = false
			r.round = 1
			r.round2Open = false
			r.episode++
			// 旧幕复议票退出有效集合（保留在 voidedVotes 之外的历史键中）。
			for k := range r.votes {
				if k[0] == 2 && k[2] != r.episode {
					delete(r.votes, k)
				}
			}
		}
		return fmt.Sprintf("sub=%d", sub), ""
	case "objection":
		rid := op.args[0]
		if rid <= 0 {
			return "", "参数非法"
		}
		r, ok := m.reviews[rid]
		if !ok {
			return "", "申报人或评审不存在"
		}
		if r.status != "publicity" || r.objection || !t.Before(r.pubEnd) {
			return "", "状态不允许"
		}
		r.objection = true
		return "ok", ""
	case "adjudge":
		rid, upheld := op.args[0], op.args[1]
		if rid <= 0 {
			return "", "参数非法"
		}
		r, ok := m.reviews[rid]
		if !ok {
			return "", "申报人或评审不存在"
		}
		if r.status != "publicity" || !r.objection || r.adjudged {
			return "", "状态不允许"
		}
		r.adjudged = true
		if upheld == 0 {
			return "ok", ""
		}
		for id := range r.panel {
			m.autoAvoid[[2]int{r.app, id}] = true
			delete(m.occupied, id)
		}
		r.status = "void"
		return "void", ""
	case "finalize":
		rid := op.args[0]
		if rid <= 0 {
			return "", "参数非法"
		}
		r, ok := m.reviews[rid]
		if !ok {
			return "", "申报人或评审不存在"
		}
		if r.status != "publicity" {
			return "", "状态不允许"
		}
		if t.Before(r.pubEnd) || (r.objection && !r.adjudged) {
			return "", "状态不允许"
		}
		for id := range r.panel {
			delete(m.occupied, id)
		}
		if r.tentativePass {
			r.status = "pass"
		} else {
			r.status = "fail"
		}
		return r.status, ""
	}
	return "", "未知操作"
}

// settleIfComplete 朴素结算：以当前有效票数与当前 panel 人数比较。
func (m *naiveModel) settleIfComplete(t time.Time, r *nReview) {
	votes := map[int]Choice{}
	for k, c := range r.votes {
		if k[0] != r.round || k[2] != r.episode {
			continue
		}
		votes[k[1]] = c
	}
	if len(votes) != len(r.panel) {
		return
	}
	var approve int
	for _, c := range votes {
		if c == Approve {
			approve++
		}
	}
	total := len(r.panel)
	if r.round == 1 {
		r.round1Settled = true
		switch {
		case approve >= nCeilDiv(2*total, 3):
			r.status = "publicity"
			r.tentativePass = true
			r.pubEnd = t.Add(7 * 24 * time.Hour)
		case approve <= total/2:
			r.status = "publicity"
			r.tentativePass = false
			r.pubEnd = t.Add(7 * 24 * time.Hour)
		default:
			r.round = 2
			r.round2Open = true
			r.episode++
		}
		return
	}
	r.status = "publicity"
	r.tentativePass = approve*2 > total
	r.pubEnd = t.Add(7 * 24 * time.Hour)
}
