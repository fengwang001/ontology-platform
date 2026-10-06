package exam

// This file contains an independent, deliberately naive implementation of
// the same rules (the "model") and a randomized differential test that
// replays identical operation sequences against the Engine and the model,
// comparing every outcome and the full state digest after each step.
// The model recomputes everything from scratch (e.g. mutex reachability
// via BFS over the raw question/group graph) and shares no code with the
// engine beyond the public types.

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type mVer struct {
	score, diff int
	know        map[string]bool
}

type mQ struct {
	life Lifecycle
	vers []mVer
}

type mItem struct {
	ver         int // 0 while the paper is a draft
	score, diff int
	know        map[string]bool
}

type mPaper struct {
	state  PaperState
	target int
	kreq   map[string]int
	dreq   map[int]DifficultyRange
	items  map[string]mItem
}

type model struct {
	qs     map[string]*mQ
	ps     map[string]*mPaper
	groups map[string]map[string]bool
	qg     map[string]map[string]bool
}

func newModel() *model {
	return &model{
		qs:     map[string]*mQ{},
		ps:     map[string]*mPaper{},
		groups: map[string]map[string]bool{},
		qg:     map[string]map[string]bool{},
	}
}

func mSet(ks []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range ks {
		out[k] = true
	}
	return out
}

// connected is the naive mutex check: BFS over the raw bipartite graph.
func (m *model) connected(a, b string) bool {
	seen := map[string]bool{a: true}
	queue := []string{a}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for g := range m.qg[x] {
			for y := range m.groups[g] {
				if !seen[y] {
					seen[y] = true
					queue = append(queue, y)
				}
			}
		}
	}
	return seen[b]
}

func (m *model) attr(p *mPaper, id string) mVer {
	if p.state == Draft {
		return m.qs[id].vers[len(m.qs[id].vers)-1]
	}
	it := p.items[id]
	return mVer{score: it.score, diff: it.diff, know: it.know}
}

func sortedKeysOf[V any](mp map[string]V) []string {
	out := make([]string, 0, len(mp))
	for k := range mp {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *model) addQuestion(id string, score, diff int, know []string) Category {
	if id == "" || score < 0 || diff < 0 {
		return ErrInvalidParam
	}
	if _, ok := m.qs[id]; ok {
		return ErrInvalidParam
	}
	m.qs[id] = &mQ{life: Available, vers: []mVer{{score: score, diff: diff, know: mSet(know)}}}
	return 0
}

func (m *model) revise(id string, score, diff int, know []string) Category {
	if id == "" || score < 0 || diff < 0 {
		return ErrInvalidParam
	}
	q, ok := m.qs[id]
	if !ok {
		return ErrNotFound
	}
	q.vers = append(q.vers, mVer{score: score, diff: diff, know: mSet(know)})
	return 0
}

func (m *model) suspend(id string) Category {
	q, ok := m.qs[id]
	if !ok {
		return ErrNotFound
	}
	if q.life != Available {
		return ErrInvalidState
	}
	q.life = Suspended
	return 0
}

func (m *model) resume(id string) Category {
	q, ok := m.qs[id]
	if !ok {
		return ErrNotFound
	}
	if q.life != Suspended {
		return ErrInvalidState
	}
	q.life = Available
	return 0
}

func (m *model) retire(id string) (Category, []string) {
	q, ok := m.qs[id]
	if !ok {
		return ErrNotFound, nil
	}
	if q.life == Retired {
		return ErrInvalidState, nil
	}
	q.life = Retired
	var affected []string
	for pid, p := range m.ps {
		if p.state != Published {
			continue
		}
		if _, ok := p.items[id]; ok {
			p.state = Invalid
			affected = append(affected, pid)
		}
	}
	sort.Strings(affected)
	return 0, affected
}

func (m *model) addGroup(q, g string) Category {
	if q == "" || g == "" {
		return ErrInvalidParam
	}
	if _, ok := m.qs[q]; !ok {
		return ErrNotFound
	}
	if m.groups[g][q] {
		return ErrInvalidParam
	}
	if m.groups[g] == nil {
		m.groups[g] = map[string]bool{}
	}
	m.groups[g][q] = true
	if m.qg[q] == nil {
		m.qg[q] = map[string]bool{}
	}
	m.qg[q][g] = true
	return 0
}

func (m *model) removeGroup(q, g string) Category {
	if q == "" || g == "" {
		return ErrInvalidParam
	}
	if _, ok := m.qs[q]; !ok {
		return ErrNotFound
	}
	if !m.groups[g][q] {
		return ErrInvalidParam
	}
	delete(m.groups[g], q)
	if len(m.groups[g]) == 0 {
		delete(m.groups, g)
	}
	delete(m.qg[q], g)
	if len(m.qg[q]) == 0 {
		delete(m.qg, q)
	}
	return 0
}

func (m *model) createPaper(id string, target int, kreq map[string]int, dreq map[int]DifficultyRange) Category {
	if id == "" || target < 0 {
		return ErrInvalidParam
	}
	for kp, n := range kreq {
		if kp == "" || n <= 0 {
			return ErrInvalidParam
		}
	}
	for lvl, r := range dreq {
		if lvl < 0 || r.Min < 0 || r.Max < r.Min {
			return ErrInvalidParam
		}
	}
	if _, ok := m.ps[id]; ok {
		return ErrInvalidParam
	}
	k := map[string]int{}
	for kp, n := range kreq {
		k[kp] = n
	}
	d := map[int]DifficultyRange{}
	for lvl, r := range dreq {
		d[lvl] = r
	}
	m.ps[id] = &mPaper{state: Draft, target: target, kreq: k, dreq: d, items: map[string]mItem{}}
	return 0
}

func (m *model) draftAdd(pid, qid string) Category {
	if pid == "" || qid == "" {
		return ErrInvalidParam
	}
	p, ok := m.ps[pid]
	if !ok {
		return ErrNotFound
	}
	q, ok := m.qs[qid]
	if !ok {
		return ErrNotFound
	}
	if _, dup := p.items[qid]; dup {
		return ErrInvalidParam
	}
	if p.state != Draft {
		return ErrInvalidState
	}
	if q.life != Available {
		return ErrNotSelectable
	}
	for id := range p.items {
		if m.connected(qid, id) {
			return ErrMutexConflict
		}
	}
	p.items[qid] = mItem{}
	return 0
}

func (m *model) draftRemove(pid, qid string) Category {
	if pid == "" || qid == "" {
		return ErrInvalidParam
	}
	p, ok := m.ps[pid]
	if !ok {
		return ErrNotFound
	}
	if _, ok := p.items[qid]; !ok {
		return ErrNotFound
	}
	if p.state != Draft {
		return ErrInvalidState
	}
	delete(p.items, qid)
	return 0
}

func (m *model) validate(p *mPaper, ids []string, requireAvailable bool) Category {
	if requireAvailable {
		for _, id := range ids {
			if m.qs[id].life != Available {
				return ErrNotSelectable
			}
		}
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if m.connected(ids[i], ids[j]) {
				return ErrMutexConflict
			}
		}
	}
	total := 0
	for _, id := range ids {
		total += m.attr(p, id).score
	}
	if total != p.target {
		return ErrTotalScore
	}
	for kp, need := range p.kreq {
		cnt := 0
		for _, id := range ids {
			if m.attr(p, id).know[kp] {
				cnt++
			}
		}
		if cnt < need {
			return ErrKnowledgeCoverage
		}
	}
	for lvl, r := range p.dreq {
		cnt := 0
		for _, id := range ids {
			if m.attr(p, id).diff == lvl {
				cnt++
			}
		}
		if cnt < r.Min || cnt > r.Max {
			return ErrDifficulty
		}
	}
	return 0
}

func (m *model) validatePaper(pid string) Category {
	p, ok := m.ps[pid]
	if !ok {
		return ErrNotFound
	}
	if p.state != Draft {
		return ErrInvalidState
	}
	return m.validate(p, sortedKeysOf(p.items), true)
}

func (m *model) publish(pid string) Category {
	p, ok := m.ps[pid]
	if !ok {
		return ErrNotFound
	}
	if p.state != Draft {
		return ErrInvalidState
	}
	ids := sortedKeysOf(p.items)
	if cat := m.validate(p, ids, true); cat != 0 {
		return cat
	}
	for _, id := range ids {
		v := m.qs[id].vers[len(m.qs[id].vers)-1]
		p.items[id] = mItem{ver: len(m.qs[id].vers), score: v.score, diff: v.diff, know: mSet(nil)}
		it := p.items[id]
		for k := range v.know {
			it.know[k] = true
		}
		p.items[id] = it
	}
	p.state = Published
	return 0
}

func (m *model) replace(pid, oldID, newID string) Category {
	if pid == "" || oldID == "" || newID == "" || oldID == newID {
		return ErrInvalidParam
	}
	p, ok := m.ps[pid]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.qs[oldID]; !ok {
		return ErrNotFound
	}
	nq, ok := m.qs[newID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := p.items[oldID]; !ok {
		return ErrNotFound
	}
	if _, dup := p.items[newID]; dup {
		return ErrInvalidParam
	}
	if p.state == Draft {
		return ErrInvalidState
	}
	if p.state == Invalid && m.qs[oldID].life != Retired {
		return ErrInvalidState
	}
	if nq.life != Available {
		return ErrNotSelectable
	}
	var remaining []string
	for id := range p.items {
		if id != oldID {
			remaining = append(remaining, id)
		}
	}
	for _, id := range remaining {
		if m.connected(newID, id) {
			return ErrMutexConflict
		}
	}
	nv := nq.vers[len(nq.vers)-1]
	total := nv.score
	for _, id := range remaining {
		total += m.attr(p, id).score
	}
	if total != p.target {
		return ErrTotalScore
	}
	covers := func(kp string) int {
		cnt := 0
		if nv.know[kp] {
			cnt++
		}
		for _, id := range remaining {
			if m.attr(p, id).know[kp] {
				cnt++
			}
		}
		return cnt
	}
	for kp, need := range p.kreq {
		if covers(kp) < need {
			return ErrKnowledgeCoverage
		}
	}
	atLevel := func(lvl int) int {
		cnt := 0
		if nv.diff == lvl {
			cnt++
		}
		for _, id := range remaining {
			if m.attr(p, id).diff == lvl {
				cnt++
			}
		}
		return cnt
	}
	for lvl, r := range p.dreq {
		if n := atLevel(lvl); n < r.Min || n > r.Max {
			return ErrDifficulty
		}
	}
	delete(p.items, oldID)
	know := map[string]bool{}
	for k := range nv.know {
		know[k] = true
	}
	p.items[newID] = mItem{ver: len(nq.vers), score: nv.score, diff: nv.diff, know: know}
	if p.state == Invalid {
		stillRetired := false
		for id := range p.items {
			if m.qs[id].life == Retired {
				stillRetired = true
			}
		}
		if !stillRetired {
			p.state = Published
		}
	}
	return 0
}

func (m *model) digest() string {
	var b strings.Builder
	for _, id := range sortedKeysOf(m.qs) {
		q := m.qs[id]
		fmt.Fprintf(&b, "Q %s %s", id, q.life)
		for i, v := range q.vers {
			fmt.Fprintf(&b, " v%d:%d:%d:%s", i+1, v.score, v.diff, strings.Join(sortedKeysOf(v.know), ","))
		}
		b.WriteByte('\n')
	}
	for _, id := range sortedKeysOf(m.ps) {
		p := m.ps[id]
		fmt.Fprintf(&b, "P %s %s target=%d", id, p.state, p.target)
		for _, qid := range sortedKeysOf(p.items) {
			it := p.items[qid]
			fmt.Fprintf(&b, " %s@v%d:%d:%d:%s", qid, it.ver, it.score, it.diff,
				strings.Join(sortedKeysOf(it.know), ","))
		}
		b.WriteByte('\n')
	}
	for _, g := range sortedKeysOf(m.groups) {
		fmt.Fprintf(&b, "G %s %s\n", g, strings.Join(sortedKeysOf(m.groups[g]), ","))
	}
	return b.String()
}

// --- randomized differential test ---------------------------------------

type rndOp struct {
	kind   string
	q, p   string
	g      string
	score  int
	diff   int
	know   []string
	target int
	kreq   map[string]int
	dreq   map[int]DifficultyRange
}

func (o rndOp) String() string {
	switch o.kind {
	case "addQuestion", "revise":
		return fmt.Sprintf("%s(%s score=%d diff=%d know=%v)", o.kind, o.q, o.score, o.diff, o.know)
	case "createPaper":
		return fmt.Sprintf("createPaper(%s target=%d kreq=%v dreq=%v)", o.p, o.target, o.kreq, o.dreq)
	case "draftAdd", "draftRemove", "replace":
		return fmt.Sprintf("%s(%s %s->%s)", o.kind, o.p, o.q, o.g)
	case "addGroup", "removeGroup":
		return fmt.Sprintf("%s(%s %s)", o.kind, o.q, o.g)
	default:
		return fmt.Sprintf("%s(%s%s)", o.kind, o.q, o.p)
	}
}

// genOp picks random inputs, biased by the engine's current state so that
// deep paths (publish success, retire invalidation, invalid-paper repair)
// are exercised. It only reads state to choose inputs, never outputs.
func genOp(rng *rand.Rand, e *Engine) rndOp {
	q := fmt.Sprintf("q%d", rng.Intn(14))
	p := fmt.Sprintf("p%d", rng.Intn(5))
	g := fmt.Sprintf("g%d", rng.Intn(6))
	know := []string{}
	for i := 0; i < rng.Intn(3); i++ {
		know = append(know, fmt.Sprintf("k%d", rng.Intn(4)))
	}
	score := 5 * rng.Intn(4) // 0,5,10,15 keeps totals reachable
	if rng.Intn(100) < 3 {
		score = -1 // exercises invalid-param
	}
	diff := rng.Intn(4) - 1 // -1 exercises invalid-param
	switch n := rng.Intn(100); {
	case n < 10:
		return rndOp{kind: "addQuestion", q: q, score: score, diff: diff, know: know}
	case n < 20:
		return rndOp{kind: "revise", q: q, score: score, diff: diff, know: know}
	case n < 25:
		return rndOp{kind: "suspend", q: q}
	case n < 30:
		return rndOp{kind: "resume", q: q}
	case n < 35:
		if rng.Intn(100) < 60 {
			// Bias: retire a question that sits in a published paper.
			var cand []string
			for i := 0; i < 5; i++ {
				pid := fmt.Sprintf("p%d", i)
				if st, _ := e.PaperStateOf(pid); st == Published {
					for qid := range e.PaperItems(pid) {
						cand = append(cand, qid)
					}
				}
			}
			if len(cand) > 0 {
				q = cand[rng.Intn(len(cand))]
			}
		}
		return rndOp{kind: "retire", q: q}
	case n < 45:
		return rndOp{kind: "addGroup", q: q, g: g}
	case n < 50:
		return rndOp{kind: "removeGroup", q: q, g: g}
	case n < 58:
		kreq := map[string]int{}
		if rng.Intn(100) < 50 {
			for i := 0; i < 3; i++ {
				if rng.Intn(100) < 30 {
					kreq[fmt.Sprintf("k%d", i)] = 1 + rng.Intn(2)
				}
			}
		}
		dreq := map[int]DifficultyRange{}
		if rng.Intn(100) < 50 {
			for lvl := 0; lvl < 3; lvl++ {
				if rng.Intn(100) < 30 {
					min := rng.Intn(2)
					max := min + rng.Intn(3)
					if rng.Intn(100) < 5 {
						max = min - 1 // invalid range
					}
					dreq[lvl] = DifficultyRange{Min: min, Max: max}
				}
			}
		}
		return rndOp{kind: "createPaper", p: p, target: 5 * rng.Intn(7), kreq: kreq, dreq: dreq}
	case n < 71:
		return rndOp{kind: "draftAdd", p: p, q: q}
	case n < 77:
		return rndOp{kind: "draftRemove", p: p, q: q}
	case n < 81:
		return rndOp{kind: "validate", p: p}
	case n < 87:
		return rndOp{kind: "publish", p: p}
	default:
		if rng.Intn(100) < 60 {
			// Bias: replace inside an existing published/invalid paper;
			// for invalid papers target a retired question.
			var pids []string
			for i := 0; i < 5; i++ {
				pid := fmt.Sprintf("p%d", i)
				if st, _ := e.PaperStateOf(pid); st != Draft {
					pids = append(pids, pid)
				}
			}
			if len(pids) > 0 {
				p = pids[rng.Intn(len(pids))]
				st, _ := e.PaperStateOf(p)
				var olds []string
				for qid := range e.PaperItems(p) {
					if st != Invalid || e.LifecycleOf(qid) == Retired {
						olds = append(olds, qid)
					}
				}
				if len(olds) > 0 {
					return rndOp{kind: "replace", p: p, q: olds[rng.Intn(len(olds))],
						g: fmt.Sprintf("q%d", rng.Intn(14))}
				}
			}
		}
		return rndOp{kind: "replace", p: p, q: q, g: fmt.Sprintf("q%d", rng.Intn(14))}
	}
}

func catOf(err error) Category {
	if err == nil {
		return 0
	}
	var ee *Error
	if errors.As(err, &ee) {
		return ee.Cat
	}
	return -1
}

func applyEngine(e *Engine, op rndOp) (Category, []string, string) {
	var err error
	var affected []string
	switch op.kind {
	case "addQuestion":
		err = e.AddQuestion(op.q, op.score, op.diff, op.know)
	case "revise":
		_, err = e.ReviseQuestion(op.q, op.score, op.diff, op.know)
	case "suspend":
		err = e.SuspendQuestion(op.q)
	case "resume":
		err = e.ResumeQuestion(op.q)
	case "retire":
		affected, err = e.RetireQuestion(op.q)
	case "addGroup":
		err = e.AddToGroup(op.q, op.g)
	case "removeGroup":
		err = e.RemoveFromGroup(op.q, op.g)
	case "createPaper":
		err = e.CreatePaper(op.p, op.target, op.kreq, op.dreq)
	case "draftAdd":
		err = e.DraftAdd(op.p, op.q)
	case "draftRemove":
		err = e.DraftRemove(op.p, op.q)
	case "validate":
		err = e.ValidatePaper(op.p)
	case "publish":
		err = e.Publish(op.p)
	case "replace":
		err = e.Replace(op.p, op.q, op.g)
	}
	if err != nil {
		return catOf(err), affected, err.(*Error).Msg
	}
	return 0, affected, "ok"
}

func applyModel(m *model, op rndOp) (Category, []string) {
	switch op.kind {
	case "addQuestion":
		return m.addQuestion(op.q, op.score, op.diff, op.know), nil
	case "revise":
		return m.revise(op.q, op.score, op.diff, op.know), nil
	case "suspend":
		return m.suspend(op.q), nil
	case "resume":
		return m.resume(op.q), nil
	case "retire":
		return m.retire(op.q)
	case "addGroup":
		return m.addGroup(op.q, op.g), nil
	case "removeGroup":
		return m.removeGroup(op.q, op.g), nil
	case "createPaper":
		return m.createPaper(op.p, op.target, op.kreq, op.dreq), nil
	case "draftAdd":
		return m.draftAdd(op.p, op.q), nil
	case "draftRemove":
		return m.draftRemove(op.p, op.q), nil
	case "validate":
		return m.validatePaper(op.p), nil
	case "publish":
		return m.publish(op.p), nil
	case "replace":
		return m.replace(op.p, op.q, op.g), nil
	}
	return -1, nil
}

func catName(c Category) string {
	if c == 0 {
		return "ok"
	}
	return c.String()
}

// scriptedSetup builds a deterministic prefix that ends with two published
// papers, so the random phase has live papers to invalidate and repair.
func scriptedSetup() []rndOp {
	var ops []rndOp
	for i := 0; i < 8; i++ {
		ops = append(ops, rndOp{kind: "addQuestion", q: fmt.Sprintf("q%d", i),
			score: 5 * (1 + i%2), diff: i % 3,
			know: []string{fmt.Sprintf("k%d", i%3)}})
	}
	return append(ops,
		rndOp{kind: "createPaper", p: "p0", target: 20, kreq: map[string]int{}, dreq: map[int]DifficultyRange{}},
		rndOp{kind: "draftAdd", p: "p0", q: "q0"},
		rndOp{kind: "draftAdd", p: "p0", q: "q2"},
		rndOp{kind: "draftAdd", p: "p0", q: "q1"},
		rndOp{kind: "publish", p: "p0"},
		rndOp{kind: "createPaper", p: "p1", target: 20, kreq: map[string]int{}, dreq: map[int]DifficultyRange{}},
		rndOp{kind: "draftAdd", p: "p1", q: "q3"},
		rndOp{kind: "draftAdd", p: "p1", q: "q5"},
		rndOp{kind: "publish", p: "p1"},
	)
}

// TestDifferentialRandom replays identical random operation sequences
// against the engine and the independent naive model, logging every step's
// input, output and decision basis, and comparing outcomes plus the full
// state digest after each step.
func TestDifferentialRandom(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			eng := NewEngine()
			mod := newModel()
			setup := scriptedSetup()
			for i := 0; i < len(setup)+300; i++ {
				op := rndOp{}
				if i < len(setup) {
					op = setup[i]
				} else {
					op = genOp(rng, eng)
				}
				ec, eAff, reason := applyEngine(eng, op)
				mc, mAff := applyModel(mod, op)
				t.Logf("step %03d in=%-58s engine=%-18s model=%-18s affected=%v basis=%s",
					i, op, catName(ec), catName(mc), eAff, reason)
				if ec != mc {
					t.Fatalf("step %d %s: engine=%s model=%s\nengine digest:\n%s\nmodel digest:\n%s",
						i, op, catName(ec), catName(mc), eng.Digest(), mod.digest())
				}
				if fmt.Sprint(eAff) != fmt.Sprint(mAff) {
					t.Fatalf("step %d %s: affected engine=%v model=%v", i, op, eAff, mAff)
				}
				if d1, d2 := eng.Digest(), mod.digest(); d1 != d2 {
					t.Fatalf("step %d %s: digest diverged\nengine:\n%s\nmodel:\n%s", i, op, d1, d2)
				}
			}
		})
	}
}
