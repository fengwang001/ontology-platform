package estimation

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 本文件包含一个刻意独立的朴素模型：它不用任何增量计数器，
// 每次揭示都重新遍历全体成员与选票重新计算，以此与生产实现
// （计数器 + 惰性到期）交叉对照。两者接受完全相同的随机操作
// 序列，逐操作比对错误码、揭示结果与 Peek 视图；每个序列还会在
// 生产实现上重放两次，验证确定性。

// naiveSession 为朴素模型：状态只用普通 map 保存，揭示时全量重算。
type naiveSession struct {
	deck       []int
	pos        map[int]int
	maxRounds  int
	ttl        int64
	auto       bool
	host       string
	lastNow    int64
	members    map[string]Role
	phase      Phase
	round      int
	roundStart int64
	votes      map[string]Card
}

func newNaive(host string, deck []int, maxRounds int, ttl int64, auto bool) *naiveSession {
	pos := make(map[int]int, len(deck))
	for i, v := range deck {
		pos[v] = i
	}
	return &naiveSession{
		deck:      deck,
		pos:       pos,
		maxRounds: maxRounds,
		ttl:       ttl,
		auto:      auto,
		host:      host,
		members:   map[string]Role{},
		votes:     map[string]Card{},
	}
}

func (n *naiveSession) voterCount() int {
	c := 0
	for _, r := range n.members {
		if r == RoleVoter {
			c++
		}
	}
	return c
}

func (n *naiveSession) validCard(c Card) bool {
	if c.Special() {
		return true
	}
	_, ok := n.pos[int(c)]
	return ok
}

func (n *naiveSession) isMember(user string) bool {
	_, ok := n.members[user]
	return ok
}

// doReveal 全量重算揭示结果，并推进朴素模型的状态机。
func (n *naiveSession) doReveal(at int64, trig RevealTrigger) *RevealOutcome {
	dist := map[Card]int{}
	nums := []int{}
	for _, c := range n.votes {
		dist[c]++
		if !c.Special() {
			nums = append(nums, int(c))
		}
	}
	out := &RevealOutcome{
		Round:        n.round,
		RevealedAt:   at,
		Trigger:      trig,
		Distribution: dist,
		NumericVotes: len(nums),
	}
	sort.Ints(nums)
	switch {
	case len(nums) == 0:
		if n.round >= n.maxRounds {
			out.Kind = ResultFinalNoResult
		} else {
			out.Kind = ResultNoValidVotes
		}
	case nums[0] == nums[len(nums)-1]:
		out.Kind = ResultConsensus
		out.Value = nums[0]
		out.HasValue = true
	default:
		lo := n.pos[nums[0]]
		hi := n.pos[nums[len(nums)-1]]
		switch {
		case hi-lo == 1:
			out.Kind = ResultConverged
			out.Value = n.deck[hi]
			out.HasValue = true
		case n.round >= n.maxRounds:
			out.Kind = ResultForced
			out.Value = nums[(len(nums)-1)/2]
			out.HasValue = true
		default:
			out.Kind = ResultDiverged
		}
	}
	out.IssueEnded = out.Kind.EndsIssue()
	if out.IssueEnded {
		n.phase = PhaseIdle
	} else {
		n.phase = PhaseRevealed
	}
	return out
}

func (n *naiveSession) expireIfDue(now int64) *RevealOutcome {
	if n.phase == PhaseVoting && now >= n.roundStart+n.ttl {
		return n.doReveal(n.roundStart+n.ttl, TriggerExpired)
	}
	return nil
}

func (n *naiveSession) autoReveal(now int64) *RevealOutcome {
	if n.auto && n.phase == PhaseVoting {
		voters := n.voterCount()
		if voters >= 1 && len(n.votes) == voters {
			return n.doReveal(now, TriggerAuto)
		}
	}
	return nil
}

func (n *naiveSession) checkClock(now int64) *Error {
	if now < n.lastNow {
		return newError(ErrClockRegression, "时钟回退")
	}
	return nil
}

func (n *naiveSession) join(user string, role Role, now int64) (*RevealOutcome, *Error) {
	if user == "" || !role.Valid() || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	if n.isMember(user) {
		return exp, newError(ErrAlreadyExists, "已存在")
	}
	n.members[user] = role
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) leave(user string, now int64) (*RevealOutcome, *Error) {
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	role, ok := n.members[user]
	if !ok {
		return exp, newError(ErrNotInSession, "不在会话")
	}
	if role == RoleVoter {
		delete(n.votes, user)
	}
	delete(n.members, user)
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) setRole(actor, target string, role Role, now int64) (*RevealOutcome, *Error) {
	if actor == "" || target == "" || !role.Valid() || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	if actor != n.host && !n.isMember(actor) {
		return exp, newError(ErrNotInSession, "调用者不在会话")
	}
	old, ok := n.members[target]
	if !ok {
		return exp, newError(ErrNotInSession, "目标不在会话")
	}
	if actor != n.host && actor != target {
		return exp, newError(ErrPermissionDenied, "权限不足")
	}
	if old != role {
		if old == RoleVoter {
			delete(n.votes, target)
		}
		n.members[target] = role
	}
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) start(user string, now int64) (*RevealOutcome, *Error) {
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	if user != n.host && !n.isMember(user) {
		return exp, newError(ErrNotInSession, "调用者不在会话")
	}
	if user != n.host {
		return exp, newError(ErrPermissionDenied, "权限不足")
	}
	if n.phase != PhaseIdle {
		return exp, newError(ErrInvalidState, "状态不允许")
	}
	n.phase = PhaseVoting
	n.round = 1
	n.roundStart = now
	n.votes = map[string]Card{}
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) vote(user string, card Card, now int64) (*RevealOutcome, *Error) {
	if user == "" || !n.validCard(card) || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	role, ok := n.members[user]
	if !ok {
		return exp, newError(ErrNotInSession, "调用者不在会话")
	}
	if role != RoleVoter {
		return exp, newError(ErrPermissionDenied, "权限不足")
	}
	if n.phase != PhaseVoting {
		return exp, newError(ErrInvalidState, "状态不允许")
	}
	n.votes[user] = card
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) unvote(user string, now int64) (*RevealOutcome, *Error) {
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	role, ok := n.members[user]
	if !ok {
		return exp, newError(ErrNotInSession, "调用者不在会话")
	}
	if role != RoleVoter {
		return exp, newError(ErrPermissionDenied, "权限不足")
	}
	if n.phase != PhaseVoting {
		return exp, newError(ErrInvalidState, "状态不允许")
	}
	delete(n.votes, user)
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) reveal(user string, now int64) (*RevealOutcome, *Error) {
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	if user != n.host && !n.isMember(user) {
		return exp, newError(ErrNotInSession, "调用者不在会话")
	}
	if user != n.host {
		return exp, newError(ErrPermissionDenied, "权限不足")
	}
	if n.phase != PhaseVoting {
		return exp, newError(ErrInvalidState, "状态不允许")
	}
	if len(n.votes) == 0 {
		return exp, newError(ErrNoVotes, "无人投票")
	}
	out := n.doReveal(now, TriggerManual)
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) revote(user string, now int64) (*RevealOutcome, *Error) {
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	exp := n.expireIfDue(now)
	if user != n.host && !n.isMember(user) {
		return exp, newError(ErrNotInSession, "调用者不在会话")
	}
	if user != n.host {
		return exp, newError(ErrPermissionDenied, "权限不足")
	}
	if n.phase != PhaseRevealed {
		return exp, newError(ErrInvalidState, "状态不允许")
	}
	n.round++
	n.phase = PhaseVoting
	n.roundStart = now
	n.votes = map[string]Card{}
	out := exp
	if a := n.autoReveal(now); a != nil {
		out = a
	}
	n.lastNow = now
	return out, nil
}

func (n *naiveSession) peek(user string, now int64) (PeekView, *RevealOutcome, *Error) {
	var view PeekView
	if user == "" || !validNow(now) {
		return view, nil, newError(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return view, nil, err
	}
	exp := n.expireIfDue(now)
	if !n.isMember(user) {
		return view, exp, newError(ErrNotInSession, "调用者不在会话")
	}
	if c, ok := n.votes[user]; ok {
		view.HasVote = true
		view.Self = c
	}
	view.VotedCount = len(n.votes)
	return view, exp, nil
}

// ---- 随机操作序列生成与对照驱动 ----

type opKind int

const (
	opJoin opKind = iota
	opLeave
	opSetRole
	opStart
	opVote
	opUnvote
	opReveal
	opRevote
	opPeek
)

type testOp struct {
	kind   opKind
	user   string
	target string
	role   Role
	card   Card
	now    int64
}

func (op testOp) String() string {
	switch op.kind {
	case opJoin:
		return fmt.Sprintf("Join(%s,%s,%d)", op.user, op.role, op.now)
	case opLeave:
		return fmt.Sprintf("Leave(%s,%d)", op.user, op.now)
	case opSetRole:
		return fmt.Sprintf("SetRole(%s,%s,%s,%d)", op.user, op.target, op.role, op.now)
	case opStart:
		return fmt.Sprintf("Start(%s,%d)", op.user, op.now)
	case opVote:
		return fmt.Sprintf("Vote(%s,%s,%d)", op.user, op.card, op.now)
	case opUnvote:
		return fmt.Sprintf("Unvote(%s,%d)", op.user, op.now)
	case opReveal:
		return fmt.Sprintf("Reveal(%s,%d)", op.user, op.now)
	case opRevote:
		return fmt.Sprintf("Revote(%s,%d)", op.user, op.now)
	case opPeek:
		return fmt.Sprintf("Peek(%s,%d)", op.user, op.now)
	}
	return "?"
}

// opOutcome 为归一化的操作结果，用于两种实现逐操作比对。
type opOutcome struct {
	reveal *RevealOutcome
	peek   *PeekView
	err    ErrCode // 0 表示成功
}

func applyReal(s *Session, op testOp) opOutcome {
	var out *RevealOutcome
	var err *Error
	switch op.kind {
	case opJoin:
		out, err = s.Join(op.user, op.role, op.now)
	case opLeave:
		out, err = s.Leave(op.user, op.now)
	case opSetRole:
		out, err = s.SetRole(op.user, op.target, op.role, op.now)
	case opStart:
		out, err = s.Start(op.user, op.now)
	case opVote:
		out, err = s.Vote(op.user, op.card, op.now)
	case opUnvote:
		out, err = s.Unvote(op.user, op.now)
	case opReveal:
		out, err = s.Reveal(op.user, op.now)
	case opRevote:
		out, err = s.Revote(op.user, op.now)
	case opPeek:
		view, exp, e := s.Peek(op.user, op.now)
		return opOutcome{reveal: exp, peek: &view, err: errCode(e)}
	}
	return opOutcome{reveal: out, err: errCode(err)}
}

func applyNaive(n *naiveSession, op testOp) opOutcome {
	var out *RevealOutcome
	var err *Error
	switch op.kind {
	case opJoin:
		out, err = n.join(op.user, op.role, op.now)
	case opLeave:
		out, err = n.leave(op.user, op.now)
	case opSetRole:
		out, err = n.setRole(op.user, op.target, op.role, op.now)
	case opStart:
		out, err = n.start(op.user, op.now)
	case opVote:
		out, err = n.vote(op.user, op.card, op.now)
	case opUnvote:
		out, err = n.unvote(op.user, op.now)
	case opReveal:
		out, err = n.reveal(op.user, op.now)
	case opRevote:
		out, err = n.revote(op.user, op.now)
	case opPeek:
		view, exp, e := n.peek(op.user, op.now)
		return opOutcome{reveal: exp, peek: &view, err: errCode(e)}
	}
	return opOutcome{reveal: out, err: errCode(err)}
}

func errCode(err *Error) ErrCode {
	if err == nil {
		return 0
	}
	return err.Code
}

func describeOutcome(oc opOutcome) string {
	s := ""
	if oc.err != 0 {
		s += "err=" + oc.err.String()
	} else {
		s += "ok"
	}
	if oc.reveal != nil {
		r := oc.reveal
		s += fmt.Sprintf(" reveal{round=%d at=%d trig=%s dist=%v numeric=%d kind=%s value=%d hasValue=%v ended=%v}",
			r.Round, r.RevealedAt, r.Trigger, r.Distribution, r.NumericVotes, r.Kind, r.Value, r.HasValue, r.IssueEnded)
	}
	if oc.peek != nil {
		s += fmt.Sprintf(" peek{%+v}", *oc.peek)
	}
	return s
}

var (
	modelUsers  = []string{"host", "u0", "u1", "u2", "u3", "u4", "ghost"}
	modelDecks  = [][]int{{1, 2, 3, 5, 8, 13}, {1, 2, 4, 8, 16}, {5, 10}, {1, 3, 5, 7, 9, 11, 13, 15}}
	badCards    = []Card{0, 999, -7, Card(1 << 20)}
	badRole     = Role(99)
	specialDeck = []Card{CardUnsure, CardBreak}
)

// genSequence 生成一条随机操作序列及其会话参数。
func genSequence(rng *rand.Rand) ([]int, int, int64, bool, []testOp) {
	deck := modelDecks[rng.Intn(len(modelDecks))]
	rounds := 1 + rng.Intn(MaxRounds)
	ttl := int64(1 + rng.Intn(8))
	if rng.Intn(100) < 40 {
		ttl = int64(5 + rng.Intn(40)) // 部分会话给足投票窗口
	}
	auto := rng.Intn(2) == 0

	n := 5 + rng.Intn(26)
	ops := make([]testOp, 0, n)
	var cur int64
	// 固定前缀：若干投票者加入并由主持人开局，保证序列能深入投票流程。
	for _, u := range []string{"u0", "u1", "u2", "u3"} {
		ops = append(ops, testOp{kind: opJoin, user: u, role: RoleVoter, now: cur})
	}
	ops = append(ops, testOp{kind: opStart, user: "host", now: cur})
	for i := 0; i < n; i++ {
		// 时间：多数小幅前进，少量回退、大跳变或越界。
		now := cur
		switch r := rng.Intn(100); {
		case r < 78:
			cur += int64(rng.Intn(3))
			now = cur
		case r < 84:
			if cur > 0 {
				now = cur - 1 // 时钟回退
			}
		case r < 90:
			cur += int64(5 + rng.Intn(20)) // 大跳变，容易越过到期时刻
			now = cur
		case r < 92:
			now = MaxNow + 1 // 越界
		}
		user := modelUsers[rng.Intn(len(modelUsers))]
		target := modelUsers[rng.Intn(len(modelUsers))]
		role := RoleVoter
		if rng.Intn(2) == 0 {
			role = RoleObserver
		}
		if rng.Intn(12) == 0 {
			role = badRole
		}
		card := Card(deck[rng.Intn(len(deck))])
		switch r := rng.Intn(100); {
		case r < 12:
			card = specialDeck[rng.Intn(2)]
		case r < 20:
			card = badCards[rng.Intn(len(badCards))]
		}
		var op testOp
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13:
			op = testOp{kind: opJoin, user: user, role: role, now: now}
		case 14, 15, 16, 17, 18, 19, 20:
			op = testOp{kind: opLeave, user: user, now: now}
		case 21, 22, 23, 24, 25, 26, 27, 28, 29:
			op = testOp{kind: opSetRole, user: user, target: target, role: role, now: now}
		case 30, 31, 32, 33, 34, 35, 36:
			op = testOp{kind: opStart, user: user, now: now}
		case 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61:
			op = testOp{kind: opVote, user: user, card: card, now: now}
		case 62, 63, 64, 65, 66, 67, 68:
			op = testOp{kind: opUnvote, user: user, now: now}
		case 69, 70, 71, 72, 73, 74, 75:
			op = testOp{kind: opReveal, user: user, now: now}
		case 76, 77, 78, 79, 80, 81, 82:
			op = testOp{kind: opRevote, user: user, now: now}
		default:
			op = testOp{kind: opPeek, user: user, now: now}
		}
		ops = append(ops, op)
	}
	return deck, rounds, ttl, auto, ops
}

// TestModelAgainstNaive 用至少 1500 组随机操作序列对照生产实现与
// 朴素模型，并验证相同序列重放结果完全一致。日志打印每条序列的
// 输入（参数与操作）、输出（错误码/揭示结果/Peek 视图）与判定依据
// （分布、参与统计票数、结果类别、取值）。
func TestModelAgainstNaive(t *testing.T) {
	const sequences = 1500
	rng := rand.New(rand.NewSource(20261007))
	coverage := map[string]int{}
	for i := 0; i < sequences; i++ {
		deck, rounds, ttl, auto, ops := genSequence(rng)
		t.Logf("seq=%d deck=%v R=%d T=%d auto=%v ops=%d", i, deck, rounds, ttl, auto, len(ops))

		real, err := NewSession("host", deck, rounds, ttl, auto)
		if err != nil {
			t.Fatalf("seq=%d NewSession: %v", i, err)
		}
		naive := newNaive("host", deck, rounds, ttl, auto)
		replay, err := NewSession("host", deck, rounds, ttl, auto)
		if err != nil {
			t.Fatalf("seq=%d NewSession(replay): %v", i, err)
		}

		for j, op := range ops {
			got := applyReal(real, op)
			want := applyNaive(naive, op)
			again := applyReal(replay, op)

			t.Logf("  op=%d %s -> %s", j, op, describeOutcome(got))
			if got.err != 0 {
				coverage["err:"+got.err.String()]++
			}
			if got.reveal != nil {
				coverage["reveal:"+got.reveal.Kind.String()]++
				coverage["trigger:"+got.reveal.Trigger.String()]++
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seq=%d op=%d %s\n生产实现: %s\n朴素模型: %s",
					i, j, op, describeOutcome(got), describeOutcome(want))
			}
			if !reflect.DeepEqual(got, again) {
				t.Fatalf("seq=%d op=%d %s 重放不一致:\n首次: %s\n重放: %s",
					i, j, op, describeOutcome(got), describeOutcome(again))
			}
		}
	}
	t.Logf("覆盖统计: %v", coverage)
	// 随机序列必须实质性地覆盖关键路径，否则对照失去意义。
	for _, key := range []string{
		"reveal:consensus", "reveal:converged", "reveal:diverged",
		"reveal:no_valid_votes", "reveal:forced", "reveal:final_no_result",
		"trigger:manual", "trigger:auto", "trigger:expired",
		"err:invalid_param", "err:clock_regression", "err:not_in_session",
		"err:permission_denied", "err:invalid_state", "err:no_votes",
		"err:already_exists",
	} {
		if coverage[key] == 0 {
			t.Fatalf("随机序列未覆盖关键路径: %s", key)
		}
	}
}
