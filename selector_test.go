package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func keyset(keys ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

func labelsOf(pairs ...string) map[string]string {
	if len(pairs)%2 != 0 {
		panic("labelsOf 需要成对参数")
	}
	m := make(map[string]string, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func mustNew(t *testing.T, selectors []SelectorSpec, policy FallbackPolicy, def map[string]string, pt int) *HostSelector {
	t.Helper()
	s, err := NewHostSelector(selectors, policy, def, pt)
	if err != nil {
		t.Fatalf("NewHostSelector 意外失败: %v", err)
	}
	return s
}

func mustAdd(t *testing.T, s *HostSelector, id string, labels map[string]string, healthy bool) {
	t.Helper()
	if err := s.AddHost(id, labels, healthy); err != nil {
		t.Fatalf("AddHost(%s) 意外失败: %v", id, err)
	}
}

func mustRoute(t *testing.T, s *HostSelector, labels map[string]string, want string) {
	t.Helper()
	got, err := s.Route(labels)
	if err != nil {
		t.Fatalf("Route(%v) 意外失败: %v", labels, err)
	}
	if got != want {
		t.Fatalf("Route(%v) = %q, 期望 %q", labels, got, want)
	}
}

func mustRouteFail(t *testing.T, s *HostSelector, labels map[string]string, want error) {
	t.Helper()
	got, err := s.Route(labels)
	if !errors.Is(err, want) {
		t.Fatalf("Route(%v) err = %v, 期望 %v (got id=%q)", labels, err, want, got)
	}
}

// TestWorkedExample 逐步对照题目给出的完整示例。
func TestWorkedExample(t *testing.T) {
	selectors := []SelectorSpec{
		{KeySet: keyset("version")},
		{KeySet: keyset("version", "zone"), FallbackKeys: keyset("version")},
	}
	s := mustNew(t, selectors, FallbackAnyEndpoint, nil, 50)
	mustAdd(t, s, "a", labelsOf("version", "v1", "zone", "z1"), true)
	mustAdd(t, s, "b", labelsOf("version", "v1", "zone", "z2"), true)
	mustAdd(t, s, "c", labelsOf("version", "v2", "zone", "z1"), true)
	mustAdd(t, s, "d", labelsOf("version", "v2", "zone", "z2"), false)
	mustAdd(t, s, "e", labelsOf("version", "v3", "zone", "z1"), false)

	mustRoute(t, s, labelsOf("version", "v1"), "a")
	mustRoute(t, s, labelsOf("version", "v1"), "b")
	mustRoute(t, s, labelsOf("version", "v2"), "c")
	mustRoute(t, s, labelsOf("version", "v2", "zone", "z2"), "d")
	mustRoute(t, s, labelsOf("version", "v1", "zone", "z9"), "a")
	mustRoute(t, s, labelsOf("version", "v9", "zone", "z1"), "a")
	mustRoute(t, s, labelsOf("version", "v4"), "b")

	if c := s.SubsetCount(labelsOf("version", "v1")); c != 3 {
		t.Fatalf("子集 {version:v1} 计数 = %d, 期望 3", c)
	}
	if c := s.SubsetCount(labelsOf("version", "v2", "zone", "z2")); c != 1 {
		t.Fatalf("子集 {version:v2,zone:z2} 计数 = %d, 期望 1", c)
	}
	if g := s.GlobalCount(); g != 2 {
		t.Fatalf("全局计数 = %d, 期望 2", g)
	}
}

// TestKeySetMustBeExactlyEqual 键集合是选择器的超集或子集时都不算匹配。
func TestKeySetMustBeExactlyEqual(t *testing.T) {
	selectors := []SelectorSpec{{KeySet: keyset("version", "zone")}}
	s := mustNew(t, selectors, FallbackAnyEndpoint, nil, 50)
	mustAdd(t, s, "a", labelsOf("version", "v1"), true)
	mustAdd(t, s, "b", labelsOf("version", "v1", "zone", "z1", "rack", "r1"), true)

	mustRoute(t, s, labelsOf("version", "v1"), "a")
	if g := s.GlobalCount(); g != 1 {
		t.Fatalf("子集请求应走全局回退, g=%d", g)
	}
	mustRoute(t, s, labelsOf("version", "v1", "zone", "z1", "rack", "r1"), "b")
	if g := s.GlobalCount(); g != 2 {
		t.Fatalf("超集请求应走全局回退, g=%d", g)
	}
	mustRoute(t, s, labelsOf("version", "v1", "zone", "z1"), "b")
	if c := s.SubsetCount(labelsOf("version", "v1", "zone", "z1")); c != 1 {
		t.Fatalf("精确命中应推进子集计数, c=%d", c)
	}
}

// TestEmptyKeysUseGlobalFallback K 为空走全局回退。
func TestEmptyKeysUseGlobalFallback(t *testing.T) {
	selectors := []SelectorSpec{{KeySet: keyset("version")}}
	s := mustNew(t, selectors, FallbackAnyEndpoint, nil, 50)
	mustAdd(t, s, "a", labelsOf("version", "v1"), true)
	mustAdd(t, s, "b", labelsOf("version", "v2"), true)
	mustRoute(t, s, map[string]string{}, "a")
	mustRoute(t, s, map[string]string{}, "b")
	if g := s.GlobalCount(); g != 2 {
		t.Fatalf("空键请求应推进全局计数, g=%d", g)
	}
}

// TestPanicThresholdBoundary 严格小于才恐慌：相等不恐慌，差 1 恐慌。
func TestPanicThresholdBoundary(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{KeySet: keyset("v")}}, FallbackNone, nil, 50)
	mustAdd(t, s, "a", labelsOf("v", "1"), true)
	mustAdd(t, s, "b", labelsOf("v", "1"), false)
	for i := 0; i < 4; i++ {
		mustRoute(t, s, labelsOf("v", "1"), "a")
	}

	s2 := mustNew(t, []SelectorSpec{{KeySet: keyset("v")}}, FallbackNone, nil, 50)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("h%02d", i)
		mustAdd(t, s2, id, labelsOf("v", "1"), i < 49)
	}
	mustRoute(t, s2, labelsOf("v", "1"), "h00")
	mustRoute(t, s2, labelsOf("v", "1"), "h01")
	if c := s2.SubsetCount(labelsOf("v", "1")); c != 2 {
		t.Fatalf("恐慌命中应推进同一子集计数, c=%d", c)
	}
}

// TestPanicZeroNeverPanics Pt=0 永不恐慌：H 为空时落空并降键。
func TestPanicZeroNeverPanics(t *testing.T) {
	selectors := []SelectorSpec{
		{KeySet: keyset("version", "zone"), FallbackKeys: keyset("version")},
	}
	s := mustNew(t, selectors, FallbackNone, nil, 0)
	mustAdd(t, s, "a", labelsOf("version", "v1", "zone", "z1"), false)
	mustAdd(t, s, "b", labelsOf("version", "v1", "zone", "z2"), true)

	mustRoute(t, s, labelsOf("version", "v1", "zone", "z1"), "b")
	if c := s.SubsetCount(labelsOf("version", "v1", "zone", "z1")); c != 0 {
		t.Fatalf("落空子集计数不应推进, c=%d", c)
	}
	if c := s.SubsetCount(labelsOf("version", "v1")); c != 1 {
		t.Fatalf("降键成功应推进降级后子集计数, c=%d", c)
	}
}

// TestFallbackOneLevelOnly 降键只降一级；降键后仍落空才走全局回退。
func TestFallbackOneLevelOnly(t *testing.T) {
	selectors := []SelectorSpec{
		{KeySet: keyset("a", "b", "c"), FallbackKeys: keyset("a")},
	}
	s := mustNew(t, selectors, FallbackAnyEndpoint, nil, 50)
	mustAdd(t, s, "x", labelsOf("a", "1"), true)

	mustRoute(t, s, labelsOf("a", "1", "b", "2", "c", "3"), "x")
	mustRoute(t, s, labelsOf("a", "9", "b", "2", "c", "3"), "x")
	if g := s.GlobalCount(); g != 1 {
		t.Fatalf("降键后仍落空应走全局回退, g=%d", g)
	}
}

// TestDirectAndFallbackShareSubsetCounter 直接命中与降键到达共用同一子集计数。
func TestDirectAndFallbackShareSubsetCounter(t *testing.T) {
	selectors := []SelectorSpec{
		{KeySet: keyset("version")},
		{KeySet: keyset("version", "zone"), FallbackKeys: keyset("version")},
	}
	s := mustNew(t, selectors, FallbackNone, nil, 50)
	mustAdd(t, s, "a", labelsOf("version", "v1", "zone", "z1"), true)
	mustAdd(t, s, "b", labelsOf("version", "v1", "zone", "z2"), true)

	mustRoute(t, s, labelsOf("version", "v1"), "a")
	mustRoute(t, s, labelsOf("version", "v1"), "b")
	mustRoute(t, s, labelsOf("version", "v1", "zone", "z9"), "a")
	if c := s.SubsetCount(labelsOf("version", "v1")); c != 3 {
		t.Fatalf("直接与降键应共用计数, c=%d", c)
	}
}

// TestGlobalFallbackPolicies 三种回退策略及候选为空报错。
func TestGlobalFallbackPolicies(t *testing.T) {
	selectors := []SelectorSpec{{KeySet: keyset("version")}}
	def := labelsOf("tier", "gold")

	s := mustNew(t, selectors, FallbackDefaultSubset, def, 50)
	mustAdd(t, s, "a", labelsOf("version", "v1", "tier", "gold"), true)
	mustAdd(t, s, "b", labelsOf("version", "v2", "tier", "silver"), true)
	mustAdd(t, s, "c", labelsOf("version", "v3", "tier", "gold"), false)
	for i := 0; i < 3; i++ {
		mustRoute(t, s, labelsOf("version", "missing"), "a")
	}
	if d := s.DefaultCount(); d != 3 {
		t.Fatalf("默认子集回退应推进默认计数, d=%d", d)
	}

	s2 := mustNew(t, selectors, FallbackNone, nil, 0)
	mustRouteFail(t, s2, labelsOf("other", "1"), ErrNoSelector)
	mustRouteFail(t, s2, map[string]string{}, ErrNoSelector)
	mustAdd(t, s2, "a", labelsOf("version", "v1"), false)
	mustRouteFail(t, s2, labelsOf("version", "v1"), ErrNoHealthyHost)

	s3 := mustNew(t, selectors, FallbackAnyEndpoint, nil, 50)
	mustRouteFail(t, s3, labelsOf("version", "v1"), ErrNoHealthyHost)

	s4 := mustNew(t, selectors, FallbackDefaultSubset, def, 50)
	mustRouteFail(t, s4, labelsOf("version", "v1"), ErrNoHealthyHost)
}

// TestIndependentCountersAndResizing 子集计数独立、只在成功时推进，且按当前候选大小取模。
func TestIndependentCountersAndResizing(t *testing.T) {
	selectors := []SelectorSpec{{KeySet: keyset("v")}}
	s := mustNew(t, selectors, FallbackNone, nil, 50)
	mustAdd(t, s, "a", labelsOf("v", "1"), true)
	mustAdd(t, s, "b", labelsOf("v", "2"), true)
	mustRoute(t, s, labelsOf("v", "1"), "a")
	mustRoute(t, s, labelsOf("v", "2"), "b")

	mustAdd(t, s, "y", labelsOf("v", "2"), true)
	mustRoute(t, s, labelsOf("v", "2"), "y")
	if c := s.SubsetCount(labelsOf("v", "1")); c != 1 {
		t.Fatalf("子集 v=1 计数 = %d, 期望 1", c)
	}
	if c := s.SubsetCount(labelsOf("v", "2")); c != 2 {
		t.Fatalf("子集 v=2 计数 = %d, 期望 2", c)
	}

	mustRouteFail(t, s, labelsOf("v", "9"), ErrNoHealthyHost)
	if c := s.SubsetCount(labelsOf("v", "9")); c != 0 {
		t.Fatalf("失败请求不应推进计数, c=%d", c)
	}
}

// TestExactValueEquality 标签值精确相等，空串是合法值。
func TestExactValueEquality(t *testing.T) {
	selectors := []SelectorSpec{{KeySet: keyset("v", "note")}}
	s := mustNew(t, selectors, FallbackNone, nil, 50)
	mustAdd(t, s, "a", labelsOf("v", "1", "note", ""), true)
	mustAdd(t, s, "b", labelsOf("v", "1", "note", "x"), true)
	mustRoute(t, s, labelsOf("v", "1", "note", ""), "a")
	mustRoute(t, s, labelsOf("v", "1", "note", "x"), "b")
	mustRouteFail(t, s, labelsOf("v", "01", "note", ""), ErrNoHealthyHost)
}

// TestRejectedOpsDoNotMutate 被拒绝的操作不得改变主机与任何计数。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{KeySet: keyset("v")}}, FallbackNone, nil, 50)
	if err := s.AddHost("", labelsOf("v", "1"), true); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("空 id 应报 ErrEmptyID, got %v", err)
	}
	if err := s.AddHost("h", map[string]string{"": "1"}, true); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("空键应报 ErrInvalidLabel, got %v", err)
	}
	if s.HostCount() != 0 {
		t.Fatalf("被拒绝的 AddHost 不应登记主机, n=%d", s.HostCount())
	}
	mustAdd(t, s, "h", labelsOf("v", "1"), true)
	if err := s.AddHost("h", labelsOf("v", "2"), true); !errors.Is(err, ErrDuplicateHost) {
		t.Fatalf("重复 id 应报 ErrDuplicateHost, got %v", err)
	}
	mustRoute(t, s, labelsOf("v", "1"), "h")
	mustRoute(t, s, labelsOf("v", "1"), "h")
	if c := s.SubsetCount(labelsOf("v", "1")); c != 2 {
		t.Fatalf("重复 AddHost 不应改变状态, c=%d", c)
	}

	if err := s.SetHealth("ghost", false); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("SetHealth 未知 id 应报 ErrHostNotFound, got %v", err)
	}
	if err := s.RemoveHost("ghost"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("RemoveHost 未知 id 应报 ErrHostNotFound, got %v", err)
	}
	if err := s.SetHealth("h", true); err != nil {
		t.Fatalf("设成相同健康值应被接受: %v", err)
	}
	mustRouteFail(t, s, map[string]string{"": "1"}, ErrInvalidLabel)
	if c := s.SubsetCount(labelsOf("v", "1")); c != 2 {
		t.Fatalf("非法路由不应推进计数, c=%d", c)
	}
}

// TestInvalidConfigs 覆盖构造时的各类非法配置。
func TestInvalidConfigs(t *testing.T) {
	cases := []struct {
		name      string
		selectors []SelectorSpec
		policy    FallbackPolicy
		def       map[string]string
		pt        int
	}{
		{"空选择器列表", nil, FallbackNone, nil, 50},
		{"选择器键集合为空", []SelectorSpec{{KeySet: map[string]struct{}{}}}, FallbackNone, nil, 50},
		{"选择器含空键", []SelectorSpec{{KeySet: keyset("a", "")}}, FallbackNone, nil, 50},
		{"两个选择器键集合相同", []SelectorSpec{{KeySet: keyset("a")}, {KeySet: keyset("a")}}, FallbackNone, nil, 50},
		{"FK 为空", []SelectorSpec{{KeySet: keyset("a"), FallbackKeys: map[string]struct{}{}}}, FallbackNone, nil, 50},
		{"FK 等于键集合", []SelectorSpec{{KeySet: keyset("a"), FallbackKeys: keyset("a")}}, FallbackNone, nil, 50},
		{"FK 不是子集", []SelectorSpec{{KeySet: keyset("a"), FallbackKeys: keyset("b")}}, FallbackNone, nil, 50},
		{"FK 含空键", []SelectorSpec{{KeySet: keyset("a", "b"), FallbackKeys: keyset("")}}, FallbackNone, nil, 50},
		{"Pt 为负", []SelectorSpec{{KeySet: keyset("a")}}, FallbackNone, nil, -1},
		{"Pt 超过 100", []SelectorSpec{{KeySet: keyset("a")}}, FallbackNone, nil, 101},
		{"策略非法", []SelectorSpec{{KeySet: keyset("a")}}, FallbackPolicy(99), nil, 50},
		{"默认子集默认标签为空", []SelectorSpec{{KeySet: keyset("a")}}, FallbackDefaultSubset, map[string]string{}, 50},
		{"默认子集默认标签含空键", []SelectorSpec{{KeySet: keyset("a")}}, FallbackDefaultSubset, map[string]string{"": "x"}, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHostSelector(tc.selectors, tc.policy, tc.def, tc.pt); err == nil {
				t.Fatalf("非法配置应被拒绝: %s", tc.name)
			}
		})
	}

	if _, err := NewHostSelector([]SelectorSpec{{KeySet: keyset("a")}}, FallbackAnyEndpoint, nil, 0); err != nil {
		t.Fatalf("任意端点策略应忽略默认标签且 Pt=0 合法: %v", err)
	}
}

// TestSetHealthAndRemove 变更后候选立即生效；移除后重新登记同 id，计数延续。
func TestSetHealthAndRemove(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{KeySet: keyset("v")}}, FallbackNone, nil, 0)
	mustAdd(t, s, "a", labelsOf("v", "1"), false)
	mustRouteFail(t, s, labelsOf("v", "1"), ErrNoHealthyHost)
	if err := s.SetHealth("a", true); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}
	mustRoute(t, s, labelsOf("v", "1"), "a")
	if err := s.RemoveHost("a"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	mustRouteFail(t, s, labelsOf("v", "1"), ErrNoHealthyHost)
	mustAdd(t, s, "a", labelsOf("v", "1"), true)
	mustRoute(t, s, labelsOf("v", "1"), "a")
	if c := s.SubsetCount(labelsOf("v", "1")); c != 2 {
		t.Fatalf("计数不应因移除而回滚, c=%d", c)
	}
}

// TestConcurrentRoutes 并发调用结果等价于某个串行顺序：选择序列是同一主机的重复排列。
func TestConcurrentRoutes(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{KeySet: keyset("v")}}, FallbackAnyEndpoint, nil, 0)
	mustAdd(t, s, "a", labelsOf("v", "1"), true)
	mustAdd(t, s, "b", labelsOf("v", "2"), true)

	const goroutines = 16
	const perRoutine = 200
	var wg sync.WaitGroup
	results := make(chan string, goroutines*perRoutine)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				id, err := s.Route(labelsOf("v", "1"))
				if err != nil {
					t.Errorf("并发 Route 失败: %v", err)
					return
				}
				results <- id
			}
		}()
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for id := range results {
		counts[id]++
	}
	total := goroutines * perRoutine
	if counts["a"] != total {
		t.Fatalf("并发下 v=1 子集只有 a, 实际 a=%d 次", counts["a"])
	}
	if c := s.SubsetCount(labelsOf("v", "1")); c != total {
		t.Fatalf("并发成功调用应逐次推进计数, c=%d, 期望 %d", c, total)
	}
}
