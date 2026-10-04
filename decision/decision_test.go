package decision_test

import (
	"errors"
	"testing"

	"ontology/decision"
	"ontology/strike"
)

func newSys(t *testing.T, period int64) *decision.System {
	t.Helper()
	store, err := strike.NewStore(period)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return decision.NewSystem(store)
}

func overturn(t *testing.T, s *decision.System, id string) {
	t.Helper()
	s.Lock()
	s.OverturnLocked(id)
	s.Unlock()
}

func level(t *testing.T, s *decision.System, content string) int {
	t.Helper()
	l, err := s.EffectiveLevel([]byte(content))
	if err != nil {
		t.Fatalf("EffectiveLevel(%s): %v", content, err)
	}
	return l
}

// 多条决定取最大值；推翻最高者后回落到次高，而不是回到 0。
func TestEffectiveLevelMaxAndFallback(t *testing.T) {
	s := newSys(t, 1000)
	if err := s.Publish(0, []byte("c"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	if got := level(t, s, "c"); got != 0 {
		t.Fatalf("empty level=%d, want 0", got)
	}
	steps := []struct {
		id    string
		level int
		want  int
	}{
		{"d1", 1, 1},
		{"d2", 3, 3},
		{"d3", 2, 3},
	}
	for i, st := range steps {
		if err := s.Decide(int64(10+i), []byte(st.id), []byte("c"), st.level, []byte("r1")); err != nil {
			t.Fatal(err)
		}
		if got := level(t, s, "c"); got != st.want {
			t.Fatalf("after %s level=%d, want %d", st.id, got, st.want)
		}
	}
	overturn(t, s, "d2") // 推翻最高者，回落到次高
	if got := level(t, s, "c"); got != 2 {
		t.Fatalf("after overturn d2 level=%d, want 2", got)
	}
	overturn(t, s, "d3")
	if got := level(t, s, "c"); got != 1 {
		t.Fatalf("after overturn d3 level=%d, want 1", got)
	}
	overturn(t, s, "d1")
	if got := level(t, s, "c"); got != 0 {
		t.Fatalf("after overturn all level=%d, want 0", got)
	}
	if _, err := s.EffectiveLevel([]byte("ghost")); !errors.Is(err, decision.ErrContentNotFound) {
		t.Fatalf("EffectiveLevel(ghost) err=%v, want ErrContentNotFound", err)
	}
}

// level 1 权重为 0，不记分。
func TestLevel1NoScore(t *testing.T) {
	s := newSys(t, 1000)
	if err := s.Publish(0, []byte("c"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		id := []byte{byte('a' + i)}
		if err := s.Decide(int64(10+i), id, []byte("c"), 1, []byte("r")); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.State([]byte("u"), 100); got != strike.StateNormal {
		t.Fatalf("State=%v, want normal (level 1 不记分)", got)
	}
	if got := level(t, s, "c"); got != 1 {
		t.Fatalf("level=%d, want 1", got)
	}
}

// 账号受限（禁言/封禁）时 Publish 被拒；既有内容仍可继续 Decide。
func TestPublishRestrictedAndDecideAllowed(t *testing.T) {
	s := newSys(t, 1000)
	if err := s.Publish(0, []byte("c"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	// 两条 level 3：s=4，禁言。
	if err := s.Decide(10, []byte("d1"), []byte("c"), 3, []byte("r")); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(20, []byte("d2"), []byte("c"), 3, []byte("r")); err != nil {
		t.Fatal(err)
	}
	if got := s.State([]byte("u"), 30); got != strike.StateMuted {
		t.Fatalf("State=%v, want muted", got)
	}
	if err := s.Publish(30, []byte("c2"), []byte("u")); !errors.Is(err, decision.ErrAccountRestricted) {
		t.Fatalf("Publish muted err=%v, want ErrAccountRestricted", err)
	}
	// 禁言不妨碍对既有内容继续决定；s=6 封禁。
	if err := s.Decide(40, []byte("d3"), []byte("c"), 3, []byte("r")); err != nil {
		t.Fatalf("Decide on banned creator's content: %v", err)
	}
	if got := s.State([]byte("u"), 50); got != strike.StateBanned {
		t.Fatalf("State=%v, want banned", got)
	}
	if err := s.Publish(50, []byte("c3"), []byte("u")); !errors.Is(err, decision.ErrAccountRestricted) {
		t.Fatalf("Publish banned err=%v, want ErrAccountRestricted", err)
	}
	// 封禁不粘滞：计分过期后恢复，Publish 放行。
	if err := s.Publish(1040, []byte("c4"), []byte("u")); err != nil {
		t.Fatalf("Publish after expiry: %v", err)
	}
}

// Publish 拒绝次序：参数非法 > 时钟回退 > 内容已存在 > 账号受限；被拒不改状态。
func TestPublishRejectOrder(t *testing.T) {
	s := newSys(t, 1000)
	if err := s.Publish(10, []byte("c"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		now     int64
		content string
		creator string
		want    error
	}{
		{"空内容+时钟回退报参数非法", 5, "", "u", decision.ErrInvalidArgument},
		{"空创作者", 20, "x", "", decision.ErrInvalidArgument},
		{"now越界", 1e12 + 1, "x", "u", decision.ErrInvalidArgument},
		{"时钟回退优先于内容已存在", 5, "c", "u", decision.ErrClockRegression},
		{"内容已存在", 20, "c", "v", decision.ErrContentExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Publish(tc.now, []byte(tc.content), []byte(tc.creator)); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	// 被拒操作不推进时钟：now=10 仍被接受。
	if err := s.Publish(10, []byte("c9"), []byte("u")); err != nil {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
	// 内容已存在优先于账号受限。
	if err := s.Publish(30, []byte("d"), []byte("w")); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(40, []byte("d1"), []byte("d"), 3, []byte("r")); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(50, []byte("d2"), []byte("d"), 3, []byte("r")); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(60, []byte("c9"), []byte("w")); !errors.Is(err, decision.ErrContentExists) {
		t.Fatalf("err=%v, want ErrContentExists (优先于账号受限)", err)
	}
	if err := s.Publish(60, []byte("fresh"), []byte("w")); !errors.Is(err, decision.ErrAccountRestricted) {
		t.Fatalf("err=%v, want ErrAccountRestricted", err)
	}
}

// Decide 拒绝次序：参数非法 > 时钟回退 > 决定已存在 > 内容不存在。
func TestDecideRejectOrder(t *testing.T) {
	s := newSys(t, 1000)
	if err := s.Publish(10, []byte("c"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(20, []byte("d1"), []byte("c"), 2, []byte("r")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		now      int64
		id       string
		content  string
		level    int
		reviewer string
		want     error
	}{
		{"level越界+时钟回退报参数非法", 5, "x", "c", 4, "r", decision.ErrInvalidArgument},
		{"level为0", 30, "x", "c", 0, "r", decision.ErrInvalidArgument},
		{"空审核员", 30, "x", "c", 2, "", decision.ErrInvalidArgument},
		{"时钟回退优先于决定已存在", 15, "d1", "c", 2, "r", decision.ErrClockRegression},
		{"决定已存在优先于内容不存在", 30, "d1", "ghost", 2, "r", decision.ErrDecisionExists},
		{"内容不存在", 30, "d2", "ghost", 2, "r", decision.ErrContentNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Decide(tc.now, []byte(tc.id), []byte(tc.content), tc.level, []byte(tc.reviewer))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	// 被拒操作不推进时钟、不产生计分。
	if err := s.Decide(20, []byte("d9"), []byte("c"), 3, []byte("r")); err != nil {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
	if got := s.State([]byte("u"), 100); got != strike.StateMuted {
		t.Fatalf("State=%v, want muted (s=1+2=3)", got)
	}
	if got := level(t, s, "c"); got != 3 {
		t.Fatalf("level=%d, want 3", got)
	}
}
