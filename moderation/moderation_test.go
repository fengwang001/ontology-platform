package moderation_test

import (
	"errors"
	"testing"

	"ontology/moderation"
)

func newSys(t *testing.T, p, a, tmax int64) *moderation.System {
	t.Helper()
	s, err := moderation.New(p, a, tmax)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", p, a, tmax, err)
	}
	return s
}

func wantErr(t *testing.T, op string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", op, got, want)
	}
}

func wantOK(t *testing.T, op string, got error) {
	t.Helper()
	if got != nil {
		t.Fatalf("%s: got %v, want nil", op, got)
	}
}

func wantState(t *testing.T, s *moderation.System, creator string, now int64, want moderation.State) {
	t.Helper()
	got, err := s.State(creator, now)
	if err != nil {
		t.Fatalf("State(%s,%d): %v", creator, now, err)
	}
	if got != want {
		t.Fatalf("State(%s,%d) = %v, want %v", creator, now, got, want)
	}
}

func wantLevel(t *testing.T, s *moderation.System, content string, want int) {
	t.Helper()
	got, err := s.Level(content)
	if err != nil {
		t.Fatalf("Level(%s): %v", content, err)
	}
	if got != want {
		t.Fatalf("Level(%s) = %d, want %d", content, got, want)
	}
}

func wantAppeal(t *testing.T, s *moderation.System, id string, now int64, wantClosed, wantOverturned bool) {
	t.Helper()
	closed, overturned, err := s.AppealStatus(id, now)
	if err != nil {
		t.Fatalf("AppealStatus(%s,%d): %v", id, now, err)
	}
	if closed != wantClosed || overturned != wantOverturned {
		t.Fatalf("AppealStatus(%s,%d) = (%v,%v), want (%v,%v)",
			id, now, closed, overturned, wantClosed, wantOverturned)
	}
}

// 规格示例一：P=1000, A=100, Tmax=500 的完整时间线。
func TestSpecExampleMain(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish c", s.Publish(0, "c", "u"))
	wantOK(t, "Decide d1", s.Decide(10, "d1", "c", 2, "r1"))
	wantLevel(t, s, "c", 2)
	wantState(t, s, "u", 10, moderation.Normal) // s=1
	wantOK(t, "Decide d2", s.Decide(20, "d2", "c", 3, "r2"))
	wantLevel(t, s, "c", 3)
	wantState(t, s, "u", 20, moderation.Muted) // s=1+2=3
	wantErr(t, "Publish while muted", s.Publish(20, "c2", "u"), moderation.ErrAccountRestricted)

	wantOK(t, "Appeal a1 at 110", s.Appeal(110, "a1", "d1", "u"))                               // 110 <= 10+100
	wantErr(t, "Appeal a2 at 121", s.Appeal(121, "a2", "d2", "u"), moderation.ErrAppealExpired) // 121 > 20+100
	wantErr(t, "r1 recused", s.Review(130, "a1", "r1", true), moderation.ErrRecusal)
	wantOK(t, "r3 overturn", s.Review(130, "a1", "r3", true))
	wantOK(t, "r4 uphold", s.Review(130, "a1", "r4", false))
	wantOK(t, "r5 overturn", s.Review(130, "a1", "r5", true)) // 2:1 结案为推翻
	wantAppeal(t, s, "a1", 130, true, true)

	wantLevel(t, s, "c", 3)                       // d2 仍生效，不回落到 0
	wantState(t, s, "u", 130, moderation.Normal)  // s=2
	wantState(t, s, "u", 1019, moderation.Normal) // 1019 < 20+1000，d2 计分仍有效
	wantState(t, s, "u", 1020, moderation.Normal) // 恰到 t+P，s=0
	wantLevel(t, s, "c", 3)                       // 有效级别不随计分过期变化
}

// 规格示例二：超时恰等 Tmax 视为维持；三决定封禁后推翻一个降为禁言。
func TestSpecExampleTimeoutAndBan(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish c", s.Publish(0, "c", "u"))
	wantOK(t, "Publish c2", s.Publish(0, "c2", "u"))
	wantOK(t, "Decide d1", s.Decide(10, "d1", "c", 2, "r1"))
	wantOK(t, "Decide d2", s.Decide(20, "d2", "c", 3, "r2"))
	wantOK(t, "Decide d3", s.Decide(30, "d3", "c2", 3, "r2"))
	wantState(t, s, "u", 30, moderation.Banned) // s=1+2+2=5
	wantErr(t, "Publish while banned", s.Publish(30, "c3", "u"), moderation.ErrAccountRestricted)

	// a1 只收到一票推翻，到 110+500=610 仍未结案 -> 视为维持。
	wantOK(t, "Appeal a1", s.Appeal(110, "a1", "d1", "u"))
	wantOK(t, "r3 overturn", s.Review(120, "a1", "r3", true))
	wantAppeal(t, s, "a1", 609, false, false)
	wantAppeal(t, s, "a1", 610, true, false) // 恰等 Tmax，按维持结案
	wantErr(t, "Review after timeout", s.Review(610, "a1", "r4", true), moderation.ErrAppealClosed)
	wantErr(t, "re-appeal d1", s.Appeal(610, "a9", "d1", "u"), moderation.ErrAlreadyAppealed)
	wantLevel(t, s, "c", 3) // d1 未被推翻

	// 推翻 d3（w=2）：s=5-2=3，仍为禁言；再推翻 d2 才恢复。
	wantOK(t, "Appeal a2", s.Appeal(120, "a2", "d2", "u")) // 恰等 20+100
	wantOK(t, "Appeal a3", s.Appeal(130, "a3", "d3", "u")) // 130 <= 30+100
	wantOK(t, "r3 overturn", s.Review(140, "a3", "r3", true))
	wantOK(t, "r4 overturn", s.Review(140, "a3", "r4", true))
	wantState(t, s, "u", 140, moderation.Muted) // s=1+2=3
	wantOK(t, "r3 overturn", s.Review(150, "a2", "r3", true))
	wantOK(t, "r4 overturn", s.Review(150, "a2", "r4", true))
	wantState(t, s, "u", 150, moderation.Normal) // s=1
	wantLevel(t, s, "c2", 0)
}

// 多条决定取最大值；推翻最高者后回落到次高，而不是归零。
func TestEffectiveLevelFallback(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1 lv1", s.Decide(1, "d1", "c", 1, "r1"))
	wantOK(t, "d2 lv3", s.Decide(2, "d2", "c", 3, "r2"))
	wantOK(t, "d3 lv2", s.Decide(3, "d3", "c", 2, "r3"))
	wantLevel(t, s, "c", 3)

	overturn := func(appealID, decID string, at int64) {
		t.Helper()
		wantOK(t, "Appeal "+appealID, s.Appeal(at, appealID, decID, "u"))
		wantOK(t, "vote1", s.Review(at+10, appealID, "rx", true))
		wantOK(t, "vote2", s.Review(at+10, appealID, "ry", true))
	}
	overturn("a2", "d2", 10) // 推翻最高者
	wantLevel(t, s, "c", 2)
	overturn("a3", "d3", 30)
	wantLevel(t, s, "c", 1)
	overturn("a1", "d1", 50)
	wantLevel(t, s, "c", 0)
}

// level 1 权重为 0，不记分。
func TestLevel1NoScore(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	for i, id := range []string{"d1", "d2", "d3", "d4", "d5"} {
		wantOK(t, "Decide "+id, s.Decide(int64(i+1), id, "c", 1, "r1"))
	}
	wantState(t, s, "u", 100, moderation.Normal)
	wantLevel(t, s, "c", 1)
}

// 计分在 [t, t+P) 内有效，恰到 t+P 即失效。
func TestScoreExpiryBoundary(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1", s.Decide(0, "d1", "c", 3, "r1"))
	wantOK(t, "d2", s.Decide(10, "d2", "c", 3, "r2"))
	wantState(t, s, "u", 999, moderation.Muted)   // s=2+2=4
	wantState(t, s, "u", 1000, moderation.Normal) // d1 恰到 t+P 失效，s=2
	wantState(t, s, "u", 1009, moderation.Normal) // s=2
	wantState(t, s, "u", 1010, moderation.Normal) // 全部过期，s=0
	wantLevel(t, s, "c", 3)                       // 级别不过期
}

// 申诉期限恰等允许，超过一刻即拒。
func TestAppealDeadlineExact(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1", s.Decide(10, "d1", "c", 2, "r1"))
	wantOK(t, "d2", s.Decide(10, "d2", "c", 2, "r2"))
	wantOK(t, "appeal at exactly t+A", s.Appeal(110, "a1", "d1", "u"))
	wantErr(t, "appeal at t+A+1", s.Appeal(111, "a2", "d2", "u"), moderation.ErrAppealExpired)
}

// 1 比 1 之后由第三票定案。
func TestThirdVoteDecides(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1", s.Decide(10, "d1", "c", 3, "r1"))
	wantOK(t, "Appeal", s.Appeal(20, "a1", "d1", "u"))
	wantOK(t, "up", s.Review(30, "a1", "r2", true))
	wantOK(t, "down", s.Review(30, "a1", "r3", false))
	wantAppeal(t, s, "a1", 30, false, false) // 1:1 未结案
	wantOK(t, "third vote", s.Review(30, "a1", "r4", true))
	wantAppeal(t, s, "a1", 30, true, true) // 2:1 推翻
	wantLevel(t, s, "c", 0)
	wantState(t, s, "u", 30, moderation.Normal)
}

// 2 比 0 提前结案，其后的投票报已结案；维持结案同理。
func TestEarlyCloseThenVoteRejected(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1", s.Decide(10, "d1", "c", 3, "r1"))
	wantOK(t, "d2", s.Decide(10, "d2", "c", 2, "r1"))
	wantOK(t, "Appeal a1", s.Appeal(20, "a1", "d1", "u"))
	wantOK(t, "Appeal a2", s.Appeal(20, "a2", "d2", "u"))
	wantOK(t, "up1", s.Review(30, "a1", "r2", true))
	wantOK(t, "up2", s.Review(30, "a1", "r3", true)) // 2:0 结案
	wantAppeal(t, s, "a1", 30, true, true)
	wantErr(t, "third vote rejected", s.Review(30, "a1", "r4", false), moderation.ErrAppealClosed)
	wantOK(t, "down1", s.Review(30, "a2", "r2", false))
	wantOK(t, "down2", s.Review(30, "a2", "r3", false)) // 维持结案
	wantAppeal(t, s, "a2", 30, true, false)
	wantLevel(t, s, "c", 2) // d2 维持生效，d1 被推翻
}

// 须回避先于重复投票：原审核员投几次都报须回避；普通审核员第二票报重复投票。
func TestRecusalBeforeDuplicate(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1", s.Decide(10, "d1", "c", 2, "r1"))
	wantOK(t, "Appeal", s.Appeal(20, "a1", "d1", "u"))
	wantErr(t, "r1 recused", s.Review(30, "a1", "r1", true), moderation.ErrRecusal)
	wantErr(t, "r1 recused again", s.Review(30, "a1", "r1", false), moderation.ErrRecusal)
	wantOK(t, "r2 votes", s.Review(30, "a1", "r2", true))
	wantErr(t, "r2 duplicate", s.Review(30, "a1", "r2", false), moderation.ErrDuplicateVote)
	wantErr(t, "r1 still recused", s.Review(30, "a1", "r1", true), moderation.ErrRecusal)
}

// 各操作的拒绝次序：只报第一个错误。
func TestRejectionOrder(t *testing.T) {
	base := func(t *testing.T) *moderation.System {
		s := newSys(t, 1000, 100, 500)
		wantOK(t, "Publish", s.Publish(0, "c", "u"))
		wantOK(t, "d1", s.Decide(10, "d1", "c", 2, "r1"))
		wantOK(t, "Appeal a1", s.Appeal(20, "a1", "d1", "u"))
		return s // maxNow=20
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, s *moderation.System)
		op    func(s *moderation.System) error
		want  error
	}{
		{"Publish 参数非法>时钟回退", nil,
			func(s *moderation.System) error { return s.Publish(5, "", "u") }, moderation.ErrInvalidParam},
		{"Publish 时钟回退>内容已存在", nil,
			func(s *moderation.System) error { return s.Publish(5, "c", "u") }, moderation.ErrClockRegression},
		{"Publish 内容已存在>账号受限", func(t *testing.T, s *moderation.System) {
			wantOK(t, "d2", s.Decide(21, "d2", "c", 3, "r2")) // s=3，禁言
		}, func(s *moderation.System) error { return s.Publish(22, "c", "u") }, moderation.ErrContentExists},
		{"Publish 账号受限", func(t *testing.T, s *moderation.System) {
			wantOK(t, "d2", s.Decide(21, "d2", "c", 3, "r2"))
		}, func(s *moderation.System) error { return s.Publish(22, "c2", "u") }, moderation.ErrAccountRestricted},

		{"Decide 参数非法>时钟回退", nil,
			func(s *moderation.System) error { return s.Decide(5, "d9", "c", 4, "r2") }, moderation.ErrInvalidParam},
		{"Decide 时钟回退>决定已存在", nil,
			func(s *moderation.System) error { return s.Decide(5, "d1", "c", 2, "r2") }, moderation.ErrClockRegression},
		{"Decide 决定已存在>内容不存在", nil,
			func(s *moderation.System) error { return s.Decide(30, "d1", "nope", 2, "r2") }, moderation.ErrDecisionExists},
		{"Decide 内容不存在", nil,
			func(s *moderation.System) error { return s.Decide(30, "d9", "nope", 2, "r2") }, moderation.ErrContentNotFound},

		{"Appeal 参数非法>时钟回退", nil,
			func(s *moderation.System) error { return s.Appeal(5, "", "d1", "u") }, moderation.ErrInvalidParam},
		{"Appeal 时钟回退>申诉已存在", nil,
			func(s *moderation.System) error { return s.Appeal(5, "a1", "d1", "u") }, moderation.ErrClockRegression},
		{"Appeal 申诉已存在>决定不存在", nil,
			func(s *moderation.System) error { return s.Appeal(30, "a1", "nope", "u") }, moderation.ErrAppealExists},
		{"Appeal 决定不存在>无权", nil,
			func(s *moderation.System) error { return s.Appeal(30, "a2", "nope", "v") }, moderation.ErrDecisionNotFound},
		{"Appeal 无权>已申诉过", nil,
			func(s *moderation.System) error { return s.Appeal(30, "a2", "d1", "v") }, moderation.ErrForbidden},
		{"Appeal 已申诉过>超过期限", nil,
			func(s *moderation.System) error { return s.Appeal(200, "a2", "d1", "u") }, moderation.ErrAlreadyAppealed},
		{"Appeal 超过期限", func(t *testing.T, s *moderation.System) {
			wantOK(t, "d3", s.Decide(20, "d3", "c", 3, "r2"))
		}, func(s *moderation.System) error { return s.Appeal(200, "a3", "d3", "u") }, moderation.ErrAppealExpired},

		{"Review 参数非法>时钟回退", nil,
			func(s *moderation.System) error { return s.Review(5, "", "r2", true) }, moderation.ErrInvalidParam},
		{"Review 时钟回退>申诉不存在", nil,
			func(s *moderation.System) error { return s.Review(5, "nope", "r2", true) }, moderation.ErrClockRegression},
		{"Review 申诉不存在>已结案", nil,
			func(s *moderation.System) error { return s.Review(30, "nope", "r2", true) }, moderation.ErrAppealNotFound},
		{"Review 已结案>须回避", func(t *testing.T, s *moderation.System) {
			wantOK(t, "v1", s.Review(30, "a1", "r2", true))
			wantOK(t, "v2", s.Review(30, "a1", "r3", true))
		}, func(s *moderation.System) error { return s.Review(30, "a1", "r1", true) }, moderation.ErrAppealClosed},
		{"Review 须回避>重复投票", func(t *testing.T, s *moderation.System) {
			wantOK(t, "v1", s.Review(30, "a1", "r2", true))
		}, func(s *moderation.System) error { return s.Review(30, "a1", "r1", true) }, moderation.ErrRecusal},
		{"Review 重复投票", func(t *testing.T, s *moderation.System) {
			wantOK(t, "v1", s.Review(30, "a1", "r2", true))
		}, func(s *moderation.System) error { return s.Review(30, "a1", "r2", false) }, moderation.ErrDuplicateVote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base(t)
			if tc.setup != nil {
				tc.setup(t, s)
			}
			wantErr(t, tc.name, tc.op(s), tc.want)
		})
	}
}

// 被拒绝的操作不改任何状态，含时钟与票数。
func TestRejectedOpsKeepState(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	wantOK(t, "Publish", s.Publish(0, "c", "u"))
	wantOK(t, "d1", s.Decide(10, "d1", "c", 3, "r1"))
	wantOK(t, "Appeal", s.Appeal(20, "a1", "d1", "u"))
	wantOK(t, "r2 up", s.Review(30, "a1", "r2", true))

	// 被拒的票不入账。
	wantErr(t, "dup", s.Review(30, "a1", "r2", true), moderation.ErrDuplicateVote)
	wantErr(t, "recused", s.Review(30, "a1", "r1", true), moderation.ErrRecusal)
	wantErr(t, "no appeal", s.Review(30, "nope", "r3", true), moderation.ErrAppealNotFound)
	wantOK(t, "r3 down", s.Review(30, "a1", "r3", false))
	wantOK(t, "r4 up", s.Review(30, "a1", "r4", true)) // 恰 2:1 结案，说明只有 3 票入账
	wantAppeal(t, s, "a1", 30, true, true)

	// 被拒的操作不推进时钟：now=40 的被拒 Publish 之后，now=35 仍被接受。
	wantErr(t, "rejected publish", s.Publish(40, "c", "u"), moderation.ErrContentExists)
	wantOK(t, "publish at 35", s.Publish(35, "c2", "u"))

	// 被拒的 Appeal 不消耗申诉机会：无权申诉被拒后，本人仍可申诉。
	wantOK(t, "d2", s.Decide(36, "d2", "c", 2, "r1"))
	wantErr(t, "forbidden", s.Appeal(37, "a2", "d2", "v"), moderation.ErrForbidden)
	wantOK(t, "appeal by creator", s.Appeal(38, "a2", "d2", "u"))
}
