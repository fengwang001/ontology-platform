package spectate

import (
	"errors"
	"testing"

	"ontology/live"
)

// buildExample 构造题给事件流: e1(1000,N) e2(2000,H) e3(5000,N)
// e4(9000,N) e5(10000,End), D=3000。
func buildExample(t *testing.T) *Service {
	t.Helper()
	s, err := New(3000, 10)
	if err != nil {
		t.Fatal(err)
	}
	emits := []struct {
		now  int64
		kind live.Kind
	}{
		{1000, live.Normal}, {2000, live.Hidden}, {5000, live.Normal},
		{9000, live.Normal}, {10000, live.End},
	}
	for _, e := range emits {
		if _, err := s.Emit(e.now, e.kind); err != nil {
			t.Fatalf("emit %d: %v", e.now, err)
		}
	}
	return s
}

func seqs(evs []Event) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}

func eqSeq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pullErr(s *Service, now int64, v string, n int) error {
	_, _, err := s.Pull(now, v, n)
	return err
}

func lagErr(s *Service, now int64, v string) error {
	_, _, err := s.Lag(now, v)
	return err
}

// TestExampleViewerV 题给 v 的逐步拉取与 Hidden 不补发。
func TestExampleViewerV(t *testing.T) {
	s, _ := New(3000, 10)
	mustEmit := func(now int64, k live.Kind) {
		t.Helper()
		if _, err := s.Emit(now, k); err != nil {
			t.Fatal(err)
		}
	}
	mustEmit(1000, live.Normal)
	mustEmit(2000, live.Hidden)
	if err := s.Join(3500, "v", false); err != nil {
		t.Fatal(err)
	}
	pull := func(now int64, want []int64) {
		t.Helper()
		r, _, err := s.Pull(now, "v", 1000)
		if err != nil {
			t.Fatalf("pull %d: %v", now, err)
		}
		if !eqSeq(seqs(r.Events), want) {
			t.Fatalf("pull %d = %v, want %v", now, seqs(r.Events), want)
		}
	}
	pull(4000, []int64{1})
	mustEmit(5000, live.Normal)
	pull(5500, nil) // e2 此刻不可投递, 被游标越过
	mustEmit(9000, live.Normal)
	mustEmit(10000, live.End)
	pull(10500, []int64{3})
	pull(11499, []int64{4})
	pull(11500, []int64{5})
	r, _, err := s.Pull(12000, "v", 1000)
	if err != nil || len(r.Events) != 0 {
		t.Fatalf("skipped hidden re-delivered: %v err=%v", seqs(r.Events), err)
	}
}

// TestExampleLateViewerW 赛后加入可一次看到全部(含 Hidden)。
func TestExampleLateViewerW(t *testing.T) {
	s := buildExample(t)
	if err := s.Join(11500, "w", false); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Pull(11500, "w", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !eqSeq(seqs(r.Events), []int64{1, 2, 3, 4, 5}) || r.More {
		t.Fatalf("w got %v more=%v", seqs(r.Events), r.More)
	}
}

// TestExampleJudge 裁判零延迟, now=2000 即得 e1,e2。
func TestExampleJudge(t *testing.T) {
	s, _ := New(3000, 10)
	if _, err := s.Emit(1000, live.Normal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(2000, live.Hidden); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(2000, "j", true); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Pull(2000, "j", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !eqSeq(seqs(r.Events), []int64{1, 2}) {
		t.Fatalf("judge got %v", seqs(r.Events))
	}
}

// emitExampleAll 发完题给五条事件。
func emitExampleAll(t *testing.T, s *Service) {
	t.Helper()
	for _, e := range []struct {
		now  int64
		kind live.Kind
	}{
		{1000, live.Normal}, {2000, live.Hidden}, {5000, live.Normal},
		{9000, live.Normal}, {10000, live.End},
	} {
		if _, err := s.Emit(e.now, e.kind); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExampleLag u 全程不拉: Lag(5500)=1, Lag(11500)=5。
func TestExampleLag(t *testing.T) {
	s, _ := New(3000, 10)
	if _, err := s.Emit(1000, live.Normal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(2000, live.Hidden); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(3500, "u", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(5000, live.Normal); err != nil {
		t.Fatal(err)
	}
	if n, _, err := s.Lag(5500, "u"); err != nil || n != 1 {
		t.Fatalf("lag5500 = %d, %v", n, err)
	}
	if _, err := s.Emit(9000, live.Normal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(10000, live.End); err != nil {
		t.Fatal(err)
	}
	if n, _, err := s.Lag(11500, "u"); err != nil || n != 5 {
		t.Fatalf("lag11500 = %d, %v", n, err)
	}
}

// TestExampleMaxNNotCross maxN=1 在 5500 只投递 e1, 不越过 e2, 赛后可得 e2..e5。
func TestExampleMaxNNotCross(t *testing.T) {
	s, _ := New(3000, 10)
	if _, err := s.Emit(1000, live.Normal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(2000, live.Hidden); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(3500, "u", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(5000, live.Normal); err != nil {
		t.Fatal(err)
	}
	r, touch, err := s.Pull(5500, "u", 1)
	if err != nil || !eqSeq(seqs(r.Events), []int64{1}) || r.More {
		t.Fatalf("pull maxN1 = %v more=%v err=%v", seqs(r.Events), r.More, err)
	}
	if touch.Records > 2 {
		t.Fatalf("touched %d records, bound delivered+skipped+1<=2", touch.Records)
	}
	if _, err := s.Emit(9000, live.Normal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Emit(10000, live.End); err != nil {
		t.Fatal(err)
	}
	r, _, err = s.Pull(11500, "u", 1000)
	if err != nil || !eqSeq(seqs(r.Events), []int64{2, 3, 4, 5}) {
		t.Fatalf("u after catchup = %v err=%v", seqs(r.Events), err)
	}
}

// TestMoreFlag maxN 截断后 More 的真假。
func TestMoreFlag(t *testing.T) {
	s := buildExample(t)
	if err := s.Join(11500, "x", false); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Pull(11500, "x", 2)
	if err != nil || !eqSeq(seqs(r.Events), []int64{1, 2}) || !r.More {
		t.Fatalf("got %v more=%v err=%v", seqs(r.Events), r.More, err)
	}
	r, _, err = s.Pull(11500, "x", 10)
	if err != nil || !eqSeq(seqs(r.Events), []int64{3, 4, 5}) || r.More {
		t.Fatalf("got %v more=%v err=%v", seqs(r.Events), r.More, err)
	}
}

// TestJoinRejectOrder 拒绝次序: 非法参数 > 回退 > 已在 > Off > 非好友 > 名额。
func TestJoinRejectOrder(t *testing.T) {
	s, _ := New(100, 1)
	must := func(err error, want error, ctx string) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err=%v want=%v", ctx, err, want)
		}
	}
	must(s.Join(10, "", false), ErrInvalidParam, "empty viewer")
	if err := s.Join(100, "a", false); err != nil {
		t.Fatal(err)
	}
	must(s.Join(50, "a", false), ErrClockRollback, "rollback before joined")
	must(s.Join(100, "a", false), ErrAlreadyJoined, "already joined")

	if err := s.SetMode(200, Off); err != nil {
		t.Fatal(err)
	}
	if err := s.Befriend(300, "b"); err != nil {
		t.Fatal(err)
	}
	must(s.Join(300, "b", false), ErrModeOff, "off before friend check")

	if err := s.SetMode(400, FriendsOnly); err != nil {
		t.Fatal(err)
	}
	must(s.Join(400, "c", false), ErrNotFriend, "not friend before limit")

	// a 在切到 FriendsOnly 时被移除; 加好友后重新加入占住唯一名额。
	if err := s.Befriend(500, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(500, "a", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(500, "d", true); err != nil {
		t.Fatalf("judge unrestricted: %v", err)
	}
	if err := s.Befriend(600, "e"); err != nil {
		t.Fatal(err)
	}
	must(s.Join(600, "e", false), ErrSpectatorLimit, "limit for non-judge")

	must(pullErr(s, 600, "", 1), ErrInvalidParam, "pull empty viewer")
	must(pullErr(s, 600, "z", 0), ErrInvalidParam, "maxN zero")
	must(pullErr(s, 600, "z", 1001), ErrInvalidParam, "maxN too big")
	must(pullErr(s, 100, "z", 1), ErrClockRollback, "pull rollback first")
	must(pullErr(s, 600, "z", 1), ErrNotSpectating, "pull missing")
	must(lagErr(s, 100, "z"), ErrClockRollback, "lag rollback first")
	must(lagErr(s, 600, "z"), ErrNotSpectating, "lag missing")
	must(s.Leave(100, "z"), ErrClockRollback, "leave rollback first")
	must(s.Leave(600, "z"), ErrNotSpectating, "leave missing")
	must(s.SetMode(600, Mode(9)), ErrInvalidParam, "bad mode")
}

// TestModeRemoval 模式切换与撤销好友的移除、裁判保留、重入游标作废。
func TestModeRemoval(t *testing.T) {
	s, _ := New(100, 10)
	if err := s.Join(100, "p1", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(100, "j1", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Befriend(150, "f"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(150, "f", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode(200, FriendsOnly); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Lag(200, "p1"); !errors.Is(err, ErrNotSpectating) {
		t.Fatalf("non-friend removed: %v", err)
	}
	if _, _, err := s.Lag(200, "f"); err != nil {
		t.Fatalf("friend stays: %v", err)
	}
	if _, _, err := s.Lag(200, "j1"); err != nil {
		t.Fatalf("judge stays: %v", err)
	}
	if err := s.Unfriend(300, "f"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Lag(300, "f"); !errors.Is(err, ErrNotSpectating) {
		t.Fatalf("unfriend removes immediately: %v", err)
	}
	if _, err := s.Emit(400, live.Normal); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode(500, Public); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(500, "p1", false); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Pull(500, "p1", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !eqSeq(seqs(r.Events), []int64{1}) {
		t.Fatalf("rejoin cursor reset got %v", seqs(r.Events))
	}
	if err := s.SetMode(600, Off); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Lag(600, "p1"); !errors.Is(err, ErrNotSpectating) {
		t.Fatalf("off removes non-judges: %v", err)
	}
	if _, _, err := s.Lag(600, "j1"); err != nil {
		t.Fatalf("judge survives off: %v", err)
	}
}

// TestCapExcludesJudge 名额只数非裁判, 且被拒不推进时钟。
func TestCapExcludesJudge(t *testing.T) {
	s, _ := New(1000, 1)
	if err := s.Join(0, "j1", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(0, "j2", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(0, "a", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(0, "b", false); !errors.Is(err, ErrSpectatorLimit) {
		t.Fatalf("cap err=%v", err)
	}
	if err := s.Leave(0, "a"); err != nil {
		t.Fatalf("rejected join must not advance clock: %v", err)
	}
	if err := s.Join(0, "b", false); err != nil {
		t.Fatalf("freed slot reusable: %v", err)
	}
}

// TestNewValidation 构造参数边界。
func TestNewValidation(t *testing.T) {
	for _, c := range []struct {
		D      int64
		M      int
		reason string
	}{
		{-1, 1, "D neg"},
		{1_000_000_001, 1, "D too big"},
		{0, 0, "M zero"},
		{0, 100_001, "M too big"},
	} {
		if _, err := New(c.D, c.M); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: err=%v", c.reason, err)
		}
	}
	if _, err := New(0, 1); err != nil {
		t.Fatalf("D=0 valid: %v", err)
	}
}
