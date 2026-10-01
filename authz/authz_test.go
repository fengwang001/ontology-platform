package authz

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func basisName(b string) string {
	if b == BasisNone {
		return "无"
	}
	return b
}

func logDecision(t *testing.T, op, subject, action, path string, d Decision, err error) {
	t.Helper()
	if err != nil {
		t.Logf("输入=%s(%q,%q,%q) 输出=拒绝执行 原因=%v", op, subject, action, path, err)
		return
	}
	t.Logf("输入=%s(%q,%q,%q) 输出=allow=%v 判定依据=%s", op, subject, action, path, d.Allow, basisName(d.Basis))
}

func mustSet(t *testing.T, c *Cache, path, subject, action string, allow bool) {
	t.Helper()
	if err := c.SetRule(path, subject, action, allow); err != nil {
		t.Fatalf("SetRule(%q,%q,%q,%v) 失败: %v", path, subject, action, allow, err)
	}
	t.Logf("输入=SetRule(%q,%q,%q,%v) 输出=成功", path, subject, action, allow)
}

func mustDelete(t *testing.T, c *Cache, path, subject, action string) {
	t.Helper()
	if err := c.DeleteRule(path, subject, action); err != nil {
		t.Fatalf("DeleteRule(%q,%q,%q) 失败: %v", path, subject, action, err)
	}
	t.Logf("输入=DeleteRule(%q,%q,%q) 输出=成功", path, subject, action)
}

func mustDecide(t *testing.T, c *Cache, subject, action, path string) Decision {
	t.Helper()
	d, err := c.Decide(subject, action, path)
	if err != nil {
		t.Fatalf("Decide(%q,%q,%q) 失败: %v", subject, action, path, err)
	}
	logDecision(t, "Decide", subject, action, path, d, nil)
	return d
}

func logStats(t *testing.T, c *Cache) Stats {
	t.Helper()
	s := c.Stats()
	t.Logf("统计: 命中=%d 现算=%d 被失效=%d", s.Hits, s.Computes, s.Invalidated)
	return s
}

func assertDecision(t *testing.T, d Decision, wantAllow bool, wantBasis string) {
	t.Helper()
	if d.Allow != wantAllow || d.Basis != wantBasis {
		t.Fatalf("判定不符: 得到 allow=%v 依据=%s, 期望 allow=%v 依据=%s",
			d.Allow, basisName(d.Basis), wantAllow, basisName(wantBasis))
	}
}

// TestNearestBasisAndDefaults 验证最近祖先规则优先、根规则兜底、默认拒绝，
// 以及 /a 不是 /ab 的祖先。
func TestNearestBasisAndDefaults(t *testing.T) {
	c := New(0)

	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b"), false, BasisNone)

	mustSet(t, c, "/", "alice", "read", true)
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b"), true, "/")

	mustSet(t, c, "/a", "alice", "read", false)
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b"), false, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a"), false, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/c"), true, "/")

	// /a 不是 /ab 的祖先：/ab 仍命中根规则。
	assertDecision(t, mustDecide(t, c, "alice", "read", "/ab"), true, "/")

	// 其他主体/动作不受影响。
	assertDecision(t, mustDecide(t, c, "bob", "read", "/a/b"), false, BasisNone)
	assertDecision(t, mustDecide(t, c, "alice", "write", "/a/b"), false, BasisNone)

	logStats(t, c)
}

// TestDeepSetInvalidation 验证深层新增规则只失效依据为其真祖先或「无」的
// 子树内条目；更深依据的条目、兄弟子树与 /ab 不受影响。
func TestDeepSetInvalidation(t *testing.T) {
	c := New(0)
	mustSet(t, c, "/a", "alice", "read", true)
	mustSet(t, c, "/a/b", "alice", "read", false)

	// 预填充缓存。
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x/y"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b/c"), false, "/a/b")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/other/p"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/ab/y"), false, BasisNone)
	before := logStats(t, c)
	if before.Hits != 0 || before.Computes != 6 {
		t.Fatalf("预填充统计不符: %+v", before)
	}

	// 深层新增规则 /a/x：只应失效 /a/x 与 /a/x/y 两条
	// （依据由 /a 变为 /a/x，结果由 allow 变为 deny）。
	mustSet(t, c, "/a/x", "alice", "read", false)
	after := logStats(t, c)
	if after.Invalidated != 2 {
		t.Fatalf("失效条数不符: 得到 %d, 期望 2", after.Invalidated)
	}

	// 未受影响的条目必须原样保留：再次判定全部命中、零现算。
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b/c"), false, "/a/b")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/other/p"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/ab/y"), false, BasisNone)
	// 被失效的两条重新现算，依据变为 /a/x。
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x"), false, "/a/x")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x/y"), false, "/a/x")

	final := logStats(t, c)
	if final.Hits != 4 || final.Computes != 8 || final.Invalidated != 2 {
		t.Fatalf("最终统计不符: %+v, 期望 Hits=4 Computes=8 Invalidated=2", final)
	}
}

// TestDeleteFallsBackToAncestor 验证删除规则后依据退回祖先。
func TestDeleteFallsBackToAncestor(t *testing.T) {
	c := New(0)
	mustSet(t, c, "/a", "alice", "read", true)
	mustSet(t, c, "/a/b", "alice", "read", false)

	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b/c"), false, "/a/b")

	mustDelete(t, c, "/a/b", "alice", "read")
	if s := logStats(t, c); s.Invalidated != 1 {
		t.Fatalf("失效条数不符: 得到 %d, 期望 1", s.Invalidated)
	}

	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b/c"), true, "/a")

	// 删除后缓存重新建立，再次判定命中。
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/b/c"), true, "/a")
	if s := logStats(t, c); s.Hits != 1 || s.Computes != 2 || s.Invalidated != 1 {
		t.Fatalf("最终统计不符: %+v, 期望 Hits=1 Computes=2 Invalidated=1", s)
	}
}

// TestOverwriteSameValueZeroInvalidation 验证覆盖为相同取值零失效，
// 覆盖为不同取值只失效结果变化的条目。
func TestOverwriteSameValueZeroInvalidation(t *testing.T) {
	c := New(0)
	mustSet(t, c, "/a", "alice", "read", true)
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x"), true, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/y"), true, "/a")

	// 同值覆盖：零失效，缓存原样保留。
	mustSet(t, c, "/a", "alice", "read", true)
	if s := logStats(t, c); s.Invalidated != 0 {
		t.Fatalf("同值覆盖不应失效任何条目, 得到 %d", s.Invalidated)
	}
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x"), true, "/a")
	if s := logStats(t, c); s.Hits != 1 {
		t.Fatalf("同值覆盖后应直接命中, Hits=%d", s.Hits)
	}

	// 异值覆盖：两条缓存的结果都翻转，全部失效。
	mustSet(t, c, "/a", "alice", "read", false)
	if s := logStats(t, c); s.Invalidated != 2 {
		t.Fatalf("异值覆盖应失效 2 条, 得到 %d", s.Invalidated)
	}
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/x"), false, "/a")
	assertDecision(t, mustDecide(t, c, "alice", "read", "/a/y"), false, "/a")
	logStats(t, c)
}

// TestValidation 验证各类非法输入按顺序只报第一个原因，
// 且被拒绝的操作不改变规则、缓存与统计。
func TestValidation(t *testing.T) {
	c := New(1)
	mustSet(t, c, "/ok", "alice", "read", true)
	assertDecision(t, mustDecide(t, c, "alice", "read", "/ok/x"), true, "/ok")
	before := logStats(t, c)
	beforeRules := c.RuleCount()
	beforeEntries := len(c.CachedEntries())

	cases := []struct {
		name string
		op   func() error
		code ErrCode
	}{
		{"路径不以斜杠开头", func() error { return c.SetRule("a/b", "s", "a", true) }, ErrInvalidPath},
		{"路径含空段(双斜杠)", func() error { return c.SetRule("/a//b", "s", "a", true) }, ErrInvalidPath},
		{"路径含空段(尾斜杠)", func() error { return c.SetRule("/a/", "s", "a", true) }, ErrInvalidPath},
		{"空路径", func() error { return c.SetRule("", "s", "a", true) }, ErrInvalidPath},
		{"主体为空", func() error { return c.SetRule("/a", "", "a", true) }, ErrEmptySubject},
		{"动作为空", func() error { return c.SetRule("/a", "s", "", true) }, ErrEmptyAction},
		{"路径错误优先于主体为空", func() error { return c.SetRule("bad", "", "", true) }, ErrInvalidPath},
		{"主体为空优先于动作为空", func() error { return c.SetRule("/a", "", "", true) }, ErrEmptySubject},
		{"规则数达上限设新规则", func() error { return c.SetRule("/new", "s", "a", true) }, ErrRuleLimit},
		{"删除不存在的规则", func() error { return c.DeleteRule("/ok", "alice", "write") }, ErrRuleNotFound},
		{"删除不存在节点的规则", func() error { return c.DeleteRule("/nope", "alice", "read") }, ErrRuleNotFound},
		{"删除时路径非法优先", func() error { return c.DeleteRule("bad", "", "") }, ErrInvalidPath},
	}
	for _, tc := range cases {
		err := tc.op()
		t.Logf("输入=%s 输出=拒绝执行 原因=%v", tc.name, err)
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != tc.code {
			t.Fatalf("%s: 原因码不符, 得到 %v, 期望 %v", tc.name, err, tc.code)
		}
	}

	// Decide 同样校验输入。
	if _, err := c.Decide("alice", "read", "bad"); err == nil {
		t.Fatal("Decide 非法路径应报错")
	} else {
		logDecision(t, "Decide", "alice", "read", "bad", Decision{}, err)
	}

	// 被拒绝的操作不得改变规则、缓存与统计。
	if got := c.RuleCount(); got != beforeRules {
		t.Fatalf("规则数被改变: %d -> %d", beforeRules, got)
	}
	if got := len(c.CachedEntries()); got != beforeEntries {
		t.Fatalf("缓存条目数被改变: %d -> %d", beforeEntries, got)
	}
	if after := c.Stats(); after != before {
		t.Fatalf("统计被改变: %+v -> %+v", before, after)
	}

	// 达上限后覆盖已有规则（非新规则）仍应成功。
	mustSet(t, c, "/ok", "alice", "read", false)
	if s := logStats(t, c); s.Invalidated != 1 {
		t.Fatalf("覆盖已有规则应失效 1 条, 得到 %d", s.Invalidated)
	}
}

// TestStaleBackfillPrevented 确定性复现「现算与规则变更交错」：
// 现算完成后、回填前插入一次规则变更，旧的现算结果必须被丢弃，
// 不得写入缓存；最终返回与缓存都必须等于变更后的现算结果。
func TestStaleBackfillPrevented(t *testing.T) {
	c := New(0)
	mustSet(t, c, "/a", "alice", "read", false)

	changed := make(chan struct{})
	var once sync.Once
	c.testHookBeforeWrite = func() {
		once.Do(func() {
			// 在 Decide 现算完成后、回填前并发变更规则。
			if err := c.SetRule("/a", "alice", "read", true); err != nil {
				t.Errorf("并发 SetRule 失败: %v", err)
			}
			t.Logf("钩子: 回填前插入 SetRule(/a,alice,read,true)")
			close(changed)
		})
	}

	done := make(chan Decision, 1)
	go func() {
		d, err := c.Decide("alice", "read", "/a/x")
		if err != nil {
			t.Errorf("Decide 失败: %v", err)
		}
		done <- d
	}()

	<-changed
	d := <-done
	c.testHookBeforeWrite = nil
	logDecision(t, "Decide(交错)", "alice", "read", "/a/x", d, nil)

	// 返回值必须等于调用期间某一时刻的现算结果；此处重试后应为新规则的结果。
	assertDecision(t, d, true, "/a")

	// 静止后缓存条目必须等于现算结果：旧结果 deny 不得残留在缓存中。
	for _, e := range c.CachedEntries() {
		want := c.Recompute(e.Subject, e.Action, e.Path)
		t.Logf("缓存条目 (%q,%q,%q) = allow=%v 依据=%s, 现算 = allow=%v 依据=%s",
			e.Subject, e.Action, e.Path, e.Decision.Allow, basisName(e.Decision.Basis),
			want.Allow, basisName(want.Basis))
		if e.Decision != want {
			t.Fatalf("缓存残留旧结果: 条目=%+v, 现算=%+v", e.Decision, want)
		}
	}
	// 变更时缓存为空，失效条数必须为 0（失效只由变更与已缓存条目决定）。
	if s := logStats(t, c); s.Invalidated != 0 {
		t.Fatalf("失效条数不符: 得到 %d, 期望 0", s.Invalidated)
	}
}

// TestConcurrentMixed 并发混合调用决策、设置与删除；
// 静止后每个缓存条目都必须等于现算结果。
func TestConcurrentMixed(t *testing.T) {
	c := New(64)
	nodes := []string{"/", "/a", "/a/b", "/b", "/c/d"}
	subjects := []string{"alice", "bob", "carol"}
	actions := []string{"read", "write"}
	resources := []string{
		"/", "/a", "/a/b", "/a/b/c", "/a/x/y", "/ab", "/ab/1",
		"/b", "/b/2", "/c/d", "/c/d/e/f", "/none/here",
	}

	var wg sync.WaitGroup
	var decides uint64
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				s := subjects[(w+i)%len(subjects)]
				a := actions[(w+i)%len(actions)]
				r := resources[(w*7+i)%len(resources)]
				if _, err := c.Decide(s, a, r); err != nil {
					t.Errorf("Decide(%q,%q,%q) 失败: %v", s, a, r, err)
					return
				}
				atomic.AddUint64(&decides, 1)
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				n := nodes[(w+i)%len(nodes)]
				s := subjects[(w+i)%len(subjects)]
				a := actions[(w+i)%len(actions)]
				if i%3 == 2 {
					_ = c.DeleteRule(n, s, a)
				} else {
					_ = c.SetRule(n, s, a, i%2 == 0)
				}
			}
		}(w)
	}
	wg.Wait()
	t.Logf("并发完成: Decide 调用=%d", atomic.LoadUint64(&decides))

	// 静止后：每个缓存条目都等于现算结果。
	entries := c.CachedEntries()
	for _, e := range entries {
		want := c.Recompute(e.Subject, e.Action, e.Path)
		if e.Decision != want {
			t.Fatalf("缓存条目 (%q,%q,%q)=%+v 与现算 %+v 不一致",
				e.Subject, e.Action, e.Path, e.Decision, want)
		}
	}
	t.Logf("静止后一致性校验通过: %d 个缓存条目全部等于现算结果", len(entries))

	// 缓存命中必须等于现算：抽样复查若干键。
	for _, s := range subjects {
		for _, a := range actions {
			for _, r := range resources {
				d, err := c.Decide(s, a, r)
				if err != nil {
					t.Fatalf("Decide 失败: %v", err)
				}
				if want := c.Recompute(s, a, r); d != want {
					t.Fatalf("Decide(%q,%q,%q)=%+v 与现算 %+v 不一致", s, a, r, d, want)
				}
			}
		}
	}
	logStats(t, c)
}

// Example 演示基本用法。
func Example() {
	c := New(100)
	_ = c.SetRule("/a", "alice", "read", true)
	d, _ := c.Decide("alice", "read", "/a/b/c")
	fmt.Println(d.Allow)
	// Output: true
}
