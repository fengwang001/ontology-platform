package spectate_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/live"
	"ontology/spectate"
)

func mustNew(t *testing.T, d, m int64) *spectate.Service {
	t.Helper()
	svc, err := spectate.New(d, m)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", d, m, err)
	}
	return svc
}

func mustEmit(t *testing.T, svc *spectate.Service, now int64, k live.Kind) live.Event {
	t.Helper()
	ev, err := svc.Emit(now, k)
	if err != nil {
		t.Fatalf("Emit(%d,%v): %v", now, k, err)
	}
	return ev
}

func must(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func wantErr(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func seqsOf(events []live.Event) []int64 {
	out := []int64{}
	for _, ev := range events {
		out = append(out, ev.Seq)
	}
	return out
}

func mustPull(t *testing.T, svc *spectate.Service, now int64, name string, maxN int64, wantSeqs []int64, wantMore bool) {
	t.Helper()
	out, more, err := svc.Pull(now, name, maxN)
	if err != nil {
		t.Fatalf("Pull(%d,%s,%d): %v", now, name, maxN, err)
	}
	if got := seqsOf(out); !reflect.DeepEqual(got, wantSeqs) || more != wantMore {
		t.Fatalf("Pull(%d,%s,%d) = (seqs %v, more %v), want (%v, %v)",
			now, name, maxN, got, more, wantSeqs, wantMore)
	}
}

func mustLag(t *testing.T, svc *spectate.Service, now int64, name string, want int64) {
	t.Helper()
	n, err := svc.Lag(now, name)
	if err != nil {
		t.Fatalf("Lag(%d,%s): %v", now, name, err)
	}
	if n != want {
		t.Fatalf("Lag(%d,%s) = %d, want %d", now, name, n, want)
	}
}

// emitSpecStream 生成题目示例中的事件流 e1..e5。
func emitSpecStream(t *testing.T, svc *spectate.Service) {
	t.Helper()
	mustEmit(t, svc, 1000, live.Normal) // e1
	mustEmit(t, svc, 2000, live.Hidden) // e2
	mustEmit(t, svc, 5000, live.Normal) // e3
	mustEmit(t, svc, 9000, live.Normal) // e4
	mustEmit(t, svc, 10000, live.End)   // e5
}

// TestSpecExample 完整走查题目示例（D=3000）。
func TestSpecExample(t *testing.T) {
	svc := mustNew(t, 3000, 10)
	mustEmit(t, svc, 1000, live.Normal) // e1
	mustEmit(t, svc, 2000, live.Hidden) // e2
	// 裁判 j 在 now=2000 零延迟拿到 [e1,e2]（含 Hidden）。
	must(t, svc.Join(2000, "j", true), "join judge")
	mustPull(t, svc, 2000, "j", 1000, []int64{1, 2}, false)

	must(t, svc.Join(3500, "v", false), "join v")
	mustPull(t, svc, 4000, "v", 1000, []int64{1}, false)  // cutoff=1000
	mustEmit(t, svc, 5000, live.Normal)                   // e3
	mustPull(t, svc, 5500, "v", 1000, []int64{}, false)   // cutoff=2500，e2 被跳过
	mustEmit(t, svc, 9000, live.Normal)                   // e4
	mustEmit(t, svc, 10000, live.End)                     // e5，tE=10000
	mustPull(t, svc, 10500, "v", 1000, []int64{3}, false) // cutoff=8000
	mustPull(t, svc, 11499, "v", 1000, []int64{4}, false) // cutoff=9998
	mustPull(t, svc, 11500, "v", 1000, []int64{5}, false) // cutoff=10000=tE
	// w 赛后加入，从头拿到全部五条（含 e2）。
	must(t, svc.Join(11500, "w", false), "join w")
	mustPull(t, svc, 11500, "w", 1000, []int64{1, 2, 3, 4, 5}, false)
	// v 已越过 e2，赛后也收不到它。
	mustPull(t, svc, 12000, "v", 1000, []int64{}, false)
}

// TestLagExample 走查题目第二例：u 加入后不拉取，Lag 不移动游标。
func TestLagExample(t *testing.T) {
	svc := mustNew(t, 3000, 10)
	mustEmit(t, svc, 1000, live.Normal)
	mustEmit(t, svc, 2000, live.Hidden)
	must(t, svc.Join(3500, "u", false), "join u")
	mustEmit(t, svc, 5000, live.Normal)
	mustLag(t, svc, 5500, "u", 1) // 只有 e1；e2 此刻不可投递
	mustEmit(t, svc, 9000, live.Normal)
	mustEmit(t, svc, 10000, live.End)
	mustLag(t, svc, 11500, "u", 5) // 游标未越过 e2，此刻它已可投递
}

// TestMaxNTruncationKeepsHidden 走查题目第二例的 maxN=1 情形：
// 截断即停、不越过 Hidden，赛后仍可拿到它。
func TestMaxNTruncationKeepsHidden(t *testing.T) {
	svc := mustNew(t, 3000, 10)
	mustEmit(t, svc, 1000, live.Normal)
	mustEmit(t, svc, 2000, live.Hidden)
	must(t, svc.Join(3500, "u", false), "join u")
	mustEmit(t, svc, 5000, live.Normal)
	mustPull(t, svc, 5500, "u", 1, []int64{1}, false) // 游标停在 e1，未越过 e2
	mustEmit(t, svc, 9000, live.Normal)
	mustEmit(t, svc, 10000, live.End)
	mustPull(t, svc, 11500, "u", 1000, []int64{2, 3, 4, 5}, false) // 赛后补到 e2
}

// TestCutoffEquality 事件时刻等于 cutoff 时可见（取等可见）。
func TestCutoffEquality(t *testing.T) {
	svc := mustNew(t, 3000, 10)
	mustEmit(t, svc, 2000, live.Normal)
	must(t, svc.Join(2000, "v", false), "join v")
	mustPull(t, svc, 4999, "v", 1000, []int64{}, false) // cutoff=1999 < 2000
	mustPull(t, svc, 5000, "v", 1000, []int64{1}, false)
	mustEmit(t, svc, 5000, live.End) // tE=5000，与当前 now 同刻
	// 赛后 cutoff=min(5000, 2*now-5000-3000)：now=5000 时仅 2000，End 还不可见。
	mustPull(t, svc, 5000, "v", 1000, []int64{}, false)
	mustPull(t, svc, 6499, "v", 1000, []int64{}, false)  // cutoff=4998
	mustPull(t, svc, 6500, "v", 1000, []int64{2}, false) // cutoff=5000=tE，追平
}

// TestHiddenSkipNoRedelivery 奇数 D：追平前一毫秒 cutoff=tE-1，Hidden 被跳过；
// 追平后 cutoff==tE，但游标已越过，不再补发。新加入者仍能拿到它。
func TestHiddenSkipNoRedelivery(t *testing.T) {
	svc := mustNew(t, 3001, 10) // tE=10000 时追平时刻为 10000+ceil(3001/2)=11501
	mustEmit(t, svc, 500, live.Hidden)
	mustEmit(t, svc, 800, live.Normal)
	mustEmit(t, svc, 10000, live.End)
	must(t, svc.Join(10000, "v", false), "join v")
	// now=11500：cutoff=2*11500-10000-3001=9999 < tE，Hidden 不可投递，被跳过。
	mustPull(t, svc, 11500, "v", 1000, []int64{2}, false)
	// now=11501：cutoff=10000=tE 已追平，但 seq=1 的 Hidden 已被越过，不补发。
	mustPull(t, svc, 11501, "v", 1000, []int64{3}, false)
	mustPull(t, svc, 11502, "v", 1000, []int64{}, false)
	// 追平后才加入的 w 从头开始，拿得到该 Hidden。
	must(t, svc.Join(11502, "w", false), "join w")
	mustPull(t, svc, 11502, "w", 1000, []int64{1, 2, 3}, false)
}

// TestTruncationDoesNotCrossHidden maxN 截断时游标停在最后一条投递事件上，
// 不向后跳过 Hidden；赛后追平后该 Hidden 仍可投递。
func TestTruncationDoesNotCrossHidden(t *testing.T) {
	svc := mustNew(t, 1000, 10) // tE=1000，追平时刻 now=1500
	mustEmit(t, svc, 100, live.Normal)
	mustEmit(t, svc, 200, live.Normal)
	mustEmit(t, svc, 300, live.Hidden)
	mustEmit(t, svc, 1000, live.End) // e4，tE=1000
	must(t, svc.Join(1000, "v", false), "join v")
	// now=1200：cutoff=min(1000, 2400-1000-1000)=400，e1、e2 可投递，e3 是 Hidden。
	// maxN=2 截断后游标停在 e2，不向后越过 e3。
	mustPull(t, svc, 1200, "v", 2, []int64{1, 2}, false)
	// now=1500：cutoff=1000=tE 追平，e3（Hidden）仍可投递——证明截断时未越过它。
	mustPull(t, svc, 1500, "v", 1000, []int64{3, 4}, false)
}

// TestMoreAfterTruncation maxN 截断后游标之后仍有可投递事件时 More 为真。
func TestMoreAfterTruncation(t *testing.T) {
	svc := mustNew(t, 0, 10)
	for i := int64(1); i <= 4; i++ {
		mustEmit(t, svc, i, live.Normal)
	}
	must(t, svc.Join(4, "v", false), "join v")
	mustPull(t, svc, 10, "v", 2, []int64{1, 2}, true)
	mustPull(t, svc, 10, "v", 2, []int64{3, 4}, false)
	mustPull(t, svc, 10, "v", 2, []int64{}, false)
}

// TestJudgeZeroDelay 裁判 cutoff 恒为 now，赛前即可拿到 Hidden，且不受模式限制。
func TestJudgeZeroDelay(t *testing.T) {
	svc := mustNew(t, 1_000_000_000, 1)
	mustEmit(t, svc, 5, live.Normal)
	mustEmit(t, svc, 6, live.Hidden)
	must(t, svc.Join(6, "j", true), "join judge")
	mustPull(t, svc, 6, "j", 1000, []int64{1, 2}, false)
	must(t, svc.SetMode(7, spectate.Off), "set Off")
	must(t, svc.Join(8, "j2", true), "judge joins in Off mode")
	mustPull(t, svc, 8, "j2", 1000, []int64{1, 2}, false)
	mustEmit(t, svc, 9, live.Hidden)
	mustEmit(t, svc, 10, live.End)
	mustPull(t, svc, 10, "j2", 1000, []int64{3, 4}, false)
}

// TestModeSwitchRemovals 模式切换的移除语义：FriendsOnly 移除非好友非裁判，
// Off 移除全部非裁判，裁判始终保留；被移除者再 Join 从头开始。
func TestModeSwitchRemovals(t *testing.T) {
	svc := mustNew(t, 0, 10)
	mustEmit(t, svc, 1, live.Normal)
	must(t, svc.Befriend(2, "f"), "befriend f")
	must(t, svc.Join(3, "f", false), "join f")
	must(t, svc.Join(3, "g", false), "join g")
	must(t, svc.Join(3, "j", true), "join judge")
	mustPull(t, svc, 4, "g", 1000, []int64{1}, false) // g 的游标前进到 1

	must(t, svc.SetMode(5, spectate.FriendsOnly), "set FriendsOnly")
	_, _, pullErr := svc.Pull(6, "g", 1)
	wantErr(t, pullErr, spectate.ErrNotWatching, "g removed")
	mustPull(t, svc, 6, "f", 1000, []int64{1}, false) // f 仍在，从头未动
	mustPull(t, svc, 6, "j", 1000, []int64{1}, false) // 裁判不受模式限制
	wantErr(t, svc.Join(7, "g", false), spectate.ErrNotFriend, "g rejoin w/o friend")

	mustEmit(t, svc, 8, live.Normal)
	must(t, svc.SetMode(9, spectate.Off), "set Off")
	_, _, pullErr2 := svc.Pull(10, "f", 1)
	wantErr(t, pullErr2, spectate.ErrNotWatching, "f removed by Off")
	mustPull(t, svc, 10, "j", 1000, []int64{2}, false) // 裁判保留且游标连续
	wantErr(t, svc.Join(11, "x", false), spectate.ErrModeOff, "join in Off")

	must(t, svc.SetMode(12, spectate.Public), "set Public")
	must(t, svc.Join(13, "g", false), "g rejoins")
	mustPull(t, svc, 14, "g", 1000, []int64{1, 2}, false) // 游标作废，从头开始
}

// TestUnfriendRemovesInFriendsOnly FriendsOnly 模式下 Unfriend 立即移除该观战者；
// 其他模式只清除好友标记；裁判不被移除。
func TestUnfriendRemovesInFriendsOnly(t *testing.T) {
	svc := mustNew(t, 0, 10)
	must(t, svc.Befriend(1, "a"), "befriend a")
	must(t, svc.Befriend(1, "b"), "befriend b")
	must(t, svc.Befriend(1, "j"), "befriend judge")
	must(t, svc.SetMode(2, spectate.FriendsOnly), "set FriendsOnly")
	must(t, svc.Join(3, "a", false), "join a")
	must(t, svc.Join(3, "b", false), "join b")
	must(t, svc.Join(3, "j", true), "join judge")
	must(t, svc.Unfriend(4, "a"), "unfriend a")
	_, _, pullErr := svc.Pull(5, "a", 1)
	wantErr(t, pullErr, spectate.ErrNotWatching, "a removed")
	mustLag(t, svc, 5, "b", 0) // b 仍在
	must(t, svc.Unfriend(6, "j"), "unfriend judge")
	mustLag(t, svc, 7, "j", 0) // 裁判不被移除
	// Public 模式下 Unfriend 不移除。
	must(t, svc.SetMode(8, spectate.Public), "set Public")
	must(t, svc.Join(9, "c", false), "join c")
	must(t, svc.Unfriend(10, "c"), "unfriend c")
	mustLag(t, svc, 11, "c", 0)
}

// TestCapacityExcludesJudges 非裁判人数上限为 M，裁判不占名额。
func TestCapacityExcludesJudges(t *testing.T) {
	svc := mustNew(t, 0, 1)
	must(t, svc.Join(1, "j1", true), "judge 1")
	must(t, svc.Join(1, "j2", true), "judge 2")
	must(t, svc.Join(2, "a", false), "non-judge a")
	wantErr(t, svc.Join(3, "b", false), spectate.ErrFull, "second non-judge")
	must(t, svc.Leave(4, "j1"), "judge leaves")
	wantErr(t, svc.Join(5, "b", false), spectate.ErrFull, "judge leave frees nothing")
	must(t, svc.Leave(6, "a"), "a leaves")
	must(t, svc.Join(7, "b", false), "b joins after slot freed")
}

// TestRejectionOrder 验证 Join 与 Pull/Lag 的拒绝次序。
func TestRejectionOrder(t *testing.T) {
	svc := mustNew(t, 0, 1)
	must(t, svc.Befriend(10, "c"), "befriend c")
	must(t, svc.SetMode(20, spectate.FriendsOnly), "set FriendsOnly")
	must(t, svc.Befriend(30, "a"), "befriend a")
	must(t, svc.Join(40, "a", false), "join a fills M=1")
	mustEmit(t, svc, 100, live.Normal) // 时钟推进到 100

	// Join 拒绝次序：参数非法 > 时钟回退 > 已在观战 > Off > 非好友 > 满。
	wantErr(t, svc.Join(50, "", false), spectate.ErrInvalidParam, "empty name beats clock")
	wantErr(t, svc.Join(50, "b", false), spectate.ErrClockRegression, "clock beats friend check")
	wantErr(t, svc.Join(100, "a", false), spectate.ErrAlreadyWatching, "already beats friend check")
	wantErr(t, svc.Join(100, "b", false), spectate.ErrNotFriend, "friend check beats capacity")
	wantErr(t, svc.Join(100, "c", false), spectate.ErrFull, "capacity last")
	must(t, svc.SetMode(110, spectate.Off), "set Off")
	wantErr(t, svc.Join(110, "b", false), spectate.ErrModeOff, "Off beats friend check")
	wantErr(t, svc.Join(110, "c", false), spectate.ErrModeOff, "Off beats capacity")

	// Pull/Lag：参数非法 > 时钟回退 > 不存在。
	_, _, err := svc.Pull(50, "nobody", 0)
	wantErr(t, err, spectate.ErrInvalidParam, "bad maxN beats clock")
	_, _, err = svc.Pull(50, "nobody", 1)
	wantErr(t, err, spectate.ErrClockRegression, "clock beats not-watching")
	_, _, err = svc.Pull(110, "nobody", 1)
	wantErr(t, err, spectate.ErrNotWatching, "not watching")
	_, _, err = svc.Pull(110, "nobody", 1001)
	wantErr(t, err, spectate.ErrInvalidParam, "maxN > 1000")
	_, err = svc.Lag(50, "nobody")
	wantErr(t, err, spectate.ErrClockRegression, "Lag clock beats not-watching")
	_, err = svc.Lag(110, "nobody")
	wantErr(t, err, spectate.ErrNotWatching, "Lag not watching")
	_, err = svc.Lag(110, "")
	wantErr(t, err, spectate.ErrInvalidParam, "Lag empty name")

	// Leave：不存在。
	wantErr(t, svc.Leave(110, "nobody"), spectate.ErrNotWatching, "leave not watching")
}

// TestRejectedOpsDoNotAdvanceClock 被拒绝的操作不改状态、不推进时钟。
func TestRejectedOpsDoNotAdvanceClock(t *testing.T) {
	svc := mustNew(t, 0, 1)
	mustEmit(t, svc, 100, live.Normal) // 时钟 100
	wantErr(t, svc.Join(50, "a", false), spectate.ErrClockRegression, "regressed join")
	must(t, svc.Join(100, "a", false), "join at old maxNow still accepted")
	_, err := svc.Emit(100, live.Kind(99))
	wantErr(t, err, spectate.ErrInvalidParam, "bad kind")
	mustEmit(t, svc, 100, live.Normal) // 同刻仍接受
	mustEmit(t, svc, 100, live.End)
	_, err = svc.Emit(101, live.Normal)
	wantErr(t, err, live.ErrEnded, "emit after end")
	mustLag(t, svc, 100, "a", 3) // 时钟未被 101 的拒绝推进
}

// TestNewValidation New 的参数范围：D 为 0..10^9，M 为 1..10^5。
func TestNewValidation(t *testing.T) {
	bad := [][2]int64{{-1, 1}, {1_000_000_001, 1}, {0, 0}, {0, -1}, {0, 100_001}}
	for _, p := range bad {
		if _, err := spectate.New(p[0], p[1]); !errors.Is(err, spectate.ErrInvalidParam) {
			t.Errorf("New(%d,%d) = %v, want ErrInvalidParam", p[0], p[1], err)
		}
	}
	good := [][2]int64{{0, 1}, {1_000_000_000, 100_000}, {3000, 10}}
	for _, p := range good {
		if _, err := spectate.New(p[0], p[1]); err != nil {
			t.Errorf("New(%d,%d) = %v, want nil", p[0], p[1], err)
		}
	}
}

// TestSetModeValidation 非法模式报参数非法。
func TestSetModeValidation(t *testing.T) {
	svc := mustNew(t, 0, 1)
	wantErr(t, svc.SetMode(0, spectate.Mode(99)), spectate.ErrInvalidParam, "bad mode")
	must(t, svc.SetMode(0, spectate.FriendsOnly), "valid mode")
}
