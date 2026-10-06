package exam

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 本文件包含一个按需求规则独立写成的朴素模型（naive），与 Engine
// 对照执行随机操作序列。朴素模型刻意使用最直接的实现：互斥判定每次
// 扫描全题库求传递闭包，校验用显式循环，以此交叉验证 Engine 的
// 增量邻接表、校验顺序与状态机。

type nVersion struct {
	score, diff int
	kps         []string
}

type nQuestion struct {
	life     Lifecycle
	versions []nVersion
	groups   map[string]bool
}

type nEntry struct {
	qid         string
	ver         int
	score, diff int
	kps         []string
}

type nPaper struct {
	state   PaperState
	cons    Constraints
	draft   []string
	entries []nEntry
}

type naive struct {
	qs map[string]*nQuestion
	ps map[string]*nPaper
}

func newNaive() *naive {
	return &naive{qs: map[string]*nQuestion{}, ps: map[string]*nPaper{}}
}

func nParamsOK(id string, score, diff int) bool {
	return id != "" && score > 0 && diff >= 1
}

func (n *naive) createQuestion(id string, score, diff int, kps, groups []string) Category {
	if !nParamsOK(id, score, diff) {
		return CatInvalidParam
	}
	if _, dup := n.qs[id]; dup {
		return CatInvalidParam
	}
	gs := map[string]bool{}
	for _, g := range groups {
		if g != "" {
			gs[g] = true
		}
	}
	n.qs[id] = &nQuestion{
		life:     Available,
		versions: []nVersion{{score, diff, sortedCopy(kps)}},
		groups:   gs,
	}
	return 0
}

// ---------------- 对照测试：状态摘要 ----------------

func digestEngine(e *Engine, qids, pids []string) string {
	var b strings.Builder
	for _, qid := range qids {
		life, versions, groups, ok := e.QuestionInfo(qid)
		if !ok {
			fmt.Fprintf(&b, "Q %s -\n", qid)
			continue
		}
		fmt.Fprintf(&b, "Q %s %s g=%v\n", qid, life, groups)
		for _, v := range versions {
			fmt.Fprintf(&b, "  v%d s=%d d=%d k=%v\n", v.Number, v.Score, v.Difficulty, v.KnowledgePoints)
		}
	}
	for _, pid := range pids {
		state, entries, draft, ok := e.PaperInfo(pid)
		if !ok {
			fmt.Fprintf(&b, "P %s -\n", pid)
			continue
		}
		fmt.Fprintf(&b, "P %s %s draft=%v\n", pid, state, draft)
		for _, en := range entries {
			fmt.Fprintf(&b, "  %s@v%d s=%d d=%d k=%v\n", en.QuestionID, en.Version, en.Score, en.Difficulty, en.KnowledgePoints)
		}
	}
	return b.String()
}

func digestNaive(n *naive, qids, pids []string) string {
	var b strings.Builder
	for _, qid := range qids {
		q, ok := n.qs[qid]
		if !ok {
			fmt.Fprintf(&b, "Q %s -\n", qid)
			continue
		}
		fmt.Fprintf(&b, "Q %s %s g=%v\n", qid, q.life, sortedKeysOf(q.groups))
		for i, v := range q.versions {
			fmt.Fprintf(&b, "  v%d s=%d d=%d k=%v\n", i+1, v.score, v.diff, v.kps)
		}
	}
	for _, pid := range pids {
		p, ok := n.ps[pid]
		if !ok {
			fmt.Fprintf(&b, "P %s -\n", pid)
			continue
		}
		fmt.Fprintf(&b, "P %s %s draft=%v\n", pid, p.state, nilIfEmpty(p.draft))
		for _, en := range p.entries {
			fmt.Fprintf(&b, "  %s@v%d s=%d d=%d k=%v\n", en.qid, en.ver, en.score, en.diff, en.kps)
		}
	}
	return b.String()
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// ---------------- 对照测试：随机操作序列 ----------------

type op struct {
	name        string
	a, b        string
	score, diff int
	kps, groups []string
	cons        Constraints
}

func (o op) String() string {
	switch o.name {
	case "createQ":
		return fmt.Sprintf("createQ(%s score=%d diff=%d kps=%v groups=%v)", o.a, o.score, o.diff, o.kps, o.groups)
	case "revise":
		return fmt.Sprintf("revise(%s score=%d diff=%d kps=%v)", o.a, o.score, o.diff, o.kps)
	case "suspend", "resume", "withdraw":
		return fmt.Sprintf("%s(%s)", o.name, o.a)
	case "setGroups":
		return fmt.Sprintf("setGroups(%s %v)", o.a, o.groups)
	case "createP":
		return fmt.Sprintf("createP(%s %+v)", o.a, o.cons)
	case "add", "remove":
		return fmt.Sprintf("%s(%s %s)", o.name, o.a, o.b)
	case "validate", "publish":
		return fmt.Sprintf("%s(%s)", o.name, o.a)
	case "replace":
		return fmt.Sprintf("replace(%s %s -> %s)", o.a, o.b, o.groups[0])
	case "assemble":
		return fmt.Sprintf("assemble(%s qs=%v target=%d)", o.a, o.groups, o.cons.TargetScore)
	}
	return o.name
}

type outcome struct {
	cat    Category // 0 表示成功
	detail string
}

func catOf(err error) outcome {
	if err == nil {
		return outcome{}
	}
	ce, ok := err.(*Error)
	if !ok {
		return outcome{cat: -1, detail: err.Error()}
	}
	return outcome{cat: ce.Category, detail: ce.Reason}
}

func runOnEngine(e *Engine, o op) outcome {
	switch o.name {
	case "createQ":
		return catOf(e.CreateQuestion(o.a, o.score, o.diff, o.kps, o.groups))
	case "revise":
		n, err := e.ReviseQuestion(o.a, o.score, o.diff, o.kps)
		out := catOf(err)
		out.detail = strconv.Itoa(n)
		return out
	case "suspend":
		return catOf(e.SuspendQuestion(o.a))
	case "resume":
		return catOf(e.ResumeQuestion(o.a))
	case "withdraw":
		affected, err := e.WithdrawQuestion(o.a)
		out := catOf(err)
		out.detail = strings.Join(affected, ",")
		return out
	case "setGroups":
		return catOf(e.SetQuestionGroups(o.a, o.groups))
	case "createP":
		return catOf(e.CreatePaper(o.a, o.cons))
	case "add":
		return catOf(e.AddToPaper(o.a, o.b))
	case "remove":
		return catOf(e.RemoveFromPaper(o.a, o.b))
	case "validate":
		return catOf(e.ValidateDraft(o.a))
	case "publish":
		return catOf(e.Publish(o.a))
	case "replace":
		return catOf(e.Replace(o.a, o.b, o.groups[0]))
	case "assemble":
		if err := e.CreatePaper(o.a, o.cons); err != nil {
			return catOf(err)
		}
		for _, q := range o.groups {
			if err := e.AddToPaper(o.a, q); err != nil {
				return catOf(err)
			}
		}
		return catOf(e.Publish(o.a))
	}
	panic("unknown op " + o.name)
}

func runOnNaive(n *naive, o op) outcome {
	switch o.name {
	case "createQ":
		return outcome{cat: n.createQuestion(o.a, o.score, o.diff, o.kps, o.groups)}
	case "revise":
		ver, cat := n.revise(o.a, o.score, o.diff, o.kps)
		return outcome{cat: cat, detail: strconv.Itoa(ver)}
	case "suspend":
		return outcome{cat: n.transition(o.a, Suspended)}
	case "resume":
		return outcome{cat: n.transition(o.a, Available)}
	case "withdraw":
		affected, cat := n.withdraw(o.a)
		return outcome{cat: cat, detail: strings.Join(affected, ",")}
	case "setGroups":
		return outcome{cat: n.setGroups(o.a, o.groups)}
	case "createP":
		return outcome{cat: n.createPaper(o.a, o.cons)}
	case "add":
		return outcome{cat: n.addToPaper(o.a, o.b)}
	case "remove":
		return outcome{cat: n.removeFromPaper(o.a, o.b)}
	case "validate":
		return outcome{cat: n.validateDraft(o.a)}
	case "publish":
		return outcome{cat: n.publish(o.a)}
	case "replace":
		return outcome{cat: n.replace(o.a, o.b, o.groups[0])}
	case "assemble":
		if cat := n.createPaper(o.a, o.cons); cat != 0 {
			return outcome{cat: cat}
		}
		for _, q := range o.groups {
			if cat := n.addToPaper(o.a, q); cat != 0 {
				return outcome{cat: cat}
			}
		}
		return outcome{cat: n.publish(o.a)}
	}
	panic("unknown op " + o.name)
}

// genAssemble 基于引擎当前状态构造一个大概率可成功发布的组卷宏操作，
// 使随机序列能走到发布成功、下架失效、替换恢复等深层路径。
func genAssemble(r *rand.Rand, e *Engine, qids []string, seq int) op {
	available := []string{}
	for _, q := range qids {
		life, _, _, ok := e.QuestionInfo(q)
		if ok && life == Available {
			available = append(available, q)
		}
	}
	r.Shuffle(len(available), func(i, j int) { available[i], available[j] = available[j], available[i] })
	chosen := []string{}
	target := 0
	for _, q := range available {
		if len(chosen) >= 3 {
			break
		}
		if conflict, _ := e.CheckConflict(q, chosen); conflict != "" {
			continue
		}
		_, versions, _, _ := e.QuestionInfo(q)
		chosen = append(chosen, q)
		target += versions[len(versions)-1].Score
	}
	return op{
		name:   "assemble",
		a:      "ap" + strconv.Itoa(seq),
		groups: chosen,
		cons:   Constraints{TargetScore: target},
	}
}

func genOp(r *rand.Rand, qids, pids, kps, groups []string) op {
	pick := func(xs []string) string { return xs[r.Intn(len(xs))] }
	subset := func(xs []string) []string {
		var out []string
		for _, x := range xs {
			if r.Intn(2) == 0 {
				out = append(out, x)
			}
		}
		return out
	}
	score := 1 + r.Intn(4)
	if r.Intn(20) == 0 {
		score = -1 // 偶发非法参数
	}
	diff := 1 + r.Intn(3)
	switch r.Intn(12) {
	case 0:
		return op{name: "createQ", a: pick(qids), score: score, diff: diff, kps: subset(kps), groups: subset(groups)}
	case 1:
		return op{name: "revise", a: pick(qids), score: score, diff: diff, kps: subset(kps)}
	case 2:
		return op{name: "suspend", a: pick(qids)}
	case 3:
		return op{name: "resume", a: pick(qids)}
	case 4:
		return op{name: "withdraw", a: pick(qids)}
	case 5:
		return op{name: "setGroups", a: pick(qids), groups: subset(groups)}
	case 6:
		cons := Constraints{TargetScore: 1 + r.Intn(8), Coverage: map[string]int{}, Difficulty: map[int]DifficultyRange{}}
		for _, kp := range kps {
			if r.Intn(3) == 0 {
				cons.Coverage[kp] = 1 + r.Intn(2)
			}
		}
		for lv := 1; lv <= 3; lv++ {
			if r.Intn(3) == 0 {
				lo := r.Intn(2)
				cons.Difficulty[lv] = DifficultyRange{Min: lo, Max: lo + r.Intn(3)}
			}
		}
		return op{name: "createP", a: pick(pids), cons: cons}
	case 7, 8:
		return op{name: "add", a: pick(pids), b: pick(qids)}
	case 9:
		return op{name: "remove", a: pick(pids), b: pick(qids)}
	case 10:
		if r.Intn(2) == 0 {
			return op{name: "validate", a: pick(pids)}
		}
		return op{name: "publish", a: pick(pids)}
	default:
		return op{name: "replace", a: pick(pids), b: pick(qids), groups: []string{pick(qids)}}
	}
}

// genSmartReplace 基于引擎当前状态构造一个大概率可行的替换操作，
// 覆盖替换成功与失效试卷经替换恢复的路径。
func genSmartReplace(r *rand.Rand, e *Engine, qids, pids []string, assembleSeq int) (op, bool) {
	ids := append([]string{}, pids...)
	for i := 0; i < assembleSeq; i++ {
		ids = append(ids, "ap"+strconv.Itoa(i))
	}
	r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	for _, pid := range ids {
		state, entries, _, ok := e.PaperInfo(pid)
		if !ok || state == Draft || len(entries) == 0 {
			continue
		}
		cands := append([]Entry{}, entries...)
		r.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
		for _, old := range cands {
			oldLife, _, _, _ := e.QuestionInfo(old.QuestionID)
			if state == Invalid && oldLife != Withdrawn {
				continue // 失效试卷只能以下架题为替换对象
			}
			rest := []string{}
			inPaper := map[string]bool{}
			for _, en := range entries {
				inPaper[en.QuestionID] = true
				if en.QuestionID != old.QuestionID {
					rest = append(rest, en.QuestionID)
				}
			}
			pool := append([]string{}, qids...)
			r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
			for _, q := range pool {
				if inPaper[q] {
					continue
				}
				life, versions, _, ok := e.QuestionInfo(q)
				if !ok || life != Available {
					continue
				}
				if versions[len(versions)-1].Score != old.Score {
					continue // 保持总分不变
				}
				if conflict, _ := e.CheckConflict(q, rest); conflict != "" {
					continue
				}
				return op{name: "replace", a: pid, b: old.QuestionID, groups: []string{q}}, true
			}
		}
	}
	return op{}, false
}

// TestAgainstNaiveModel 用随机操作序列对照 Engine 与朴素模型：
// 每步比较错误类别、操作明细与全量状态摘要，日志打印每步输入、输出与判定依据。
func TestAgainstNaiveModel(t *testing.T) {
	qids := []string{"q0", "q1", "q2", "q3", "q4", "q5", "q6", "q7", "ghost-q"}
	pids := []string{"p0", "p1", "p2", "ghost-p"}
	kps := []string{"K0", "K1", "K2"}
	groups := []string{"g0", "g1", "g2"}

	allPids := append([]string{}, pids...)
	for i := 0; i < 60; i++ {
		allPids = append(allPids, "ap"+strconv.Itoa(i))
	}

	for seed := int64(1); seed <= 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		e := NewEngine()
		n := newNaive()
		stats := map[string]int{}
		assembleSeq := 0
		for step := 0; step < 300; step++ {
			var o op
			switch {
			case r.Intn(6) == 0:
				o = genAssemble(r, e, qids, assembleSeq)
				assembleSeq++
			case r.Intn(4) == 0:
				if so, ok := genSmartReplace(r, e, qids, pids, assembleSeq); ok {
					o = so
				} else {
					o = genOp(r, qids, pids, kps, groups)
				}
			default:
				o = genOp(r, qids, pids, kps, groups)
			}
			gotE := runOnEngine(e, o)
			gotN := runOnNaive(n, o)
			stats[o.name+"/"+catLabel(gotE.cat)]++
			basis := "ok"
			if gotE.cat != 0 {
				basis = gotE.cat.String() + ": " + gotE.detail
			}
			t.Logf("seed=%d step=%03d op=%s | engine=%s naive=%s | basis=%s",
				seed, step, o, catLabel(gotE.cat), catLabel(gotN.cat), basis)
			if gotE.cat != gotN.cat {
				t.Fatalf("seed=%d step=%d op=%s: engine=%v naive=%v", seed, step, o, gotE.cat, gotN.cat)
			}
			if gotE.detail != gotN.detail && gotE.cat == 0 {
				t.Fatalf("seed=%d step=%d op=%s: detail engine=%q naive=%q", seed, step, o, gotE.detail, gotN.detail)
			}
			de, dn := digestEngine(e, qids, allPids), digestNaive(n, qids, allPids)
			if de != dn {
				t.Fatalf("seed=%d step=%d op=%s: state diverged\nengine:\n%s\nnaive:\n%s", seed, step, o, de, dn)
			}
		}
		t.Logf("seed=%d stats=%v", seed, stats)
	}
}

func catLabel(c Category) string {
	if c == 0 {
		return "ok"
	}
	return c.String()
}

func (n *naive) revise(id string, score, diff int, kps []string) (int, Category) {
	if !nParamsOK(id, score, diff) {
		return 0, CatInvalidParam
	}
	q, ok := n.qs[id]
	if !ok {
		return 0, CatNotFound
	}
	q.versions = append(q.versions, nVersion{score, diff, sortedCopy(kps)})
	return len(q.versions), 0
}

func (n *naive) transition(id string, to Lifecycle) Category {
	q, ok := n.qs[id]
	if !ok {
		return CatNotFound
	}
	if !canTransition(q.life, to) {
		return CatStateNotAllowed
	}
	q.life = to
	return 0
}

func (n *naive) withdraw(id string) ([]string, Category) {
	q, ok := n.qs[id]
	if !ok {
		return nil, CatNotFound
	}
	if !canTransition(q.life, Withdrawn) {
		return nil, CatStateNotAllowed
	}
	q.life = Withdrawn
	affected := []string{}
	for pid, p := range n.ps {
		if p.state != Published {
			continue
		}
		for _, en := range p.entries {
			if en.qid == id {
				p.state = Invalid
				affected = append(affected, pid)
				break
			}
		}
	}
	sort.Strings(affected)
	return affected, 0
}

func (n *naive) setGroups(id string, groups []string) Category {
	q, ok := n.qs[id]
	if !ok {
		return CatNotFound
	}
	gs := map[string]bool{}
	for _, g := range groups {
		if g != "" {
			gs[g] = true
		}
	}
	q.groups = gs
	return 0
}

// connected 朴素传递闭包：反复扫描全题库扩张可达集。
func (n *naive) connected(a, b string) bool {
	reached := map[string]bool{a: true}
	frontier := []string{a}
	for len(frontier) > 0 {
		var next []string
		for _, x := range frontier {
			for id, q := range n.qs {
				if reached[id] {
					continue
				}
				for g := range n.qs[x].groups {
					if q.groups[g] {
						reached[id] = true
						next = append(next, id)
						break
					}
				}
			}
		}
		frontier = next
	}
	return reached[b]
}

func (n *naive) createPaper(id string, c Constraints) Category {
	if id == "" || c.TargetScore <= 0 {
		return CatInvalidParam
	}
	for kp, cnt := range c.Coverage {
		if kp == "" || cnt < 1 {
			return CatInvalidParam
		}
	}
	for _, r := range c.Difficulty {
		if r.Min < 0 || r.Max < r.Min {
			return CatInvalidParam
		}
	}
	if _, dup := n.ps[id]; dup {
		return CatInvalidParam
	}
	n.ps[id] = &nPaper{state: Draft, cons: c}
	return 0
}

func (n *naive) addToPaper(pid, qid string) Category {
	if pid == "" || qid == "" {
		return CatInvalidParam
	}
	p, ok := n.ps[pid]
	if !ok {
		return CatNotFound
	}
	q, ok := n.qs[qid]
	if !ok {
		return CatNotFound
	}
	if p.state != Draft {
		return CatStateNotAllowed
	}
	if q.life != Available {
		return CatNotSelectable
	}
	for _, id := range p.draft {
		if id == qid {
			return CatInvalidParam
		}
	}
	p.draft = append(p.draft, qid)
	return 0
}

func (n *naive) removeFromPaper(pid, qid string) Category {
	if pid == "" || qid == "" {
		return CatInvalidParam
	}
	p, ok := n.ps[pid]
	if !ok {
		return CatNotFound
	}
	if _, ok := n.qs[qid]; !ok {
		return CatNotFound
	}
	if p.state != Draft {
		return CatStateNotAllowed
	}
	for i, id := range p.draft {
		if id == qid {
			p.draft = append(p.draft[:i], p.draft[i+1:]...)
			return 0
		}
	}
	return CatNotFound
}

func (n *naive) latestEntry(qid string) nEntry {
	q := n.qs[qid]
	v := q.versions[len(q.versions)-1]
	return nEntry{qid: qid, ver: len(q.versions), score: v.score, diff: v.diff, kps: sortedCopy(v.kps)}
}

// nValidate 按固定优先级校验条目集合：互斥 > 总分 > 覆盖 > 难度。
func (n *naive) nValidate(entries []nEntry, c Constraints) Category {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if n.connected(entries[i].qid, entries[j].qid) {
				return CatMutexConflict
			}
		}
	}
	total := 0
	for _, en := range entries {
		total += en.score
	}
	if total != c.TargetScore {
		return CatTotalScore
	}
	for kp, need := range c.Coverage {
		got := 0
		for _, en := range entries {
			if contains(en.kps, kp) {
				got++
			}
		}
		if got < need {
			return CatCoverage
		}
	}
	for level, r := range c.Difficulty {
		got := 0
		for _, en := range entries {
			if en.diff == level {
				got++
			}
		}
		if got < r.Min || got > r.Max {
			return CatDifficulty
		}
	}
	return 0
}

func (n *naive) draftCheck(p *nPaper) (Category, []nEntry) {
	entries := make([]nEntry, 0, len(p.draft))
	for _, qid := range p.draft {
		if n.qs[qid].life != Available {
			return CatNotSelectable, nil
		}
		entries = append(entries, n.latestEntry(qid))
	}
	return n.nValidate(entries, p.cons), entries
}

func (n *naive) validateDraft(pid string) Category {
	if pid == "" {
		return CatInvalidParam
	}
	p, ok := n.ps[pid]
	if !ok {
		return CatNotFound
	}
	if p.state != Draft {
		return CatStateNotAllowed
	}
	cat, _ := n.draftCheck(p)
	return cat
}

func (n *naive) publish(pid string) Category {
	if pid == "" {
		return CatInvalidParam
	}
	p, ok := n.ps[pid]
	if !ok {
		return CatNotFound
	}
	if p.state != Draft {
		return CatStateNotAllowed
	}
	cat, entries := n.draftCheck(p)
	if cat != 0 {
		return cat
	}
	p.entries = entries
	p.draft = nil
	p.state = Published
	return 0
}

func (n *naive) replace(pid, oldQ, newQ string) Category {
	if pid == "" || oldQ == "" || newQ == "" || oldQ == newQ {
		return CatInvalidParam
	}
	p, ok := n.ps[pid]
	if !ok {
		return CatNotFound
	}
	if _, ok := n.qs[oldQ]; !ok {
		return CatNotFound
	}
	if _, ok := n.qs[newQ]; !ok {
		return CatNotFound
	}
	if p.state == Draft {
		return CatStateNotAllowed
	}
	idx := -1
	for i, en := range p.entries {
		if en.qid == oldQ {
			idx = i
			break
		}
	}
	if idx < 0 {
		return CatNotFound
	}
	if p.state == Invalid && n.qs[oldQ].life != Withdrawn {
		return CatStateNotAllowed
	}
	for _, en := range p.entries {
		if en.qid == newQ {
			return CatInvalidParam
		}
	}
	if n.qs[newQ].life != Available {
		return CatNotSelectable
	}
	candidate := make([]nEntry, len(p.entries))
	copy(candidate, p.entries)
	candidate[idx] = n.latestEntry(newQ)
	if cat := n.nValidate(candidate, p.cons); cat != 0 {
		return cat
	}
	p.entries = candidate
	if p.state == Invalid {
		hasWithdrawn := false
		for _, en := range p.entries {
			if n.qs[en.qid].life == Withdrawn {
				hasWithdrawn = true
			}
		}
		if !hasWithdrawn {
			p.state = Published
		}
	}
	return 0
}
