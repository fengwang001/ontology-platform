package appeal_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/appeal"
	"ontology/decision"
	"ontology/strike"
)

func mustNew(t *testing.T, p, a, tmax int64) *appeal.System {
	t.Helper()
	s, err := appeal.New(p, a, tmax)
	if err != nil {
		t.Fatalf("appeal.New: %v", err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

func levelOf(t *testing.T, s *appeal.System, content string) int {
	t.Helper()
	l, err := s.EffectiveLevel([]byte(content))
	if err != nil {
		t.Fatalf("EffectiveLevel(%s): %v", content, err)
	}
	return l
}

func outcomeOf(t *testing.T, s *appeal.System, id string, now int64) appeal.Outcome {
	t.Helper()
	o, err := s.Outcome([]byte(id), now)
	if err != nil {
		t.Fatalf("Outcome(%s): %v", id, err)
	}
	return o
}

// 规格主例：P=1000，A=100，Tmax=500，创作者 u 的内容 c。
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 2, []byte("r1")))
	if got := levelOf(t, s, "c"); got != 2 {
		t.Fatalf("level=%d, want 2", got)
	}
	if got := s.State([]byte("u"), 10); got != strike.StateNormal {
		t.Fatalf("s=1, State=%v, want normal", got)
	}
	must(t, s.Decide(20, []byte("d2"), []byte("c"), 3, []byte("r2")))
	if got := levelOf(t, s, "c"); got != 3 {
		t.Fatalf("level=%d, want 3", got)
	}
	if got := s.State([]byte("u"), 20); got != strike.StateMuted {
		t.Fatalf("s=1+2=3, State=%v, want muted", got)
	}
	// 禁言时 Publish 报账号受限。
	wantErr(t, s.Publish(25, []byte("c2"), []byte("u")), decision.ErrAccountRestricted)

	// t=110 申诉 d1：110 不大于 10+100，允许。
	must(t, s.Appeal(110, []byte("a1"), []byte("d1"), []byte("u")))
	// t=121 申诉 d2：121 大于 20+100，报超过期限。
	wantErr(t, s.Appeal(121, []byte("a2"), []byte("d2"), []byte("u")), appeal.ErrExpired)

	// t=130 原审核员 r1 投票报须回避。
	wantErr(t, s.Review(130, []byte("a1"), []byte("r1"), true), appeal.ErrRecusal)
	// r3 推翻、r4 维持、r5 推翻：2 比 1 结案为推翻。
	must(t, s.Review(130, []byte("a1"), []byte("r3"), true))
	must(t, s.Review(130, []byte("a1"), []byte("r4"), false))
	must(t, s.Review(130, []byte("a1"), []byte("r5"), true))
	if got := outcomeOf(t, s, "a1", 130); got != appeal.OutcomeOverturned {
		t.Fatalf("outcome=%v, want overturned", got)
	}
	// 结案后再投票报已结案。
	wantErr(t, s.Review(131, []byte("a1"), []byte("r6"), true), appeal.ErrClosed)

	// d1 已推翻：有效级别仍为 3（d2 生效），s=2，u 恢复正常。
	if over, err := s.DecisionOverturned([]byte("d1")); err != nil || !over {
		t.Fatalf("d1 overturned=%v err=%v, want true", over, err)
	}
	if got := levelOf(t, s, "c"); got != 3 {
		t.Fatalf("after overturn level=%d, want 3", got)
	}
	if got := s.State([]byte("u"), 130); got != strike.StateNormal {
		t.Fatalf("s=2, State=%v, want normal", got)
	}
	// t=1019：d2 计分仍有效（1019 小于 1020），s=2；t=1020：s=0。
	if got := s.State([]byte("u"), 1019); got != strike.StateNormal {
		t.Fatalf("t=1019 State=%v, want normal (s=2)", got)
	}
	if got := s.State([]byte("u"), 1020); got != strike.StateNormal {
		t.Fatalf("t=1020 State=%v, want normal (s=0)", got)
	}
	// 有效级别不随计分过期而变化，始终为 3。
	if got := levelOf(t, s, "c"); got != 3 {
		t.Fatalf("level=%d, want 3 (不随计分过期变化)", got)
	}
}

// 规格再例：超时按维持结案，已投的票作废，决定不能再次申诉。
func TestSpecExampleTimeout(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 2, []byte("r1")))
	must(t, s.Appeal(110, []byte("a1"), []byte("d1"), []byte("u")))
	// a1 只收到 r3 的一票推翻。
	must(t, s.Review(130, []byte("a1"), []byte("r3"), true))
	if got := outcomeOf(t, s, "a1", 609); got != appeal.OutcomePending {
		t.Fatalf("t=609 outcome=%v, want pending", got)
	}
	// t=610（110+500）仍未结案，视为维持。
	if got := outcomeOf(t, s, "a1", 610); got != appeal.OutcomeUpheld {
		t.Fatalf("t=610 outcome=%v, want upheld", got)
	}
	// 此后 r4 的 Review 报已结案。
	wantErr(t, s.Review(610, []byte("a1"), []byte("r4"), false), appeal.ErrClosed)
	// d1 保持生效，也不能再次申诉（报已申诉过）。
	if over, _ := s.DecisionOverturned([]byte("d1")); over {
		t.Fatal("d1 must stay effective after timeout")
	}
	if got := levelOf(t, s, "c"); got != 2 {
		t.Fatalf("level=%d, want 2", got)
	}
	wantErr(t, s.Appeal(611, []byte("a2"), []byte("d1"), []byte("u")), appeal.ErrAlreadyAppealed)
}

// 规格再例：s=5 封禁；d1 被推翻后 s=4，降为禁言而不是正常。
func TestBannedThenMutedAfterOverturn(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c1"), []byte("u")))
	must(t, s.Publish(0, []byte("c2"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c1"), 2, []byte("r1")))
	must(t, s.Decide(20, []byte("d2"), []byte("c1"), 3, []byte("r2")))
	must(t, s.Decide(30, []byte("d3"), []byte("c2"), 3, []byte("r1")))
	if got := s.State([]byte("u"), 30); got != strike.StateBanned {
		t.Fatalf("s=1+2+2=5, State=%v, want banned", got)
	}
	wantErr(t, s.Publish(40, []byte("c3"), []byte("u")), decision.ErrAccountRestricted)
	must(t, s.Appeal(110, []byte("a1"), []byte("d1"), []byte("u")))
	must(t, s.Review(120, []byte("a1"), []byte("r3"), true))
	must(t, s.Review(120, []byte("a1"), []byte("r4"), true))
	if got := s.State([]byte("u"), 120); got != strike.StateMuted {
		t.Fatalf("s=4, State=%v, want muted (不是正常)", got)
	}
	if got := levelOf(t, s, "c1"); got != 3 {
		t.Fatalf("level=%d, want 3", got)
	}
}

// 申诉期限恰等允许，超过一刻即拒。
func TestAppealDeadlineExact(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 2, []byte("r1")))
	must(t, s.Decide(10, []byte("d2"), []byte("c"), 2, []byte("r2")))
	must(t, s.Appeal(110, []byte("a1"), []byte("d1"), []byte("u"))) // 恰等允许
	wantErr(t, s.Appeal(111, []byte("a2"), []byte("d2"), []byte("u")), appeal.ErrExpired)
}

// 投票结案机制：2 比 0 提前结案、1 比 1 后第三票定案、回避先于重复投票。
func TestVoteMechanics(t *testing.T) {
	setup := func(t *testing.T) *appeal.System {
		s := mustNew(t, 1000, 100, 500)
		must(t, s.Publish(0, []byte("c"), []byte("u")))
		must(t, s.Decide(10, []byte("d1"), []byte("c"), 3, []byte("r1")))
		must(t, s.Appeal(20, []byte("a1"), []byte("d1"), []byte("u")))
		return s
	}

	t.Run("2比0提前结案后投票被拒", func(t *testing.T) {
		s := setup(t)
		must(t, s.Review(30, []byte("a1"), []byte("r2"), true))
		must(t, s.Review(30, []byte("a1"), []byte("r3"), true)) // 2:0 结案
		wantErr(t, s.Review(31, []byte("a1"), []byte("r4"), false), appeal.ErrClosed)
		if got := outcomeOf(t, s, "a1", 31); got != appeal.OutcomeOverturned {
			t.Fatalf("outcome=%v, want overturned", got)
		}
	})

	t.Run("1比1后第三票定案为维持", func(t *testing.T) {
		s := setup(t)
		must(t, s.Review(30, []byte("a1"), []byte("r2"), true))
		must(t, s.Review(30, []byte("a1"), []byte("r3"), false))
		must(t, s.Review(30, []byte("a1"), []byte("r4"), false)) // 1:2 维持
		if got := outcomeOf(t, s, "a1", 30); got != appeal.OutcomeUpheld {
			t.Fatalf("outcome=%v, want upheld", got)
		}
		if over, _ := s.DecisionOverturned([]byte("d1")); over {
			t.Fatal("d1 must stay effective")
		}
		if got := levelOf(t, s, "c"); got != 3 {
			t.Fatalf("level=%d, want 3", got)
		}
	})

	t.Run("1比1后第三票定案为推翻", func(t *testing.T) {
		s := setup(t)
		must(t, s.Review(30, []byte("a1"), []byte("r2"), false))
		must(t, s.Review(30, []byte("a1"), []byte("r3"), true))
		must(t, s.Review(30, []byte("a1"), []byte("r4"), true)) // 2:1 推翻
		if got := outcomeOf(t, s, "a1", 30); got != appeal.OutcomeOverturned {
			t.Fatalf("outcome=%v, want overturned", got)
		}
		if got := levelOf(t, s, "c"); got != 0 {
			t.Fatalf("level=%d, want 0", got)
		}
	})

	t.Run("回避先于重复投票", func(t *testing.T) {
		s := setup(t)
		// 原审核员第一票即被回避拒绝，因而不可能留下票；
		// 再次尝试仍报须回避（回避检查先于重复投票）。
		wantErr(t, s.Review(30, []byte("a1"), []byte("r1"), true), appeal.ErrRecusal)
		wantErr(t, s.Review(31, []byte("a1"), []byte("r1"), true), appeal.ErrRecusal)
		must(t, s.Review(32, []byte("a1"), []byte("r2"), true))
		wantErr(t, s.Review(33, []byte("a1"), []byte("r2"), false), appeal.ErrDuplicateVote)
		// 已结案优先于须回避。
		must(t, s.Review(34, []byte("a1"), []byte("r3"), true))
		wantErr(t, s.Review(35, []byte("a1"), []byte("r1"), true), appeal.ErrClosed)
	})
}

// 超时恰等 Tmax 视为维持；超时前最后一票仍有效。
func TestTimeoutExactTmax(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 2, []byte("r1")))
	must(t, s.Appeal(110, []byte("a1"), []byte("d1"), []byte("u")))
	must(t, s.Review(609, []byte("a1"), []byte("r2"), true)) // 609 < 110+500，有效
	if got := outcomeOf(t, s, "a1", 609); got != appeal.OutcomePending {
		t.Fatalf("t=609 outcome=%v, want pending", got)
	}
	// 恰等 Tmax：视为维持，Review 报已结案，已投的票作废。
	wantErr(t, s.Review(610, []byte("a1"), []byte("r3"), true), appeal.ErrClosed)
	if got := outcomeOf(t, s, "a1", 610); got != appeal.OutcomeUpheld {
		t.Fatalf("t=610 outcome=%v, want upheld", got)
	}
	if over, _ := s.DecisionOverturned([]byte("d1")); over {
		t.Fatal("d1 must stay effective")
	}
}

// Appeal 拒绝次序：参数非法 > 时钟回退 > 申诉已存在 > 决定不存在 > 无权 > 已申诉过 > 超过期限。
func TestAppealRejectOrder(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 2, []byte("r1")))
	must(t, s.Appeal(20, []byte("a1"), []byte("d1"), []byte("u")))

	cases := []struct {
		name string
		now  int64
		aid  string
		did  string
		by   string
		want error
	}{
		{"空申诉标识+时钟回退报参数非法", 5, "", "d1", "u", appeal.ErrInvalidArgument},
		{"空申诉人", 30, "a9", "d1", "", appeal.ErrInvalidArgument},
		{"时钟回退优先于申诉已存在", 15, "a1", "d1", "u", appeal.ErrClockRegression},
		{"申诉已存在优先于决定不存在", 30, "a1", "ghost", "u", appeal.ErrAppealExists},
		{"决定不存在", 30, "a2", "ghost", "u", appeal.ErrDecisionNotFound},
		{"无权优先于已申诉过", 30, "a2", "d1", "v", appeal.ErrNotCreator},
		{"已申诉过优先于超过期限", 200, "a2", "d1", "u", appeal.ErrAlreadyAppealed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Appeal(tc.now, []byte(tc.aid), []byte(tc.did), []byte(tc.by))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	// 超过期限：d2 在 t=30 作出，t=131 申诉超期。
	must(t, s.Decide(30, []byte("d2"), []byte("c"), 2, []byte("r2")))
	wantErr(t, s.Appeal(131, []byte("a3"), []byte("d2"), []byte("u")), appeal.ErrExpired)
	// 被拒操作不推进时钟：now=30 的操作仍被接受。
	must(t, s.Appeal(30, []byte("a4"), []byte("d2"), []byte("u")))
}

// Review 拒绝次序：参数非法 > 时钟回退 > 申诉不存在 > 已结案 > 须回避 > 重复投票。
func TestReviewRejectOrder(t *testing.T) {
	s := mustNew(t, 1000, 100, 500)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 2, []byte("r1")))
	must(t, s.Appeal(20, []byte("a1"), []byte("d1"), []byte("u")))

	cases := []struct {
		name     string
		now      int64
		aid      string
		reviewer string
		want     error
	}{
		{"空投票人+时钟回退报参数非法", 15, "a1", "", appeal.ErrInvalidArgument},
		{"时钟回退优先于申诉不存在", 15, "ghost", "r2", appeal.ErrClockRegression},
		{"申诉不存在", 30, "ghost", "r2", appeal.ErrAppealNotFound},
		{"须回避", 30, "a1", "r1", appeal.ErrRecusal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Review(tc.now, []byte(tc.aid), []byte(tc.reviewer), true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	must(t, s.Review(30, []byte("a1"), []byte("r2"), true))
	wantErr(t, s.Review(31, []byte("a1"), []byte("r2"), true), appeal.ErrDuplicateVote)
	must(t, s.Review(32, []byte("a1"), []byte("r3"), true)) // 2:0 结案
	wantErr(t, s.Review(33, []byte("a1"), []byte("r4"), true), appeal.ErrClosed)
	// 被拒的票不计入：a1 恰以 r2、r3 两票结案。
	if got := outcomeOf(t, s, "a1", 33); got != appeal.OutcomeOverturned {
		t.Fatalf("outcome=%v, want overturned", got)
	}
}

// 被拒绝的操作不改任何状态，含时钟与票数。
func TestRejectedOpsKeepState(t *testing.T) {
	s := mustNew(t, 1000, 100, 5000)
	must(t, s.Publish(0, []byte("c"), []byte("u")))
	must(t, s.Decide(10, []byte("d1"), []byte("c"), 3, []byte("r1")))
	must(t, s.Appeal(20, []byte("a1"), []byte("d1"), []byte("u")))
	// 大 now 的被拒操作不推进时钟。
	wantErr(t, s.Review(1000, []byte("a1"), []byte("r1"), true), appeal.ErrRecusal)
	wantErr(t, s.Appeal(2000, []byte("a9"), []byte("ghost"), []byte("u")), appeal.ErrDecisionNotFound)
	wantErr(t, s.Publish(3000, []byte("c"), []byte("u")), decision.ErrContentExists)
	wantErr(t, s.Decide(4000, []byte("d1"), []byte("c"), 2, []byte("r2")), decision.ErrDecisionExists)
	// 时钟仍停在 20：now=30 的操作被接受。
	must(t, s.Review(30, []byte("a1"), []byte("r2"), true))
	must(t, s.Review(30, []byte("a1"), []byte("r3"), true))
	// r1 的被拒票未计入：恰两票推翻结案。
	if got := outcomeOf(t, s, "a1", 30); got != appeal.OutcomeOverturned {
		t.Fatalf("outcome=%v, want overturned", got)
	}
	if got := s.State([]byte("u"), 30); got != strike.StateNormal {
		t.Fatalf("State=%v, want normal (推翻后计分撤销)", got)
	}
}

// ---------- 朴素模拟：每次全量重算，与实现逐步对照 ----------

type nDecision struct {
	content, reviewer    string
	level                int
	now                  int64
	overturned, appealed bool
}

type nAppeal struct {
	decisionID         string
	submit             int64
	votes              []nVote
	closed, overturned bool
}

type nVote struct {
	reviewer string
	overturn bool
}

type naive struct {
	P, A, T   int64
	maxNow    int64
	contents  map[string]string // content -> creator
	decisions map[string]*nDecision
	appeals   map[string]*nAppeal
}

func newNaive(p, a, tmax int64) *naive {
	return &naive{
		P: p, A: a, T: tmax, maxNow: -1,
		contents:  make(map[string]string),
		decisions: make(map[string]*nDecision),
		appeals:   make(map[string]*nAppeal),
	}
}

func (n *naive) state(creator string, now int64) strike.State {
	sum := 0
	for _, d := range n.decisions {
		if n.contents[d.content] != creator || d.overturned {
			continue
		}
		if d.now <= now && now < d.now+n.P {
			sum += d.level - 1
		}
	}
	switch {
	case sum >= 5:
		return strike.StateBanned
	case sum >= 3:
		return strike.StateMuted
	default:
		return strike.StateNormal
	}
}

func (n *naive) level(content string) int {
	lvl := 0
	for _, d := range n.decisions {
		if d.content == content && !d.overturned && d.level > lvl {
			lvl = d.level
		}
	}
	return lvl
}

func (n *naive) outcome(aid string, now int64) appeal.Outcome {
	a := n.appeals[aid]
	if a.closed {
		if a.overturned {
			return appeal.OutcomeOverturned
		}
		return appeal.OutcomeUpheld
	}
	if now >= a.submit+n.T {
		return appeal.OutcomeUpheld
	}
	return appeal.OutcomePending
}

func (n *naive) publish(now int64, c, u string) (error, string) {
	if !strike.ValidNow(now) || c == "" || u == "" {
		return appeal.ErrInvalidArgument, "reject: 参数非法"
	}
	if now < n.maxNow {
		return appeal.ErrClockRegression, "reject: 时钟回退"
	}
	if _, ok := n.contents[c]; ok {
		return decision.ErrContentExists, "reject: 内容已存在"
	}
	if n.state(u, now) != strike.StateNormal {
		return decision.ErrAccountRestricted, "reject: 账号受限"
	}
	n.contents[c] = u
	n.maxNow = now
	return nil, "accept"
}

func (n *naive) decide(now int64, id, c string, level int, r string) (error, string) {
	if !strike.ValidNow(now) || id == "" || c == "" || r == "" || level < 1 || level > 3 {
		return appeal.ErrInvalidArgument, "reject: 参数非法"
	}
	if now < n.maxNow {
		return appeal.ErrClockRegression, "reject: 时钟回退"
	}
	if _, ok := n.decisions[id]; ok {
		return decision.ErrDecisionExists, "reject: 决定已存在"
	}
	if _, ok := n.contents[c]; !ok {
		return decision.ErrContentNotFound, "reject: 内容不存在"
	}
	n.decisions[id] = &nDecision{content: c, reviewer: r, level: level, now: now}
	n.maxNow = now
	return nil, "accept"
}

func (n *naive) appeal(now int64, aid, did, by string) (error, string) {
	if !strike.ValidNow(now) || aid == "" || did == "" || by == "" {
		return appeal.ErrInvalidArgument, "reject: 参数非法"
	}
	if now < n.maxNow {
		return appeal.ErrClockRegression, "reject: 时钟回退"
	}
	if _, ok := n.appeals[aid]; ok {
		return appeal.ErrAppealExists, "reject: 申诉已存在"
	}
	d, ok := n.decisions[did]
	if !ok {
		return appeal.ErrDecisionNotFound, "reject: 决定不存在"
	}
	if n.contents[d.content] != by {
		return appeal.ErrNotCreator, "reject: 无权"
	}
	if d.appealed {
		return appeal.ErrAlreadyAppealed, "reject: 已申诉过"
	}
	if now > d.now+n.A {
		return appeal.ErrExpired, "reject: 超过期限"
	}
	d.appealed = true
	n.appeals[aid] = &nAppeal{decisionID: did, submit: now}
	n.maxNow = now
	return nil, "accept"
}

func (n *naive) review(now int64, aid, r string, overturn bool) (error, string) {
	if !strike.ValidNow(now) || aid == "" || r == "" {
		return appeal.ErrInvalidArgument, "reject: 参数非法"
	}
	if now < n.maxNow {
		return appeal.ErrClockRegression, "reject: 时钟回退"
	}
	a, ok := n.appeals[aid]
	if !ok {
		return appeal.ErrAppealNotFound, "reject: 申诉不存在"
	}
	if a.closed || now >= a.submit+n.T {
		return appeal.ErrClosed, "reject: 已结案"
	}
	d := n.decisions[a.decisionID]
	if d.reviewer == r {
		return appeal.ErrRecusal, "reject: 须回避"
	}
	for _, v := range a.votes {
		if v.reviewer == r {
			return appeal.ErrDuplicateVote, "reject: 重复投票"
		}
	}
	a.votes = append(a.votes, nVote{reviewer: r, overturn: overturn})
	var over, keep int
	for _, v := range a.votes {
		if v.overturn {
			over++
		} else {
			keep++
		}
	}
	if over == 2 {
		a.closed, a.overturned = true, true
		d.overturned = true
	} else if keep == 2 {
		a.closed = true
	}
	n.maxNow = now
	return nil, "accept"
}

// ---------- 随机操作序列：实现 vs 朴素模拟 ----------

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

// runRandomSeq 执行一条确定性随机操作序列，逐步与朴素模拟对照；
// rec 非 nil 时记录每步签名（供重放一致性测试）。返回路径覆盖统计。
func runRandomSeq(t *testing.T, seed int64, rec *[]string) (stats map[string]int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed*7919 + 42))
	period := 1 + rng.Int63n(50)
	window := 1 + rng.Int63n(30)
	timeout := 1 + rng.Int63n(120)
	sys, err := appeal.New(period, window, timeout)
	if err != nil {
		t.Fatalf("appeal.New: %v", err)
	}
	nv := newNaive(period, window, timeout)
	creators := []string{"u0", "u1", "u2"}
	reviewers := []string{"r0", "r1", "r2", "r3"}
	var contents, decisionIDs, appealIDs []string
	now := int64(0)
	ops := 15 + rng.Intn(25)
	t.Logf("seq=%d P=%d A=%d Tmax=%d ops=%d", seed, period, window, timeout, ops)

	check := func(i int, desc string, got, want error, reason string) {
		t.Helper()
		sig := fmt.Sprintf("op=%02d now=%d %s => %v", i, now, desc, got)
		if rec != nil {
			*rec = append(*rec, sig)
		}
		t.Logf("seq=%d %s | 判定依据: %s", seed, sig, reason)
		if !errors.Is(got, want) {
			t.Fatalf("seq=%d %s: err=%v, naive want %v (%s)", seed, desc, got, want, reason)
		}
	}
	compareAll := func(i int) {
		t.Helper()
		for _, u := range creators {
			if got, want := sys.State([]byte(u), now), nv.state(u, now); got != want {
				t.Fatalf("seq=%d op=%d State(%s,%d)=%v, naive want %v", seed, i, u, now, got, want)
			}
		}
		for _, c := range contents {
			got, err := sys.EffectiveLevel([]byte(c))
			if err != nil {
				t.Fatalf("seq=%d op=%d EffectiveLevel(%s): %v", seed, i, c, err)
			}
			if want := nv.level(c); got != want {
				t.Fatalf("seq=%d op=%d level(%s)=%d, naive want %d", seed, i, c, got, want)
			}
		}
		for _, aid := range appealIDs {
			got, err := sys.Outcome([]byte(aid), now)
			if err != nil {
				t.Fatalf("seq=%d op=%d Outcome(%s): %v", seed, i, aid, err)
			}
			if want := nv.outcome(aid, now); got != want {
				t.Fatalf("seq=%d op=%d outcome(%s)=%v, naive want %v", seed, i, aid, got, want)
			}
		}
	}

	for i := 0; i < ops; i++ {
		now += rng.Int63n(8)
		if rng.Intn(6) == 0 {
			now += rng.Int63n(120) // 偶发大跳变，触发计分过期与申诉超时
		}
		switch rng.Intn(7) {
		case 0:
			c := fmt.Sprintf("c%d", rng.Intn(5))
			u := creators[rng.Intn(len(creators))]
			desc := fmt.Sprintf("Publish(%s,%s)", c, u)
			got := sys.Publish(now, []byte(c), []byte(u))
			want, reason := nv.publish(now, c, u)
			check(i, desc, got, want, reason)
			if want == nil {
				contents = appendUnique(contents, c)
			}
		case 1, 2:
			id := fmt.Sprintf("d%d", rng.Intn(8))
			c := fmt.Sprintf("c%d", rng.Intn(5))
			if len(contents) > 0 && rng.Intn(2) == 0 {
				c = contents[rng.Intn(len(contents))] // 偏向已有内容
			}
			lv := 1 + rng.Intn(3)
			r := reviewers[rng.Intn(len(reviewers))]
			desc := fmt.Sprintf("Decide(%s,%s,lv%d,%s)", id, c, lv, r)
			got := sys.Decide(now, []byte(id), []byte(c), lv, []byte(r))
			want, reason := nv.decide(now, id, c, lv, r)
			check(i, desc, got, want, reason)
			if want == nil {
				decisionIDs = appendUnique(decisionIDs, id)
			}
		case 3:
			aid := fmt.Sprintf("a%d", rng.Intn(6))
			did := fmt.Sprintf("d%d", rng.Intn(8))
			by := creators[rng.Intn(len(creators))]
			if len(decisionIDs) > 0 && rng.Intn(2) == 0 {
				did = decisionIDs[rng.Intn(len(decisionIDs))] // 偏向已有决定
				by = nv.contents[nv.decisions[did].content]   // 偏向有权申诉人
			}
			desc := fmt.Sprintf("Appeal(%s,%s,%s)", aid, did, by)
			got := sys.Appeal(now, []byte(aid), []byte(did), []byte(by))
			want, reason := nv.appeal(now, aid, did, by)
			check(i, desc, got, want, reason)
			if want == nil {
				appealIDs = appendUnique(appealIDs, aid)
			}
		case 4, 5:
			aid := fmt.Sprintf("a%d", rng.Intn(6))
			if len(appealIDs) > 0 && rng.Intn(4) > 0 {
				aid = appealIDs[rng.Intn(len(appealIDs))] // 偏向已有申诉
			}
			r := reviewers[rng.Intn(len(reviewers))]
			overturn := rng.Intn(2) == 0
			desc := fmt.Sprintf("Review(%s,%s,%v)", aid, r, overturn)
			got := sys.Review(now, []byte(aid), []byte(r), overturn)
			want, reason := nv.review(now, aid, r, overturn)
			check(i, desc, got, want, reason)
		default:
			if rec != nil {
				*rec = append(*rec, fmt.Sprintf("op=%02d now=%d query", i, now))
			}
		}
		compareAll(i)
	}
	stats = map[string]int{}
	for _, a := range nv.appeals {
		switch {
		case a.closed && a.overturned:
			stats["overturned"]++
		case a.closed:
			stats["upheldByVotes"]++
		case now >= a.submit+nv.T:
			stats["timeoutUpheld"]++
		default:
			stats["pending"]++
		}
		stats["appeals"]++
	}
	for _, d := range nv.decisions {
		if d.overturned {
			stats["decisionsOverturned"]++
		}
	}
	stats["decisions"] = len(nv.decisions)
	stats["contents"] = len(nv.contents)
	return stats
}

// 1500 组随机操作序列与全量重算的朴素模拟对照。
func TestRandomAgainstNaive(t *testing.T) {
	total := map[string]int{}
	for seq := int64(0); seq < 1500; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq%d", seq), func(t *testing.T) {
			for k, v := range runRandomSeq(t, seq, nil) {
				total[k] += v
			}
		})
	}
	t.Logf("coverage: %v", total)
	for _, k := range []string{"appeals", "overturned", "upheldByVotes", "timeoutUpheld", "decisionsOverturned"} {
		if total[k] == 0 {
			t.Fatalf("random sequences never exercised path %q", k)
		}
	}
}

// 相同操作序列重放结果相同。
func TestReplayDeterministic(t *testing.T) {
	for seed := int64(5000); seed < 5005; seed++ {
		var r1, r2 []string
		runRandomSeq(t, seed, &r1)
		runRandomSeq(t, seed, &r2)
		if len(r1) != len(r2) {
			t.Fatalf("seed=%d replay length %d != %d", seed, len(r1), len(r2))
		}
		for i := range r1 {
			if r1[i] != r2[i] {
				t.Fatalf("seed=%d op=%d replay mismatch:\n%s\n%s", seed, i, r1[i], r2[i])
			}
		}
	}
}

// 并发调用等价于某个串行顺序（配合 -race；只断言无竞态无panic）。
func TestConcurrentOps(t *testing.T) {
	s := mustNew(t, 1e9, 1e9, 1e9)
	var now atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			u := fmt.Sprintf("u%d", g%3)
			for i := 0; i < 100; i++ {
				n := now.Add(1)
				switch i % 6 {
				case 0:
					_ = s.Publish(n, []byte(fmt.Sprintf("c%d-%d", g, i)), []byte(u))
				case 1:
					_ = s.Decide(n, []byte(fmt.Sprintf("d%d-%d", g, i)),
						[]byte(fmt.Sprintf("c%d-%d", g, i-1)), 1+i%3, []byte("r0"))
				case 2:
					_ = s.Appeal(n, []byte(fmt.Sprintf("a%d-%d", g, i)),
						[]byte(fmt.Sprintf("d%d-%d", g, i-1)), []byte(u))
				case 3:
					_ = s.Review(n, []byte(fmt.Sprintf("a%d-%d", g, i-2)),
						[]byte(fmt.Sprintf("r%d", g)), i%2 == 0)
				case 4:
					_ = s.State([]byte(u), n)
				default:
					_, _ = s.EffectiveLevel([]byte(fmt.Sprintf("c%d-%d", g, i)))
				}
			}
		}(g)
	}
	wg.Wait()
}
