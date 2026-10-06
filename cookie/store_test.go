package cookie

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func tp(t time.Time) *time.Time { return &t }

func baseInput(name, site string) SetInput {
	return SetInput{Name: name, Value: "v-" + name, Site: site, Path: "/", SourceSite: site, SourceSecure: true}
}

func mustSet(t *testing.T, s *Store, in SetInput) {
	t.Helper()
	if err := s.Set(in); err != nil {
		t.Fatalf("Set(%s/%s) 意外失败: %v", in.Site, in.Name, err)
	}
}

func attachNames(t *testing.T, s *Store, r Request) []string {
	t.Helper()
	es, err := s.Attach(r)
	if err != nil {
		t.Fatalf("Attach(%s%s) 意外失败: %v", r.Site, r.Path, err)
	}
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name
	}
	t.Logf("Attach site=%s path=%s secure=%v initiator=%q topNav=%v safe=%v -> %v",
		r.Site, r.Path, r.Secure, r.InitiatorSite, r.TopLevelNav, r.SafeMethod, names)
	return names
}

func equalNames(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func findEntry(s *Store, name string) (Entry, bool) {
	for _, e := range s.DebugEntries() {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// 路径边界规则：整段匹配与非整段匹配。
func TestPathBoundary(t *testing.T) {
	st := New(Config{})
	in := baseInput("root", "a.com")
	mustSet(t, st, in)
	in = baseInput("a", "a.com")
	in.Path = "/a"
	mustSet(t, st, in)
	in = baseInput("aslash", "a.com")
	in.Path = "/a/"
	mustSet(t, st, in)
	in = baseInput("ab", "a.com")
	in.Path = "/ab"
	mustSet(t, st, in)

	req := Request{Site: "a.com", Secure: true, InitiatorSite: "a.com"}
	cases := []struct {
		path string
		want []string
	}{
		{"/a/b", []string{"aslash", "a", "root"}}, // 整段：/a/ 与 /a 均命中
		{"/a", []string{"a", "root"}},             // 精确；/a/ 不是 /a 的前缀
		{"/a/", []string{"aslash", "a", "root"}},  // /a 的边界字符为 '/'
		{"/ab", []string{"ab", "root"}},           // /a 不能整段匹配 /ab
		{"/abc", []string{"root"}},                // /ab 与 /a 均不能整段匹配
		{"/a-b", []string{"root"}},                // 边界字符非 '/'
	}
	for _, c := range cases {
		req.Path = c.path
		if got := attachNames(t, st, req); !equalNames(got, c.want...) {
			t.Errorf("path=%s: got %v, want %v", c.path, got, c.want)
		}
	}
}

// 宽松模式：顶层导航的安全方法可跨站附带，非安全方法不可。
func TestLaxTopLevelNav(t *testing.T) {
	st := New(Config{LaxGrace: 2 * time.Minute})
	in := baseInput("lax", "a.com")
	in.SameSite = SameSiteLax
	mustSet(t, st, in)

	cross := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "b.com"}
	cross.TopLevelNav, cross.SafeMethod = true, true
	if got := attachNames(t, st, cross); !equalNames(got, "lax") {
		t.Errorf("顶层导航安全方法跨站: got %v, want [lax]", got)
	}
	cross.TopLevelNav, cross.SafeMethod = true, false
	if got := attachNames(t, st, cross); !equalNames(got) {
		t.Errorf("顶层导航非安全方法跨站(显式 Lax 无宽限): got %v, want []", got)
	}
	cross.TopLevelNav, cross.SafeMethod = false, true
	if got := attachNames(t, st, cross); !equalNames(got) {
		t.Errorf("非顶层导航跨站: got %v, want []", got)
	}
	same := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if got := attachNames(t, st, same); !equalNames(got, "lax") {
		t.Errorf("同站: got %v, want [lax]", got)
	}
}

// 未声明同站模式：宽限时长内允许非安全方法顶层跨站请求，恰好到达宽限时长即不再允许。
func TestUnspecifiedGraceExact(t *testing.T) {
	st := New(Config{LaxGrace: 2 * time.Minute})
	mustSet(t, st, baseInput("u", "a.com")) // SameSiteUnspecified

	crossUnsafeNav := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "b.com", TopLevelNav: true, SafeMethod: false}

	if err := st.AdvanceClock(time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := attachNames(t, st, crossUnsafeNav); !equalNames(got, "u") {
		t.Errorf("宽限期内(1m<2m): got %v, want [u]", got)
	}
	if err := st.AdvanceClock(time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := attachNames(t, st, crossUnsafeNav); !equalNames(got) {
		t.Errorf("恰好到达宽限时长(2m): got %v, want []", got)
	}
	if err := st.AdvanceClock(time.Second); err != nil {
		t.Fatal(err)
	}
	if got := attachNames(t, st, crossUnsafeNav); !equalNames(got) {
		t.Errorf("超过宽限时长: got %v, want []", got)
	}
}

// 严格模式：跨站一律拒绝，同站可附带。
func TestStrictCrossSite(t *testing.T) {
	st := New(Config{})
	in := baseInput("s", "a.com")
	in.SameSite = SameSiteStrict
	mustSet(t, st, in)

	cross := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "b.com", TopLevelNav: true, SafeMethod: true}
	if got := attachNames(t, st, cross); !equalNames(got) {
		t.Errorf("严格模式跨站顶层安全导航: got %v, want []", got)
	}
	same := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if got := attachNames(t, st, same); !equalNames(got, "s") {
		t.Errorf("严格模式同站: got %v, want [s]", got)
	}
}

// 仅安全条目遇不安全请求不附带，且不更新最近访问时刻。
func TestSecureOnlyEntry(t *testing.T) {
	st := New(Config{})
	in := baseInput("sec", "a.com")
	in.Secure = true
	mustSet(t, st, in)

	insecure := Request{Site: "a.com", Path: "/", Secure: false, InitiatorSite: "a.com"}
	if got := attachNames(t, st, insecure); !equalNames(got) {
		t.Errorf("不安全请求: got %v, want []", got)
	}
	if e, _ := findEntry(st, "sec"); !e.LastAccessedAt.Equal(e.CreatedAt) {
		t.Errorf("被拒绝附带的条目不应更新最近访问时刻: lastAccess=%v created=%v", e.LastAccessedAt, e.CreatedAt)
	}
	secure := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if got := attachNames(t, st, secure); !equalNames(got, "sec") {
		t.Errorf("安全请求: got %v, want [sec]", got)
	}
}

// 分区键：一方为空即不相等，均为空才算相等。
func TestPartitionKey(t *testing.T) {
	st := New(Config{})
	in := baseInput("p", "a.com")
	in.PartitionKey = sp("p1")
	mustSet(t, st, in)
	mustSet(t, st, baseInput("np", "a.com")) // 未分区

	req := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if got := attachNames(t, st, req); !equalNames(got, "np") {
		t.Errorf("请求未分区: got %v, want [np]", got)
	}
	req.PartitionKey = sp("p1")
	if got := attachNames(t, st, req); !equalNames(got, "p") {
		t.Errorf("请求分区 p1: got %v, want [p]", got)
	}
	req.PartitionKey = sp("p2")
	if got := attachNames(t, st, req); !equalNames(got) {
		t.Errorf("请求分区 p2: got %v, want []", got)
	}
}

// 过期时刻恰等于当前时刻即视为已过期，被观察到时移除并计过期淘汰。
func TestExpiryEqualsNow(t *testing.T) {
	st := New(Config{})
	in := baseInput("e", "a.com")
	in.ExpiresAt = tp(st.Now().Add(5 * time.Minute))
	mustSet(t, st, in)

	req := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if err := st.AdvanceClock(4 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := attachNames(t, st, req); !equalNames(got, "e") {
		t.Errorf("未到期: got %v, want [e]", got)
	}
	if err := st.AdvanceClock(time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := attachNames(t, st, req); !equalNames(got) {
		t.Errorf("恰等过期时刻: got %v, want []", got)
	}
	if st.Stats().ExpiredEvictions != 1 {
		t.Errorf("过期淘汰计数: got %d, want 1", st.Stats().ExpiredEvictions)
	}
	if st.Len() != 0 {
		t.Errorf("过期条目应被移除: len=%d", st.Len())
	}

	// 过期时刻不晚于当前时刻的写入视为删除而非新增。
	in = baseInput("gone", "a.com")
	in.ExpiresAt = tp(st.Now())
	if err := st.Set(in); err != nil {
		t.Fatalf("过期写入不算拒绝: %v", err)
	}
	if st.Len() != 0 || st.Stats().TotalAdds != 1 {
		t.Errorf("过期写入不应新增: len=%d totalAdds=%d", st.Len(), st.Stats().TotalAdds)
	}
}

// 覆盖保留原创建时刻、更新最近访问时刻，从而影响淘汰次序。
func TestOverwriteKeepsCreatedAt(t *testing.T) {
	st := New(Config{SiteLimit: 2})
	mustSet(t, st, baseInput("A", "a.com"))
	createdA := st.Now()
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("B", "a.com"))
	_ = st.AdvanceClock(time.Second)

	in := baseInput("A", "a.com")
	in.Value = "v2"
	mustSet(t, st, in) // 覆盖 A，刷新其最近访问时刻

	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("C", "a.com")) // 触发站点上限，B 的最近访问最早

	if _, ok := findEntry(st, "B"); ok {
		t.Error("B 应被淘汰（最近访问最早）")
	}
	e, ok := findEntry(st, "A")
	if !ok {
		t.Fatal("A 应存在")
	}
	if !e.CreatedAt.Equal(createdA) {
		t.Errorf("覆盖应保留创建时刻: got %v, want %v", e.CreatedAt, createdA)
	}
	if e.Value != "v2" {
		t.Errorf("覆盖应更新值: got %q", e.Value)
	}
	st_ := st.Stats()
	if st_.CapacityEvictions != 1 || st_.TotalAdds != 3 {
		t.Errorf("容量淘汰=%d 累计新增=%d, want 1/3", st_.CapacityEvictions, st_.TotalAdds)
	}
}

// 站点上限与全局上限先后触发；全局淘汰选条目数最多的站点，并列按站点名字序。
func TestSiteThenGlobalLimit(t *testing.T) {
	st := New(Config{SiteLimit: 2, GlobalLimit: 3})
	mustSet(t, st, baseInput("A1", "a.com"))
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("A2", "a.com"))
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("B1", "b.com"))
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("B2", "b.com")) // 总数 4 超全局上限 3

	// a.com 与 b.com 各 2 条并列，按名字序选 a.com，淘汰其最久未访问的 A1。
	if _, ok := findEntry(st, "A1"); ok {
		t.Error("A1 应被全局淘汰")
	}
	if st.SiteLen("a.com") != 1 || st.SiteLen("b.com") != 2 || st.Len() != 3 {
		t.Errorf("站点分布: a=%d b=%d total=%d, want 1/2/3",
			st.SiteLen("a.com"), st.SiteLen("b.com"), st.Len())
	}

	// 清空 b.com 腾出全局空间，再单独触发站点上限。
	st.ClearSite("b.com")
	mustSet(t, st, baseInput("A3", "a.com"))
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("A4", "a.com")) // a.com 达 3 条超站点上限 2
	if st.SiteLen("a.com") != 2 {
		t.Errorf("a.com 条目数: got %d, want 2", st.SiteLen("a.com"))
	}
	if _, ok := findEntry(st, "A2"); ok {
		t.Error("A2 应被站点上限淘汰")
	}
}

// 淘汰并列打破：最近访问时刻相同，按创建时刻最早者淘汰。
func TestEvictionTieBreak(t *testing.T) {
	st := New(Config{SiteLimit: 2})
	mustSet(t, st, baseInput("A", "a.com"))
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("B", "a.com"))
	_ = st.AdvanceClock(time.Second)

	// 一次请求同时命中 A、B，二者最近访问时刻被刷成同一时刻。
	req := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if got := attachNames(t, st, req); !equalNames(got, "A", "B") {
		t.Fatalf("附带: got %v, want [A B]", got)
	}
	mustSet(t, st, baseInput("C", "a.com"))
	if _, ok := findEntry(st, "A"); ok {
		t.Error("最近访问并列时应淘汰创建更早的 A")
	}
	if _, ok := findEntry(st, "B"); !ok {
		t.Error("B 应保留")
	}
}

// 过期写入视为删除已有同键条目，计显式删除而非拒绝。
func TestDeleteWrite(t *testing.T) {
	st := New(Config{})
	mustSet(t, st, baseInput("A", "a.com"))

	in := baseInput("A", "a.com")
	in.ExpiresAt = tp(st.Now())
	if err := st.Set(in); err != nil {
		t.Fatalf("删除写入不算拒绝: %v", err)
	}
	if st.Len() != 0 {
		t.Errorf("同键条目应被删除: len=%d", st.Len())
	}
	st_ := st.Stats()
	if st_.ExplicitDeletes != 1 || st_.TotalAdds != 1 {
		t.Errorf("显式删除=%d 累计新增=%d, want 1/1", st_.ExplicitDeletes, st_.TotalAdds)
	}

	// 删除不存在的同键条目：成功且无计数变化。
	in.Name = "ghost"
	if err := st.Set(in); err != nil {
		t.Fatal(err)
	}
	if st.Stats().ExplicitDeletes != 1 {
		t.Errorf("无同键条目时不应计数: %d", st.Stats().ExplicitDeletes)
	}
}

// 仅 HTTP 条目对脚本读取不可见，但参与请求附带与淘汰统计。
func TestHTTPOnlyVisibility(t *testing.T) {
	st := New(Config{})
	in := baseInput("h", "a.com")
	in.HTTPOnly = true
	mustSet(t, st, in)
	mustSet(t, st, baseInput("n", "a.com"))

	req := Request{Site: "a.com", Path: "/", Secure: true, InitiatorSite: "a.com"}
	if got := attachNames(t, st, req); !equalNames(got, "h", "n") {
		t.Errorf("请求附带应包含仅 HTTP 条目: got %v", got)
	}
	es, err := st.ScriptRead("a.com", "/", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Name != "n" {
		t.Errorf("脚本读取应过滤仅 HTTP 条目: got %v", es)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 仅主机前缀 > 安全来源 > 同站无模式缺安全。
// 被拒绝的操作不得改变任何条目、统计与时钟。
func TestRejectOrder(t *testing.T) {
	st := New(Config{})
	_ = st.AdvanceClock(10 * time.Second)
	past := tp(st.Now().Add(-time.Second))

	cases := []struct {
		name string
		in   SetInput
		want error
	}{
		{"空名字+时钟回退", SetInput{Path: "/", At: past}, ErrInvalidArgument},
		{"空站点", SetInput{Name: "k", Path: "/"}, ErrInvalidArgument},
		{"路径非斜杠开头", SetInput{Name: "k", Site: "a.com", Path: "x"}, ErrInvalidArgument},
		{"未知同站模式", SetInput{Name: "k", Site: "a.com", Path: "/", SameSite: SameSite(9)}, ErrInvalidArgument},
		{"时钟回退+前缀违规", SetInput{Name: "__Host-k", Site: "a.com", Path: "/x", SourceSite: "a.com", SourceSecure: true, At: past}, ErrClockRegression},
		{"前缀违规(非根路径)+来源不安全", SetInput{Name: "__Host-k", Site: "a.com", Path: "/x", Secure: true, SourceSite: "a.com", SourceSecure: false}, ErrHostPrefixViolation},
		{"前缀违规(站点不一致)", SetInput{Name: "__Host-k", Site: "a.com", Path: "/", Secure: true, SourceSite: "evil.com", SourceSecure: true}, ErrHostPrefixViolation},
		{"前缀违规(非仅安全)", SetInput{Name: "__Host-k", Site: "a.com", Path: "/", SourceSite: "a.com", SourceSecure: true}, ErrHostPrefixViolation},
		{"来源不安全+同站无模式", SetInput{Name: "k", Site: "a.com", Path: "/", Secure: true, SourceSecure: false, SameSite: SameSiteNone}, ErrInsecureSource},
		{"同站无模式缺安全", SetInput{Name: "k", Site: "a.com", Path: "/", SameSite: SameSiteNone, SourceSite: "a.com", SourceSecure: true}, ErrSameSiteNoneInsecure},
	}
	for _, c := range cases {
		beforeStats := st.Stats()
		beforeLen := st.Len()
		beforeNow := st.Now()
		err := st.Set(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		if st.Stats() != beforeStats || st.Len() != beforeLen || !st.Now().Equal(beforeNow) {
			t.Errorf("%s: 被拒绝的操作改变了状态", c.name)
		}
		t.Logf("拒绝用例 %s -> %v", c.name, err)
	}

	// 满足全部约定的 __Host- 条目可以写入。
	ok := SetInput{Name: "__Host-ok", Site: "a.com", Path: "/", Secure: true, SourceSite: "a.com", SourceSecure: true}
	if err := st.Set(ok); err != nil {
		t.Errorf("合法 __Host- 写入: %v", err)
	}
}

// 时钟回退被拒绝且不改变时钟。
func TestClockRegression(t *testing.T) {
	st := New(Config{})
	_ = st.AdvanceClock(5 * time.Second)
	now := st.Now()
	if err := st.AdvanceClock(-time.Second); !errors.Is(err, ErrClockRegression) {
		t.Errorf("负增量: got %v, want ErrClockRegression", err)
	}
	if !st.Now().Equal(now) {
		t.Error("回退被拒绝后时钟不应改变")
	}
	if err := st.AdvanceClock(0); err != nil {
		t.Errorf("零增量应允许: %v", err)
	}
}

// 三类清除：按站点、按分区键、按创建时刻区间（左闭右开），均计显式删除。
func TestClearOps(t *testing.T) {
	st := New(Config{})
	mustSet(t, st, baseInput("a1", "a.com"))
	_ = st.AdvanceClock(time.Second)
	in := baseInput("a2", "a.com")
	in.PartitionKey = sp("p1")
	mustSet(t, st, in)
	_ = st.AdvanceClock(time.Second)
	in = baseInput("b1", "b.com")
	in.PartitionKey = sp("p1")
	mustSet(t, st, in)
	_ = st.AdvanceClock(time.Second)
	mustSet(t, st, baseInput("b2", "b.com"))

	// 按创建时刻区间 [t1, t3)：a1(t0) 保留，a2(t1)、b1(t2) 删除，b2(t3) 保留。
	all := st.DebugEntries()
	t1 := all[1].CreatedAt
	t3 := all[3].CreatedAt
	n, err := st.ClearCreatedRange(t1, t3)
	if err != nil || n != 2 {
		t.Errorf("按区间清除: n=%d err=%v, want 2", n, err)
	}
	if _, ok := findEntry(st, "a1"); !ok {
		t.Error("a1 在区间左端点之外，应保留")
	}
	if _, ok := findEntry(st, "b2"); !ok {
		t.Error("b2 在区间右端点（开区间），应保留")
	}

	// 按分区键清除。
	mustSet(t, st, baseInput("c1", "c.com"))
	in = baseInput("c2", "c.com")
	in.PartitionKey = sp("p9")
	mustSet(t, st, in)
	if n := st.ClearPartition(sp("p9")); n != 1 {
		t.Errorf("按分区清除: n=%d, want 1", n)
	}
	if _, ok := findEntry(st, "c2"); ok {
		t.Error("c2 应被分区清除")
	}

	// 按站点清除。
	if n := st.ClearSite("c.com"); n != 1 {
		t.Errorf("按站点清除: n=%d, want 1", n)
	}
	if st.SiteLen("c.com") != 0 {
		t.Error("c.com 应被清空")
	}

	// 守恒：三类删除 + 当前条目数 == 累计新增。
	st_ := st.Stats()
	if st_.ExpiredEvictions+st_.CapacityEvictions+st_.ExplicitDeletes+uint64(st.Len()) != st_.TotalAdds {
		t.Errorf("计数不守恒: %+v len=%d", st_, st.Len())
	}
	if st_.ExplicitDeletes != 4 {
		t.Errorf("显式删除: got %d, want 4", st_.ExplicitDeletes)
	}
}

// 并发调用等价于某个串行顺序：不变量始终成立。
func TestConcurrentInvariant(t *testing.T) {
	st := New(Config{SiteLimit: 8, GlobalLimit: 40, LaxGrace: time.Minute})
	sites := []string{"a.com", "b.com", "c.com"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 400; i++ {
				site := sites[rng.Intn(len(sites))]
				switch rng.Intn(5) {
				case 0:
					in := baseInput(fmt.Sprintf("k%d", rng.Intn(20)), site)
					in.Secure = rng.Intn(2) == 0
					in.SourceSecure = true
					_ = st.Set(in)
				case 1:
					_, _ = st.Attach(Request{Site: site, Path: "/", Secure: true, InitiatorSite: site})
				case 2:
					st.ClearSite(site)
				case 3:
					_ = st.AdvanceClock(time.Duration(rng.Intn(5)) * time.Second)
				case 4:
					_, _ = st.ScriptRead(site, "/", true, nil)
				}
			}
		}(int64(g))
	}
	wg.Wait()

	st_ := st.Stats()
	if st_.ExpiredEvictions+st_.CapacityEvictions+st_.ExplicitDeletes+uint64(st.Len()) != st_.TotalAdds {
		t.Errorf("并发后计数不守恒: %+v len=%d", st_, st.Len())
	}
	if st.Len() > 40 {
		t.Errorf("超出全局上限: %d", st.Len())
	}
	for _, site := range sites {
		if st.SiteLen(site) > 8 {
			t.Errorf("站点 %s 超出上限: %d", site, st.SiteLen(site))
		}
	}
}

// 性能证明一：附带判定扫描的候选数只取决于目标站点条目数，
// 与无关站点的条目总数无关。
func TestAttachCostIndependentOfBystanders(t *testing.T) {
	st := New(Config{})
	for i := 0; i < 5; i++ {
		mustSet(t, st, baseInput(fmt.Sprintf("t%d", i), "target.com"))
	}
	for s := 0; s < 200; s++ {
		for i := 0; i < 250; i++ {
			mustSet(t, st, baseInput(fmt.Sprintf("k%d", i), fmt.Sprintf("s%d.com", s)))
		}
	}
	if st.Len() != 50005 {
		t.Fatalf("条目总数: got %d", st.Len())
	}
	st.attachScans = 0
	req := Request{Site: "target.com", Path: "/", Secure: true, InitiatorSite: "target.com"}
	if got := attachNames(t, st, req); len(got) != 5 {
		t.Fatalf("附带条数: got %d, want 5", len(got))
	}
	if st.attachScans != 5 {
		t.Errorf("附带判定扫描候选数=%d, want 5（与 50000 条无关条目无关）", st.attachScans)
	}
}

// 性能证明二：淘汰选择弹出的堆项数为常数级，不随站点条目数线性增长。
func TestEvictionSelectionSublinear(t *testing.T) {
	const limit = 5000
	st := New(Config{SiteLimit: limit})
	for i := 0; i < limit; i++ {
		_ = st.AdvanceClock(time.Nanosecond)
		mustSet(t, st, baseInput(fmt.Sprintf("k%05d", i), "a.com"))
	}
	st.evictPops, st.sitePops = 0, 0
	mustSet(t, st, baseInput("new", "a.com"))

	if st.Stats().CapacityEvictions != 1 {
		t.Fatalf("容量淘汰: got %d, want 1", st.Stats().CapacityEvictions)
	}
	if st.evictPops > 3 {
		t.Errorf("淘汰选择弹出堆项数=%d（站点规模 %d），应为常数级", st.evictPops, limit)
	}
	if _, ok := findEntry(st, "k00000"); ok {
		t.Error("最久未访问的 k00000 应被淘汰")
	}
	t.Logf("站点规模 %d 时淘汰选择弹出堆项数=%d", limit, st.evictPops)
}
