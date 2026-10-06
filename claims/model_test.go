package claims

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 本文件包含一个按规格独立写成的朴素模型（线性扫描、切片队列、无索引堆），
// 用于与 Engine 做随机操作序列的差分对照。

type naiveSlot struct {
	assigned   bool
	assignee   string
	assignedAt int64
	conclusion Conclusion
	auto       bool
}

type naiveCase struct {
	id         string
	acceptTime int64
	branch     string
	score      int
	double     bool
	slots      []naiveSlot
	outcome    Outcome
}

type naive struct {
	cfg       Config
	now       int64
	rules     map[string]Rule
	cases     map[string]*naiveCase
	reviewers map[string]Reviewer
	pending   []string // 待分配案件号，选择时线性扫描最小 (受理时刻, 序号)
	seqOf     map[string]uint64
	seq       uint64
}

func newNaive(cfg Config) *naive {
	return &naive{
		cfg:       cfg,
		rules:     make(map[string]Rule),
		cases:     make(map[string]*naiveCase),
		reviewers: make(map[string]Reviewer),
		seqOf:     make(map[string]uint64),
	}
}

func (n *naive) setRule(id, feature string, score int) error {
	if id == "" || feature == "" {
		return ErrInvalidParam
	}
	n.rules[id] = Rule{ID: id, Feature: feature, Score: score}
	return nil
}

func (n *naive) removeRule(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	delete(n.rules, id)
	return nil
}

func (n *naive) registerReviewer(id, branch string, level Level) error {
	if id == "" || branch == "" || !level.valid() {
		return ErrInvalidParam
	}
	if _, dup := n.reviewers[id]; dup {
		return ErrInvalidParam
	}
	n.reviewers[id] = Reviewer{ID: id, Branch: branch, Level: level}
	return nil
}

func (n *naive) acceptCase(id string, at int64, features []string, branch string, amount int64) error {
	if id == "" || branch == "" || at < 0 || amount < 0 {
		return ErrInvalidParam
	}
	if _, dup := n.cases[id]; dup {
		return ErrInvalidParam
	}
	if at < n.now {
		return ErrClockRollback
	}
	n.now = at
	feat := make(map[string]bool, len(features))
	for _, f := range features {
		feat[f] = true
	}
	score := 0
	for _, r := range n.rules {
		if feat[r.Feature] {
			score += r.Score
		}
	}
	if score < 0 {
		score = 0
	}
	c := &naiveCase{id: id, acceptTime: at, branch: branch, score: score}
	switch {
	case score < n.cfg.LowThreshold:
		c.outcome = OutcomePass
	case score >= n.cfg.HighThreshold:
		c.double = true
		c.slots = make([]naiveSlot, 2)
		n.enqueue(id)
	default:
		c.slots = make([]naiveSlot, 1)
		n.enqueue(id)
	}
	n.cases[id] = c
	return nil
}

func (n *naive) advance(now int64) error {
	if now < n.now {
		return ErrClockRollback
	}
	n.now = now
	return nil
}

func (n *naive) enqueue(id string) {
	for _, p := range n.pending {
		if p == id {
			return
		}
	}
	n.seqOf[id] = n.seq
	n.seq++
	n.pending = append(n.pending, id)
}

func (n *naive) dequeue(id string) {
	for i, p := range n.pending {
		if p == id {
			n.pending = append(n.pending[:i], n.pending[i+1:]...)
			return
		}
	}
}

func (n *naive) sweep(c *naiveCase) {
	if c.outcome != OutcomeNone {
		return
	}
	for i := 0; i < len(c.slots); i++ {
		s := &c.slots[i]
		if s.assigned && s.conclusion == ConclusionNone && n.now >= s.assignedAt+n.cfg.ReviewTimeout {
			n.record(c, i, ConclusionPass, true)
		}
	}
}

func (n *naive) record(c *naiveCase, idx int, concl Conclusion, auto bool) {
	c.slots[idx].conclusion = concl
	c.slots[idx].auto = auto
	switch {
	case !c.double:
		c.outcome = outcomeOf(concl)
		n.dequeue(c.id)
	case idx == len(c.slots)-1 && len(c.slots) == 3:
		c.outcome = outcomeOf(concl)
		n.dequeue(c.id)
	default:
		a, b := c.slots[0], c.slots[1]
		if a.conclusion == ConclusionNone || b.conclusion == ConclusionNone {
			return
		}
		if a.conclusion == b.conclusion {
			c.outcome = outcomeOf(a.conclusion)
			n.dequeue(c.id)
			return
		}
		c.slots = append(c.slots, naiveSlot{})
		n.enqueue(c.id)
	}
}

func (n *naive) assign(caseID string, slotIdx int, reviewerID string) error {
	if caseID == "" || reviewerID == "" || slotIdx < 0 {
		return ErrInvalidParam
	}
	c, ok := n.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	r, ok := n.reviewers[reviewerID]
	if !ok {
		return ErrReviewerNotFound
	}
	n.sweep(c)
	if c.outcome != OutcomeNone {
		return ErrCaseFinal
	}
	if slotIdx >= len(c.slots) || (slotIdx > 0 && !c.slots[slotIdx-1].assigned) {
		return ErrInvalidParam
	}
	if r.Branch == c.branch {
		return ErrConflict
	}
	if c.slots[slotIdx].assigned {
		return ErrSlotOccupied
	}
	if err := naiveEligibility(c, slotIdx, r); err != nil {
		return err
	}
	n.assignSlot(c, slotIdx, reviewerID)
	return nil
}

func (n *naive) assignSlot(c *naiveCase, idx int, reviewerID string) {
	c.slots[idx].assigned = true
	c.slots[idx].assignee = reviewerID
	c.slots[idx].assignedAt = n.now
	for _, s := range c.slots {
		if !s.assigned {
			return
		}
	}
	n.dequeue(c.id)
}

func naiveEligibility(c *naiveCase, idx int, r Reviewer) error {
	if c.double && idx == 1 {
		if c.slots[0].assignee == r.ID {
			return ErrSameReviewer
		}
		if r.Level != LevelTwo {
			return ErrLevelMismatch
		}
	}
	if idx == 2 {
		if c.slots[0].assignee == r.ID || c.slots[1].assignee == r.ID {
			return ErrSameReviewer
		}
		if r.Level != LevelTwo {
			return ErrLevelMismatch
		}
	}
	return nil
}

func (n *naive) requestAssign(reviewerID string) (string, int, error) {
	if reviewerID == "" {
		return "", 0, ErrInvalidParam
	}
	r, ok := n.reviewers[reviewerID]
	if !ok {
		return "", 0, ErrReviewerNotFound
	}
	if len(n.pending) == 0 {
		return "", 0, ErrNoPending
	}
	front := n.pending[0]
	for _, id := range n.pending[1:] {
		c, cur := n.cases[id], n.cases[front]
		if c.acceptTime < cur.acceptTime ||
			(c.acceptTime == cur.acceptTime && n.seqOf[id] < n.seqOf[front]) {
			front = id
		}
	}
	c := n.cases[front]
	n.sweep(c)
	if r.Branch == c.branch {
		return "", 0, ErrConflict
	}
	idx := -1
	for i, s := range c.slots {
		if !s.assigned {
			idx = i
			break
		}
	}
	if idx < 0 {
		n.dequeue(c.id)
		return "", 0, ErrNoPending
	}
	if err := naiveEligibility(c, idx, r); err != nil {
		return "", 0, err
	}
	n.assignSlot(c, idx, reviewerID)
	return c.id, idx, nil
}

func (n *naive) submit(caseID string, slotIdx int, reviewerID string, concl Conclusion) error {
	if caseID == "" || reviewerID == "" || slotIdx < 0 || !concl.valid() {
		return ErrInvalidParam
	}
	c, ok := n.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if _, ok := n.reviewers[reviewerID]; !ok {
		return ErrReviewerNotFound
	}
	if c.outcome != OutcomeNone {
		return ErrCaseFinal
	}
	if slotIdx >= len(c.slots) {
		return ErrInvalidParam
	}
	s := &c.slots[slotIdx]
	switch {
	case s.assigned && s.conclusion == ConclusionNone && n.now >= s.assignedAt+n.cfg.ReviewTimeout:
		n.sweep(c)
		return ErrSlotTimeout
	case s.conclusion != ConclusionNone && s.auto:
		return ErrSlotTimeout
	case s.conclusion != ConclusionNone && s.assignee != reviewerID:
		return ErrNotAssignee
	case s.conclusion != ConclusionNone:
		return ErrSlotClosed
	case !s.assigned || s.assignee != reviewerID:
		return ErrNotAssignee
	}
	n.sweep(c)
	n.record(c, slotIdx, concl, false)
	return nil
}

func (n *naive) withdraw(caseID string) error {
	if caseID == "" {
		return ErrInvalidParam
	}
	c, ok := n.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	n.sweep(c)
	if c.outcome != OutcomeNone {
		return ErrCaseFinal
	}
	c.outcome = OutcomeWithdrawn
	n.dequeue(caseID)
	return nil
}

func (n *naive) snapshot(caseID string) (CaseSnapshot, error) {
	if caseID == "" {
		return CaseSnapshot{}, ErrInvalidParam
	}
	c, ok := n.cases[caseID]
	if !ok {
		return CaseSnapshot{}, ErrCaseNotFound
	}
	n.sweep(c)
	snap := CaseSnapshot{ID: c.id, Score: c.score, Outcome: c.outcome}
	for _, s := range c.slots {
		snap.Slots = append(snap.Slots, SlotSnapshot{
			Assigned:   s.assigned,
			Assignee:   s.assignee,
			Conclusion: s.conclusion,
			Auto:       s.auto,
		})
	}
	return snap, nil
}

// 以下为差分对照：随机生成受理、分配、提交、推进时刻与撤回的操作序列，
// 同时施加于 Engine 与朴素模型，逐步比对输出与全部案件快照。

type opKind int

const (
	opSetRule opKind = iota
	opRemoveRule
	opAccept
	opAssign
	opRequestAssign
	opSubmit
	opWithdraw
	opAdvance
)

type op struct {
	kind   opKind
	id     string
	second string
	slot   int
	at     int64
	amount int64
	score  int
	feats  []string
	concl  Conclusion
	level  Level
}

func (o op) String() string {
	switch o.kind {
	case opSetRule:
		return fmt.Sprintf("SetRule(%s,%s,%d)", o.id, o.second, o.score)
	case opRemoveRule:
		return fmt.Sprintf("RemoveRule(%s)", o.id)
	case opAccept:
		return fmt.Sprintf("AcceptCase(%s,at=%d,feats=%v,branch=%s,amount=%d)", o.id, o.at, o.feats, o.second, o.amount)
	case opAssign:
		return fmt.Sprintf("Assign(%s,slot=%d,%s)", o.id, o.slot, o.second)
	case opRequestAssign:
		return fmt.Sprintf("RequestAssign(%s)", o.id)
	case opSubmit:
		return fmt.Sprintf("Submit(%s,slot=%d,%s,concl=%d)", o.id, o.slot, o.second, o.concl)
	case opWithdraw:
		return fmt.Sprintf("Withdraw(%s)", o.id)
	case opAdvance:
		return fmt.Sprintf("Advance(%d)", o.at)
	}
	return "?"
}

func genOps(rng *rand.Rand, steps int, branches, features, reviewerIDs, ruleIDs []string) []op {
	ops := make([]op, 0, steps)
	var now int64
	caseSeq := 0
	for i := 0; i < steps; i++ {
		pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
		switch rng.Intn(12) {
		case 0:
			ops = append(ops, op{kind: opSetRule, id: pick(ruleIDs), second: pick(features), score: rng.Intn(11) - 4})
		case 1:
			ops = append(ops, op{kind: opRemoveRule, id: pick(ruleIDs)})
		case 2, 3, 4:
			now += rng.Int63n(5)
			var feats []string
			for _, f := range features {
				if rng.Intn(2) == 0 {
					feats = append(feats, f)
				}
			}
			ops = append(ops, op{kind: opAccept, id: fmt.Sprintf("C%d", caseSeq), at: now,
				feats: feats, second: pick(branches), amount: rng.Int63n(1000)})
			caseSeq++
		case 5, 6:
			ops = append(ops, op{kind: opAssign, id: fmt.Sprintf("C%d", rng.Intn(caseSeq+1)),
				slot: rng.Intn(3), second: pick(reviewerIDs)})
		case 7, 8:
			ops = append(ops, op{kind: opRequestAssign, id: pick(reviewerIDs)})
		case 9:
			ops = append(ops, op{kind: opSubmit, id: fmt.Sprintf("C%d", rng.Intn(caseSeq+1)),
				slot: rng.Intn(3), second: pick(reviewerIDs), concl: Conclusion(1 + rng.Intn(2))})
		case 10:
			ops = append(ops, op{kind: opWithdraw, id: fmt.Sprintf("C%d", rng.Intn(caseSeq+1))})
		case 11:
			now += rng.Int63n(130)
			ops = append(ops, op{kind: opAdvance, at: now})
		}
	}
	return ops
}

// applyOnEngine 在 Engine 上执行一个操作，归一化输出为 (案件号, 空位, 错误)。
func applyOnEngine(e *Engine, o op) (string, int, error) {
	switch o.kind {
	case opSetRule:
		return "", 0, e.SetRule(o.id, o.second, o.score)
	case opRemoveRule:
		return "", 0, e.RemoveRule(o.id)
	case opAccept:
		return "", 0, e.AcceptCase(o.id, o.at, o.feats, o.second, o.amount)
	case opAssign:
		return "", 0, e.Assign(o.id, o.slot, o.second)
	case opRequestAssign:
		return e.RequestAssign(o.id)
	case opSubmit:
		return "", 0, e.Submit(o.id, o.slot, o.second, o.concl)
	case opWithdraw:
		return "", 0, e.Withdraw(o.id)
	case opAdvance:
		return "", 0, e.Advance(o.at)
	}
	return "", 0, nil
}

// applyOnNaive 在朴素模型上执行同一操作。
func applyOnNaive(n *naive, o op) (string, int, error) {
	switch o.kind {
	case opSetRule:
		return "", 0, n.setRule(o.id, o.second, o.score)
	case opRemoveRule:
		return "", 0, n.removeRule(o.id)
	case opAccept:
		return "", 0, n.acceptCase(o.id, o.at, o.feats, o.second, o.amount)
	case opAssign:
		return "", 0, n.assign(o.id, o.slot, o.second)
	case opRequestAssign:
		return n.requestAssign(o.id)
	case opSubmit:
		return "", 0, n.submit(o.id, o.slot, o.second, o.concl)
	case opWithdraw:
		return "", 0, n.withdraw(o.id)
	case opAdvance:
		return "", 0, n.advance(o.at)
	}
	return "", 0, nil
}

func compareStates(t *testing.T, e *Engine, n *naive, caseIDs []string) {
	t.Helper()
	if e.Now() != n.now {
		t.Fatalf("now: engine=%d naive=%d", e.Now(), n.now)
	}
	if e.PendingLen() != len(n.pending) {
		t.Fatalf("pending: engine=%d naive=%d", e.PendingLen(), len(n.pending))
	}
	for _, id := range caseIDs {
		es, eerr := e.Snapshot(id)
		ns, nerr := n.snapshot(id)
		if (eerr == nil) != (nerr == nil) {
			t.Fatalf("snapshot err %s: engine=%v naive=%v", id, eerr, nerr)
		}
		if eerr != nil {
			continue
		}
		if !reflect.DeepEqual(es, ns) {
			t.Fatalf("snapshot %s:\nengine=%+v\nnaive =%+v", id, es, ns)
		}
	}
}

func TestDifferentialRandomOps(t *testing.T) {
	cfg := Config{LowThreshold: 2, HighThreshold: 5, ReviewTimeout: 100}
	branches := []string{"b0", "b1", "b2"}
	features := []string{"F0", "F1", "F2", "F3"}
	reviewerIDs := []string{"u0", "u1", "u2", "u3", "u4", "u5"}
	ruleIDs := []string{"R0", "R1", "R2"}

	for seed := int64(1); seed <= 12; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			ops := genOps(rng, 600, branches, features, reviewerIDs, ruleIDs)
			e, err := NewEngine(cfg)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			n := newNaive(cfg)
			// 固定复核员池：覆盖两个级别与全部网点（含利益冲突场景）。
			for i, id := range reviewerIDs {
				lvl := LevelOne
				if i%2 == 1 {
					lvl = LevelTwo
				}
				if err := e.RegisterReviewer(id, branches[i%len(branches)], lvl); err != nil {
					t.Fatalf("RegisterReviewer: %v", err)
				}
				if err := n.registerReviewer(id, branches[i%len(branches)], lvl); err != nil {
					t.Fatalf("naive register: %v", err)
				}
			}
			var caseIDs []string
			for i, o := range ops {
				eid, eslot, eerr := applyOnEngine(e, o)
				nid, nslot, nerr := applyOnNaive(n, o)
				if o.kind == opAccept {
					caseIDs = append(caseIDs, o.id)
				}
				basis := ""
				if o.kind == opAccept || o.kind == opAssign || o.kind == opSubmit || o.kind == opWithdraw {
					if s, err := e.Snapshot(o.id); err == nil {
						basis = fmt.Sprintf(" | 判定依据: score=%d outcome=%d slots=%v", s.Score, s.Outcome, s.Slots)
					}
				}
				t.Logf("op %d: %s -> engine(%s,%d,%v) naive(%s,%d,%v)%s",
					i, o, eid, eslot, eerr, nid, nslot, nerr, basis)
				if eerr != nerr || eid != nid || eslot != nslot {
					t.Fatalf("op %d %s: engine(%s,%d,%v) != naive(%s,%d,%v)",
						i, o, eid, eslot, eerr, nid, nslot, nerr)
				}
				compareStates(t, e, n, caseIDs)
			}
		})
	}
}

// 相同操作序列重放得到完全相同的分流、分配与结论。
func TestReplayDeterminism(t *testing.T) {
	cfg := Config{LowThreshold: 2, HighThreshold: 5, ReviewTimeout: 100}
	branches := []string{"b0", "b1"}
	features := []string{"F0", "F1"}
	reviewerIDs := []string{"u0", "u1", "u2"}
	ruleIDs := []string{"R0"}
	rng := rand.New(rand.NewSource(42))
	ops := genOps(rng, 400, branches, features, reviewerIDs, ruleIDs)

	run := func() map[string]CaseSnapshot {
		e, _ := NewEngine(cfg)
		for i, id := range reviewerIDs {
			lvl := LevelOne
			if i%2 == 1 {
				lvl = LevelTwo
			}
			_ = e.RegisterReviewer(id, branches[i%len(branches)], lvl)
		}
		out := make(map[string]CaseSnapshot)
		for _, o := range ops {
			applyOnEngine(e, o)
			if o.kind == opAccept {
				out[o.id], _ = e.Snapshot(o.id)
			}
		}
		for _, o := range ops {
			if o.kind == opAccept {
				out[o.id], _ = e.Snapshot(o.id)
			}
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch")
	}
}
