package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 本文件包含一个按规格独立写成的朴素模型：不用堆、全部线性扫描，
// 用于与 Engine 做随机操作序列的差分对照。

type mSlot struct {
	reviewerID string
	assigned   bool
	done       bool
	auto       bool
	void       bool
	verdict    Verdict
	assignTime int64
}

type mCase struct {
	id         string
	acceptTime int64
	seq        int64
	branch     string
	score      int
	state      CaseState
	slots      []*mSlot
}

type mReviewer struct {
	branch string
	level  Level
}

type model struct {
	low, high int
	timeout   int64
	now, seq  int64
	rules     map[string]Rule
	reviewers map[string]mReviewer
	cases     map[string]*mCase
	order     []*mCase
}

func newModel(low, high int, timeout int64) *model {
	return &model{
		low: low, high: high, timeout: timeout,
		rules:     make(map[string]Rule),
		reviewers: make(map[string]mReviewer),
		cases:     make(map[string]*mCase),
	}
}

func (m *model) upsertRule(r Rule) error {
	if r.ID == "" || r.Code == "" {
		return fmt.Errorf("%w: 规则编号与特征编码不能为空", ErrInvalidParam)
	}
	m.rules[r.ID] = r
	return nil
}

func (m *model) deleteRule(id string) error {
	if id == "" {
		return fmt.Errorf("%w: 规则编号不能为空", ErrInvalidParam)
	}
	if _, ok := m.rules[id]; !ok {
		return fmt.Errorf("%w: 规则不存在 %q", ErrInvalidParam, id)
	}
	delete(m.rules, id)
	return nil
}

func (m *model) registerReviewer(id, branch string, level Level) error {
	if id == "" || (level != LevelOne && level != LevelTwo) {
		return fmt.Errorf("%w: 复核员编号为空或级别非法", ErrInvalidParam)
	}
	if _, ok := m.reviewers[id]; ok {
		return fmt.Errorf("%w: 复核员重复 %q", ErrInvalidParam, id)
	}
	m.reviewers[id] = mReviewer{branch: branch, level: level}
	return nil
}

func (m *model) accept(id string, at int64, features []string, branch string, amount int64) error {
	if id == "" || at < 0 || amount < 0 {
		return fmt.Errorf("%w: 案件号为空或时刻/金额为负", ErrInvalidParam)
	}
	if _, ok := m.cases[id]; ok {
		return fmt.Errorf("%w: 案件号重复 %q", ErrInvalidParam, id)
	}
	feat := make(map[string]bool, len(features))
	for _, f := range features {
		feat[f] = true
	}
	score := 0
	for _, r := range m.rules {
		if feat[r.Code] {
			score += r.Score
		}
	}
	if score < 0 {
		score = 0
	}
	m.seq++
	c := &mCase{id: id, acceptTime: at, seq: m.seq, branch: branch, score: score}
	switch {
	case score < m.low:
		c.state = StateAutoPassed
	case score >= m.high:
		c.state = StateWaiting
		c.slots = []*mSlot{{}, {}}
	default:
		c.state = StateWaiting
		c.slots = []*mSlot{{}}
	}
	m.cases[id] = c
	m.order = append(m.order, c)
	return nil
}

func (m *model) materialize(c *mCase) {
	if c.state.Terminal() {
		return
	}
	filled := false
	for _, s := range c.slots {
		if s.assigned && !s.done && m.now >= s.assignTime+m.timeout {
			s.done = true
			s.auto = true
			s.verdict = VerdictPass
			filled = true
		}
	}
	if filled {
		m.resolve(c)
	}
}

func (m *model) resolve(c *mCase) {
	if c.state.Terminal() {
		return
	}
	finish := func(v Verdict) {
		if v == VerdictReject {
			c.state = StateRejected
		} else {
			c.state = StatePassed
		}
	}
	switch len(c.slots) {
	case 1:
		if c.slots[0].done {
			finish(c.slots[0].verdict)
		}
	case 2:
		if c.slots[0].done && c.slots[1].done {
			if c.slots[0].verdict == c.slots[1].verdict {
				finish(c.slots[0].verdict)
			} else {
				c.slots = append(c.slots, &mSlot{})
				c.state = StateWaiting
			}
		}
	case 3:
		if c.slots[2].done {
			finish(c.slots[2].verdict)
		}
	}
}

func (m *model) advance(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: 时刻为负", ErrInvalidParam)
	}
	if now < m.now {
		return fmt.Errorf("%w: 当前 %d 目标 %d", ErrClockRewind, m.now, now)
	}
	m.now = now
	for _, c := range m.order {
		m.materialize(c)
	}
	return nil
}

func mFirstFree(c *mCase) int {
	for i, s := range c.slots {
		if !s.assigned {
			return i
		}
	}
	return -1
}

func mEligibility(c *mCase, idx int, r mReviewer, rid string) error {
	switch idx {
	case 1:
		if r.level != LevelTwo {
			return fmt.Errorf("%w: 双人复核第二名须为二级", ErrInvalidParam)
		}
		if c.slots[0].reviewerID == rid {
			return fmt.Errorf("%w: 第二名与第一名同人", ErrInvalidParam)
		}
	case 2:
		if r.level != LevelTwo {
			return fmt.Errorf("%w: 仲裁员须为二级", ErrInvalidParam)
		}
		if c.slots[0].reviewerID == rid || c.slots[1].reviewerID == rid {
			return fmt.Errorf("%w: 仲裁员与前两人同人", ErrInvalidParam)
		}
	}
	return nil
}

func mOccupy(m *model, c *mCase, idx int, rid string) {
	s := c.slots[idx]
	s.assigned = true
	s.reviewerID = rid
	s.assignTime = m.now
	if mFirstFree(c) == -1 {
		c.state = StateInReview
	}
}

func (m *model) assign(caseID string, slotIdx int, rid string) error {
	if caseID == "" || rid == "" || slotIdx < 0 || slotIdx > 2 {
		return fmt.Errorf("%w: 编号为空或空位越界", ErrInvalidParam)
	}
	c, ok := m.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrCaseNotFound, caseID)
	}
	r, ok := m.reviewers[rid]
	if !ok {
		return fmt.Errorf("%w: %q", ErrReviewerNotFound, rid)
	}
	m.materialize(c)
	if c.state.Terminal() {
		return fmt.Errorf("%w: 案件 %q", ErrTerminal, caseID)
	}
	if slotIdx >= len(c.slots) {
		return fmt.Errorf("%w: 案件无空位 %d", ErrInvalidParam, slotIdx)
	}
	if err := mEligibility(c, slotIdx, r, rid); err != nil {
		return err
	}
	if r.branch == c.branch {
		return fmt.Errorf("%w: 复核员 %q 与案件同网点", ErrConflict, rid)
	}
	if c.slots[slotIdx].assigned {
		return fmt.Errorf("%w: 空位 %d", ErrSlotTaken, slotIdx)
	}
	if slotIdx != mFirstFree(c) {
		return fmt.Errorf("%w: 空位须按序分配", ErrInvalidParam)
	}
	mOccupy(m, c, slotIdx, rid)
	return nil
}

// earliestWaiting 线性扫描受理顺序，取受理时刻最早的未分配案件。
func (m *model) earliestWaiting() *mCase {
	var best *mCase
	for _, c := range m.order {
		if c.state.Terminal() || mFirstFree(c) == -1 {
			continue
		}
		if best == nil || c.acceptTime < best.acceptTime ||
			(c.acceptTime == best.acceptTime && c.seq < best.seq) {
			best = c
		}
	}
	return best
}

func (m *model) applyAssign(rid string) (string, int, error) {
	if rid == "" {
		return "", 0, fmt.Errorf("%w: 复核员编号为空", ErrInvalidParam)
	}
	r, ok := m.reviewers[rid]
	if !ok {
		return "", 0, fmt.Errorf("%w: %q", ErrReviewerNotFound, rid)
	}
	c := m.earliestWaiting()
	if c == nil {
		return "", 0, fmt.Errorf("%w", ErrNoPending)
	}
	idx := mFirstFree(c)
	if err := mEligibility(c, idx, r, rid); err != nil {
		return "", 0, err
	}
	if r.branch == c.branch {
		return "", 0, fmt.Errorf("%w: 复核员 %q 与案件同网点", ErrConflict, rid)
	}
	mOccupy(m, c, idx, rid)
	return c.id, idx, nil
}

func (m *model) submit(caseID, rid string, v Verdict) error {
	if caseID == "" || rid == "" || (v != VerdictPass && v != VerdictReject) {
		return fmt.Errorf("%w: 编号为空或结论非法", ErrInvalidParam)
	}
	c, ok := m.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrCaseNotFound, caseID)
	}
	if _, ok := m.reviewers[rid]; !ok {
		return fmt.Errorf("%w: %q", ErrReviewerNotFound, rid)
	}
	m.materialize(c)
	for _, s := range c.slots {
		if s.assigned && !s.done && s.reviewerID == rid {
			s.done = true
			s.verdict = v
			m.resolve(c)
			return nil
		}
	}
	for _, s := range c.slots {
		if s.assigned && s.auto && s.reviewerID == rid {
			return fmt.Errorf("%w: 空位已按通过自动填入", ErrTimeout)
		}
	}
	if c.state.Terminal() {
		return fmt.Errorf("%w: 案件 %q", ErrTerminal, caseID)
	}
	return fmt.Errorf("%w: 复核员 %q 在该案无待提交空位", ErrNotAssignee, rid)
}

func (m *model) withdraw(caseID string) error {
	if caseID == "" {
		return fmt.Errorf("%w: 案件号为空", ErrInvalidParam)
	}
	c, ok := m.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrCaseNotFound, caseID)
	}
	m.materialize(c)
	if c.state.Terminal() {
		return fmt.Errorf("%w: 案件 %q", ErrTerminal, caseID)
	}
	c.state = StateWithdrawn
	for _, s := range c.slots {
		if s.assigned && !s.done {
			s.done = true
			s.void = true
		}
	}
	return nil
}

func (m *model) snapshot(caseID string) (CaseSnapshot, bool) {
	c, ok := m.cases[caseID]
	if !ok {
		return CaseSnapshot{}, false
	}
	m.materialize(c)
	snap := CaseSnapshot{ID: c.id, Score: c.score, State: c.state, AcceptTime: c.acceptTime}
	for _, s := range c.slots {
		snap.Slots = append(snap.Slots, SlotSnapshot{
			Assigned:   s.assigned,
			ReviewerID: s.reviewerID,
			Done:       s.done,
			Auto:       s.auto,
			Void:       s.void,
			Verdict:    s.verdict,
			AssignTime: s.assignTime,
		})
	}
	return snap, true
}

func (m *model) pendingCount() int {
	n := 0
	for _, c := range m.order {
		if !c.state.Terminal() && mFirstFree(c) != -1 {
			n++
		}
	}
	return n
}

// ---------- 随机操作序列差分对照 ----------

type opKind int

const (
	opAccept opKind = iota
	opUpsertRule
	opDeleteRule
	opRegisterReviewer
	opAssign
	opApplyAssign
	opSubmit
	opAdvance
	opWithdraw
)

type op struct {
	kind     opKind
	caseID   string
	reviewer string
	ruleID   string
	code     string
	branch   string
	slot     int
	score    int
	features []string
	at       int64
	amount   int64
	verdict  Verdict
	level    Level
	advance  int64
}

func (o op) String() string {
	switch o.kind {
	case opAccept:
		return fmt.Sprintf("受理(%s at=%d feat=%v 网点=%s 金额=%d)", o.caseID, o.at, o.features, o.branch, o.amount)
	case opUpsertRule:
		return fmt.Sprintf("规则(%s %s=%d)", o.ruleID, o.code, o.score)
	case opDeleteRule:
		return fmt.Sprintf("删规则(%s)", o.ruleID)
	case opRegisterReviewer:
		return fmt.Sprintf("登记(%s 网点=%s 级别=%d)", o.reviewer, o.branch, o.level)
	case opAssign:
		return fmt.Sprintf("分配(%s[%d]=%s)", o.caseID, o.slot, o.reviewer)
	case opApplyAssign:
		return fmt.Sprintf("申请分配(%s)", o.reviewer)
	case opSubmit:
		return fmt.Sprintf("提交(%s %s %s)", o.caseID, o.reviewer, o.verdict)
	case opAdvance:
		return fmt.Sprintf("推进(%d)", o.advance)
	case opWithdraw:
		return fmt.Sprintf("撤回(%s)", o.caseID)
	}
	return "?"
}

var errSentinels = []struct {
	name string
	err  error
}{
	{"参数非法", ErrInvalidParam},
	{"时钟回退", ErrClockRewind},
	{"案件不存在", ErrCaseNotFound},
	{"复核员不存在", ErrReviewerNotFound},
	{"已终态", ErrTerminal},
	{"已超时", ErrTimeout},
	{"非本人", ErrNotAssignee},
	{"利益冲突", ErrConflict},
	{"已分配", ErrSlotTaken},
	{"无待办", ErrNoPending},
}

func errCode(err error) string {
	if err == nil {
		return "ok"
	}
	for _, s := range errSentinels {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	return "未知错误:" + err.Error()
}

func applyOpEngine(e *Engine, o op) string {
	switch o.kind {
	case opAccept:
		return errCode(e.Accept(o.caseID, o.at, o.features, o.branch, o.amount))
	case opUpsertRule:
		return errCode(e.UpsertRule(Rule{ID: o.ruleID, Code: o.code, Score: o.score}))
	case opDeleteRule:
		return errCode(e.DeleteRule(o.ruleID))
	case opRegisterReviewer:
		return errCode(e.RegisterReviewer(o.reviewer, o.branch, o.level))
	case opAssign:
		return errCode(e.Assign(o.caseID, o.slot, o.reviewer))
	case opApplyAssign:
		id, slot, err := e.ApplyAssign(o.reviewer)
		if err != nil {
			return errCode(err)
		}
		return fmt.Sprintf("ok:%s[%d]", id, slot)
	case opSubmit:
		return errCode(e.Submit(o.caseID, o.reviewer, o.verdict))
	case opAdvance:
		return errCode(e.Advance(o.advance))
	case opWithdraw:
		return errCode(e.Withdraw(o.caseID))
	}
	return "?"
}

func applyOpModel(m *model, o op) string {
	switch o.kind {
	case opAccept:
		return errCode(m.accept(o.caseID, o.at, o.features, o.branch, o.amount))
	case opUpsertRule:
		return errCode(m.upsertRule(Rule{ID: o.ruleID, Code: o.code, Score: o.score}))
	case opDeleteRule:
		return errCode(m.deleteRule(o.ruleID))
	case opRegisterReviewer:
		return errCode(m.registerReviewer(o.reviewer, o.branch, o.level))
	case opAssign:
		return errCode(m.assign(o.caseID, o.slot, o.reviewer))
	case opApplyAssign:
		id, slot, err := m.applyAssign(o.reviewer)
		if err != nil {
			return errCode(err)
		}
		return fmt.Sprintf("ok:%s[%d]", id, slot)
	case opSubmit:
		return errCode(m.submit(o.caseID, o.reviewer, o.verdict))
	case opAdvance:
		return errCode(m.advance(o.advance))
	case opWithdraw:
		return errCode(m.withdraw(o.caseID))
	}
	return "?"
}

// opGen 生成确定性的随机操作序列。
type opGen struct {
	rng         *rand.Rand
	caseN       int
	revN        int
	ruleN       int
	clock       int64
	acceptClock int64
	caseIDs     []string
	revIDs      []string
	ruleIDs     []string
}

func (g *opGen) pickCase() string {
	if len(g.caseIDs) == 0 || g.rng.Intn(10) == 0 {
		return "ghostCase"
	}
	return g.caseIDs[g.rng.Intn(len(g.caseIDs))]
}

func (g *opGen) pickReviewer() string {
	if len(g.revIDs) == 0 || g.rng.Intn(10) == 0 {
		return "ghostRev"
	}
	return g.revIDs[g.rng.Intn(len(g.revIDs))]
}

func (g *opGen) next() op {
	r := g.rng
	switch w := r.Intn(100); {
	case w < 22: // 受理
		g.caseN++
		g.acceptClock += r.Int63n(3)
		var feats []string
		for i := 0; i < 4; i++ {
			if r.Intn(2) == 0 {
				feats = append(feats, fmt.Sprintf("F%d", i))
			}
		}
		id := fmt.Sprintf("C%d", g.caseN)
		g.caseIDs = append(g.caseIDs, id)
		return op{kind: opAccept, caseID: id, at: g.acceptClock, features: feats,
			branch: fmt.Sprintf("B%d", r.Intn(3)), amount: r.Int63n(1000)}
	case w < 28: // 新增/更新规则
		id := fmt.Sprintf("L%d", r.Intn(g.ruleN+1))
		if id == fmt.Sprintf("L%d", g.ruleN) {
			g.ruleN++
			g.ruleIDs = append(g.ruleIDs, id)
		}
		return op{kind: opUpsertRule, ruleID: id,
			code: fmt.Sprintf("F%d", r.Intn(4)), score: r.Intn(11) - 4}
	case w < 30: // 删除规则
		id := "ghostRule"
		if len(g.ruleIDs) > 0 && r.Intn(2) == 0 {
			id = g.ruleIDs[r.Intn(len(g.ruleIDs))]
		}
		return op{kind: opDeleteRule, ruleID: id}
	case w < 36: // 登记复核员
		g.revN++
		id := fmt.Sprintf("R%d", g.revN)
		g.revIDs = append(g.revIDs, id)
		return op{kind: opRegisterReviewer, reviewer: id,
			branch: fmt.Sprintf("B%d", r.Intn(3)), level: Level(1 + r.Intn(2))}
	case w < 48: // 指定空位分配
		return op{kind: opAssign, caseID: g.pickCase(), slot: r.Intn(3), reviewer: g.pickReviewer()}
	case w < 64: // 申请分配
		return op{kind: opApplyAssign, reviewer: g.pickReviewer()}
	case w < 82: // 提交结论
		v := VerdictPass
		if x := r.Intn(10); x < 4 {
			v = VerdictReject
		} else if x == 9 {
			v = VerdictNone
		}
		return op{kind: opSubmit, caseID: g.pickCase(), reviewer: g.pickReviewer(), verdict: v}
	case w < 94: // 推进时刻
		switch r.Intn(10) {
		case 0:
			return op{kind: opAdvance, advance: -5}
		case 1:
			return op{kind: opAdvance, advance: g.clock - 1}
		default:
			g.clock += r.Int63n(9)
			return op{kind: opAdvance, advance: g.clock}
		}
	default: // 撤回
		return op{kind: opWithdraw, caseID: g.pickCase()}
	}
}

// TestDifferentialRandom 用同一随机操作序列同时驱动引擎与朴素模型，
// 逐步比对返回结果与全量状态，日志打印输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	type params struct {
		seed    int64
		timeout int64
	}
	runs := []params{{1, 5}, {2, 40}, {3, 5}, {42, 40}, {99, 5}, {20261007, 40}}

	// 关键路径覆盖统计（跨种子）。
	var covArbitration, covAutoFill, covWithdraw, covConflict, covTimeout int

	for _, run := range runs {
		t.Run(fmt.Sprintf("seed=%d/timeout=%d", run.seed, run.timeout), func(t *testing.T) {
			seed := run.seed
			rng := rand.New(rand.NewSource(seed))
			eng, err := NewEngine(3, 7, run.timeout)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			mdl := newModel(3, 7, run.timeout)
			g := &opGen{rng: rng}

			revInfo := map[string]struct {
				branch string
				level  Level
			}{}
			caseBranch := map[string]string{}

			// 预置规则与复核员，两侧一致。
			preset := []op{
				{kind: opUpsertRule, ruleID: "L0", code: "F0", score: 3},
				{kind: opUpsertRule, ruleID: "L1", code: "F1", score: 5},
				{kind: opUpsertRule, ruleID: "L2", code: "F2", score: -2},
				{kind: opRegisterReviewer, reviewer: "R0", branch: "B0", level: LevelOne},
				{kind: opRegisterReviewer, reviewer: "R1", branch: "B1", level: LevelTwo},
				{kind: opRegisterReviewer, reviewer: "R2", branch: "B2", level: LevelTwo},
			}
			g.ruleN, g.revN = 3, 3
			g.ruleIDs = []string{"L0", "L1", "L2"}
			g.revIDs = []string{"R0", "R1", "R2"}
			for _, o := range preset {
				applyOpEngine(eng, o)
				applyOpModel(mdl, o)
				revInfo[o.reviewer] = struct {
					branch string
					level  Level
				}{o.branch, o.level}
			}

			var accepted []string

			// smartOp 依据引擎当前状态生成定向操作，提高双人复核、
			// 仲裁等深层路径的覆盖率；操作确定性地由种子决定。
			smartOp := func() op {
				if len(accepted) == 0 {
					return g.next()
				}
				id := accepted[rng.Intn(len(accepted))]
				snap, _ := eng.Snapshot(id)
				if rng.Intn(2) == 0 {
					for _, s := range snap.Slots {
						if s.Assigned && !s.Done {
							v := VerdictPass
							if rng.Intn(2) == 0 {
								v = VerdictReject
							}
							return op{kind: opSubmit, caseID: id, reviewer: s.ReviewerID, verdict: v}
						}
					}
					return g.next()
				}
				if snap.State != StateWaiting {
					return g.next()
				}
				idx := -1
				for i, s := range snap.Slots {
					if !s.Assigned {
						idx = i
						break
					}
				}
				if idx < 0 {
					return g.next()
				}
				taken := map[string]bool{}
				for _, s := range snap.Slots {
					if s.Assigned {
						taken[s.ReviewerID] = true
					}
				}
				for _, rid := range g.revIDs {
					info := revInfo[rid]
					if idx > 0 && info.level != LevelTwo {
						continue
					}
					if taken[rid] || info.branch == caseBranch[id] {
						continue
					}
					return op{kind: opAssign, caseID: id, slot: idx, reviewer: rid}
				}
				return g.next()
			}

			for step := 0; step < 600; step++ {
				var o op
				if rng.Intn(10) < 3 {
					o = smartOp()
				} else {
					o = g.next()
				}
				resE := applyOpEngine(eng, o)
				resM := applyOpModel(mdl, o)
				if resE != resM {
					t.Fatalf("step=%d 操作 %s 结果分歧：引擎=%s 模型=%s", step, o, resE, resM)
				}
				if o.kind == opAccept && resE == "ok" {
					accepted = append(accepted, o.caseID)
					caseBranch[o.caseID] = o.branch
				}
				if o.kind == opRegisterReviewer && resE == "ok" {
					revInfo[o.reviewer] = struct {
						branch string
						level  Level
					}{o.branch, o.level}
				}
				// 全量状态比对：每个案件的快照、等待数、当前时刻。
				for _, id := range accepted {
					se, _ := eng.Snapshot(id)
					sm, _ := mdl.snapshot(id)
					if !reflect.DeepEqual(se, sm) {
						t.Fatalf("step=%d 操作 %s 后案件 %s 状态分歧：\n引擎: %+v\n模型: %+v",
							step, o, id, se, sm)
					}
					if len(se.Slots) == 3 {
						covArbitration++
					}
					for _, s := range se.Slots {
						if s.Auto {
							covAutoFill++
						}
					}
					if se.State == StateWithdrawn {
						covWithdraw++
					}
				}
				switch resE {
				case "利益冲突":
					covConflict++
				case "已超时":
					covTimeout++
				}
				if eng.PendingCount() != mdl.pendingCount() {
					t.Fatalf("step=%d 操作 %s 后等待数分歧：引擎=%d 模型=%d",
						step, o, eng.PendingCount(), mdl.pendingCount())
				}
				if eng.Now() != mdl.now {
					t.Fatalf("step=%d 操作 %s 后时刻分歧：引擎=%d 模型=%d", step, o, eng.Now(), mdl.now)
				}
				t.Logf("step=%03d 输入=%s 输出=%s 判定依据=[now=%d pending=%d]",
					step, o, resE, eng.Now(), eng.PendingCount())
			}
		})
	}

	// 随机序列必须触达关键路径，否则视为覆盖不足。
	if covArbitration == 0 || covAutoFill == 0 || covWithdraw == 0 || covConflict == 0 || covTimeout == 0 {
		t.Fatalf("关键路径覆盖不足：仲裁=%d 超时自动填入=%d 撤回=%d 利益冲突=%d 已超时=%d",
			covArbitration, covAutoFill, covWithdraw, covConflict, covTimeout)
	}
	t.Logf("关键路径覆盖：仲裁=%d 超时自动填入=%d 撤回=%d 利益冲突=%d 已超时=%d",
		covArbitration, covAutoFill, covWithdraw, covConflict, covTimeout)
}
