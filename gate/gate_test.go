package gate

import (
	"errors"
	"sync"
	"testing"

	"ontology/policy"
	"ontology/tenant"
)

func stAllow(acts, res []string) policy.Statement {
	return policy.Statement{Effect: policy.EffectAllow, Actions: acts, Resources: res}
}
func stDeny(acts, res []string) policy.Statement {
	return policy.Statement{Effect: policy.EffectDeny, Actions: acts, Resources: res}
}
func bsAllow(pr, acts, res []string) policy.BucketStatement {
	return policy.BucketStatement{Statement: stAllow(acts, res), Principals: pr}
}
func bsDeny(pr, acts, res []string) policy.BucketStatement {
	return policy.BucketStatement{Statement: stDeny(acts, res), Principals: pr}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
func wantErrIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

// TestRegistrationAndSetErrors：重复登记、桶同名、暂停未知租户、设置次序与语句合法性。
func TestRegistrationAndSetErrors(t *testing.T) {
	g := New()
	mustOK(t, g.RegisterPrincipal("p1", "A"))
	wantErrIs(t, g.RegisterPrincipal("p1", "B"), tenant.ErrExists)
	mustOK(t, g.CreateBucket("A", "data"))
	wantErrIs(t, g.CreateBucket("B", "data"), tenant.ErrExists)
	wantErrIs(t, g.Suspend("ZZ"), tenant.ErrNotFound)
	wantErrIs(t, g.Resume("ZZ"), tenant.ErrNotFound)
	mustOK(t, g.Suspend("A"))
	mustOK(t, g.Suspend("A")) // 幂等
	mustOK(t, g.Resume("A"))

	// 设置次序：不存在 > 参数非法 > 过多
	wantErrIs(t, g.SetIdentityPolicy("nobody",
		[]policy.Statement{{Effect: "X", Actions: nil, Resources: nil}}), policy.ErrNotFound)
	bad := []policy.Statement{{Effect: "X", Actions: []string{policy.ActionGet}, Resources: []string{"*"}}}
	wantErrIs(t, g.SetIdentityPolicy("p1", bad), policy.ErrInvalid)
	bad = []policy.Statement{stAllow(nil, []string{"*"})}
	wantErrIs(t, g.SetIdentityPolicy("p1", bad), policy.ErrInvalid)
	bad = []policy.Statement{stAllow([]string{policy.ActionGet}, nil)}
	wantErrIs(t, g.SetIdentityPolicy("p1", bad), policy.ErrInvalid)
	bad = []policy.Statement{stAllow([]string{"obj:Bogus"}, []string{"*"})}
	wantErrIs(t, g.SetIdentityPolicy("p1", bad), policy.ErrInvalid)
	bad = []policy.Statement{stAllow([]string{policy.ActionGet}, []string{"a*b"})}
	wantErrIs(t, g.SetIdentityPolicy("p1", bad), policy.ErrInvalid)
	bad = []policy.Statement{stAllow([]string{policy.ActionGet}, []string{"a**"})}
	wantErrIs(t, g.SetIdentityPolicy("p1", bad), policy.ErrInvalid)

	// 不存在优先于过多
	many := make([]policy.Statement, 201)
	for i := range many {
		many[i] = stAllow([]string{"nobodyAct"}, nil)
	}
	wantErrIs(t, g.SetIdentityPolicy("ghost", many), policy.ErrNotFound)
	for i := range many {
		many[i] = stAllow([]string{policy.ActionGet}, nil)
	}
	wantErrIs(t, g.SetIdentityPolicy("p1", many), policy.ErrTooMany)
	// 过多优先于参数非法
	many[200] = stAllow(nil, nil)
	wantErrIs(t, g.SetIdentityPolicy("p1", many), policy.ErrTooMany)
	// 边界与桶策略同样规则
	wantErrIs(t, g.SetBoundary("ghost", many), policy.ErrNotFound)
	wantErrIs(t, g.SetBoundary("p1", many), policy.ErrTooMany)
	manyBkt := make([]policy.BucketStatement, 201)
	for i := range manyBkt {
		manyBkt[i] = bsAllow([]string{"*"}, []string{policy.ActionGet}, nil)
	}
	wantErrIs(t, g.SetBucketPolicy("nobucket", manyBkt), policy.ErrNotFound)
	wantErrIs(t, g.SetBucketPolicy("data", manyBkt), policy.ErrTooMany)
	// 桶策略主体模式非法 / 为空
	wantErrIs(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		{Statement: stAllow([]string{policy.ActionGet}, []string{"*"}), Principals: nil},
	}), policy.ErrInvalid)
	wantErrIs(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		{Statement: stAllow([]string{policy.ActionGet}, []string{"*"}), Principals: []string{"x:Y"}},
	}), policy.ErrInvalid)
	wantErrIs(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		{Statement: stAllow([]string{policy.ActionGet}, []string{"*"}), Principals: []string{"t:"}},
	}), policy.ErrInvalid)

	// 被拒绝的替换不耗纪元（此前无成功替换，纪元仍为 0）
	if ep := g.Policy.Epoch(); ep != 0 {
		t.Fatalf("epoch = %d, want 0 after rejected sets", ep)
	}
	mustOK(t, g.SetIdentityPolicy("p1", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	if ep := g.Policy.Epoch(); ep != 1 {
		t.Fatalf("epoch = %d, want 1", ep)
	}
}

// TestPatternMatching：a/* 与 a、空前缀、List 空串资源的匹配边界。
func TestPatternMatching(t *testing.T) {
	cases := []struct {
		pat, res string
		want     bool
	}{
		{"*", "", true}, {"*", "anything", true},
		{"a/*", "a", false}, {"a/*", "a/", true}, {"a/*", "a/x", true},
		{"a*", "a", true}, {"a*", "ab", true}, {"a*", "b", false},
		{"*x", "", false},
		{"", "", true}, {"", "x", false}, // List 空前缀精确匹配空串
		{"pub/*", "pub", false}, {"pub/*", "pub/", true}, {"pub/*", "pub/x", true},
	}
	for _, c := range cases {
		if !policy.ValidResourcePattern(c.pat) && c.pat != "*x" {
			t.Fatalf("pattern %q should be valid syntactically unless * mid", c.pat)
		}
		if c.pat == "*x" {
			if policy.ValidResourcePattern(c.pat) {
				t.Fatalf("*x must be invalid")
			}
			continue
		}
		if got := policy.MatchResource(c.pat, c.res); got != c.want {
			t.Fatalf("MatchResource(%q,%q)=%v want %v", c.pat, c.res, got, c.want)
		}
	}
}

// setupExample 复现题述示例：A 拥有 data，桶策略 Allow t:B Get pub/*。
func setupExample(t *testing.T) *Gateway {
	g := New()
	mustOK(t, g.RegisterPrincipal("B/u", "B"))
	mustOK(t, g.RegisterPrincipal("A/v", "A"))
	mustOK(t, g.CreateBucket("A", "data"))
	mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bsAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"pub/*"}),
	}))
	return g
}

func TestCheckArgumentOrder(t *testing.T) {
	g := setupExample(t)
	dec := g.Check("B/u", "obj:Bogus", "data", "pub/x")
	if dec.Reason != ReasonInvalid {
		t.Fatalf("got %q", dec.Reason)
	}
	dec = g.Check("ghost", policy.ActionGet, "data", "pub/x")
	if dec.Reason != ReasonInvalid {
		t.Fatalf("unregistered principal: got %q", dec.Reason)
	}
	dec = g.Check("B/u", policy.ActionGet, "data", "")
	if dec.Reason != ReasonInvalid {
		t.Fatalf("empty non-List resource: got %q", dec.Reason)
	}
	// List 空前缀合法；这里桶策略只允许 Get，故跨租户缺身份许可。
	dec = g.Check("B/u", policy.ActionList, "data", "")
	if dec.Reason != ReasonNoIdentity {
		t.Fatalf("List empty prefix: got %q", dec.Reason)
	}
	dec = g.Check("B/u", policy.ActionGet, "nobucket", "pub/x")
	if dec.Reason != ReasonNoBucket {
		t.Fatalf("got %q", dec.Reason)
	}
}

// TestUnionIntersection：同租户并集、跨租户交集各四种组合。
func TestUnionIntersection(t *testing.T) {
	type combo struct {
		idAllow, bktAllow bool
		wantAllowed       bool
		wantReason        string
	}
	// 跨租户交集
	cross := []combo{
		{false, false, false, ReasonNoIdentity},
		{true, false, false, ReasonNoResource},
		{false, true, false, ReasonNoIdentity},
		{true, true, true, ""},
	}
	for _, c := range cross {
		g := setupExample(t)
		if c.idAllow {
			mustOK(t, g.SetIdentityPolicy("B/u", []policy.Statement{
				stAllow([]string{policy.ActionGet}, []string{"*"}),
			}))
		}
		if !c.bktAllow {
			mustOK(t, g.SetBucketPolicy("data", nil)) // 去掉桶 Allow
		}
		dec := g.Check("B/u", policy.ActionGet, "data", "pub/x")
		if dec.Allowed != c.wantAllowed || dec.Reason != c.wantReason {
			t.Fatalf("cross %+v: got allowed=%v reason=%q", c, dec.Allowed, dec.Reason)
		}
	}

	// 同租户并集：桶策略改为 t:A Put *
	same := []combo{
		{false, false, false, ReasonNoPermission},
		{true, false, true, ""},
		{false, true, true, ""},
		{true, true, true, ""},
	}
	for _, c := range same {
		g := setupExample(t)
		pat := []string{"t:A"}
		if !c.bktAllow {
			pat = []string{"t:B"} // 不适用于 A/v：制造“无桶许可”
		}
		mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
			bsAllow(pat, []string{policy.ActionPut}, []string{"*"}),
		}))
		if c.idAllow {
			mustOK(t, g.SetIdentityPolicy("A/v", []policy.Statement{
				stAllow([]string{policy.ActionPut}, []string{"*"}),
			}))
		}
		dec := g.Check("A/v", policy.ActionPut, "data", "k")
		if dec.Allowed != c.wantAllowed || dec.Reason != c.wantReason {
			t.Fatalf("same %+v: got allowed=%v reason=%q", c, dec.Allowed, dec.Reason)
		}
	}
}

// TestExampleNarrative：题述逐句示例。
func TestExampleNarrative(t *testing.T) {
	g := setupExample(t)
	mustOK(t, g.SetIdentityPolicy("B/u", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	checks := []struct {
		res  string
		want string
	}{
		{"pub/x", ""},
		{"priv/x", ReasonNoResource},
		{"pub", ReasonNoResource}, // pub/* 不匹配 pub
	}
	for _, c := range checks {
		dec := g.Check("B/u", policy.ActionGet, "data", c.res)
		if dec.Reason != c.want {
			t.Fatalf("res=%s got %q want %q", c.res, dec.Reason, c.want)
		}
	}

	// 无身份策略 -> 缺身份许可
	g2 := setupExample(t)
	dec := g2.Check("B/u", policy.ActionGet, "data", "pub/x")
	if dec.Reason != ReasonNoIdentity {
		t.Fatalf("got %q", dec.Reason)
	}

	// 加桶 Deny，压过身份 Allow
	mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bsAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"pub/*"}),
		bsAllow([]string{"t:A"}, []string{policy.ActionPut}, []string{"*"}),
		bsDeny([]string{"*"}, []string{policy.ActionDelete}, []string{"pub/keep*"}),
	}))
	mustOK(t, g.SetIdentityPolicy("A/v", []policy.Statement{
		stAllow([]string{policy.ActionDelete}, []string{"*"}),
	}))
	dec = g.Check("A/v", policy.ActionDelete, "data", "pub/keep1")
	if dec.Reason != ReasonDeny {
		t.Fatalf("deny should win: got %q", dec.Reason)
	}

	// 暂停次序：Suspend(A)
	mustOK(t, g.Suspend("A"))
	dec = g.Check("B/u", policy.ActionGet, "data", "pub/x")
	if !dec.Allowed {
		t.Fatalf("cross-tenant read still allowed, got %q", dec.Reason)
	}
	dec = g.Check("B/u", policy.ActionPut, "data", "x")
	if dec.Reason != ReasonBucketFrozen {
		t.Fatalf("owner frozen write: got %q", dec.Reason)
	}
	dec = g.Check("A/v", policy.ActionGet, "data", "x")
	if dec.Reason != ReasonTenantPaused {
		t.Fatalf("own tenant paused first: got %q", dec.Reason)
	}
	mustOK(t, g.Resume("A"))
	dec = g.Check("B/u", policy.ActionGet, "data", "pub/x")
	if !dec.Allowed {
		t.Fatalf("after resume: got %q", dec.Reason)
	}
}

// setupBoundary：B/u 身份 Allow Get *，桶 data(A) 允许 t:B Get pub/*。
func setupBoundary(t *testing.T) *Gateway {
	g := New()
	mustOK(t, g.RegisterPrincipal("B/u", "B"))
	mustOK(t, g.CreateBucket("A", "data"))
	mustOK(t, g.SetIdentityPolicy("B/u", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bsAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"pub/*"}),
	}))
	return g
}

// TestBoundary：边界 Allow/Deny、空列表、Deny 优先于越出边界。
func TestBoundary(t *testing.T) {
	g := setupBoundary(t)
	mustOK(t, g.SetBoundary("B/u", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"pub/a*"}),
	}))
	dec := g.Check("B/u", policy.ActionGet, "data", "pub/x")
	if dec.Reason != ReasonOutOfBoundary {
		t.Fatalf("got %q want %s", dec.Reason, ReasonOutOfBoundary)
	}
	dec = g.Check("B/u", policy.ActionGet, "data", "pub/ab")
	if !dec.Allowed {
		t.Fatalf("pub/ab should be allowed, got %q (%s)", dec.Reason, dec.Basis)
	}

	// 空列表边界：一切越出边界
	g2 := setupBoundary(t)
	mustOK(t, g2.SetBoundary("B/u", []policy.Statement{}))
	dec = g2.Check("B/u", policy.ActionGet, "data", "pub/ab")
	if dec.Reason != ReasonOutOfBoundary {
		t.Fatalf("empty boundary: got %q", dec.Reason)
	}

	// 边界 Deny：报命中 Deny 而非越出边界（且先于跨租户缺资源检查）
	g3 := setupBoundary(t)
	mustOK(t, g3.SetBoundary("B/u", []policy.Statement{
		stDeny([]string{"obj:*"}, []string{"*"}),
	}))
	dec = g3.Check("B/u", policy.ActionGet, "data", "priv/x")
	if dec.Reason != ReasonDeny {
		t.Fatalf("boundary deny: got %q", dec.Reason)
	}
}

// TestEpochRejectedNoBump：被拒绝替换纪元不变；成功替换纪元加 1 且结果带纪元。
func TestEpochRejectedNoBump(t *testing.T) {
	g := New()
	mustOK(t, g.RegisterPrincipal("B/u", "B"))
	mustOK(t, g.CreateBucket("A", "data"))
	if ep := g.Policy.Epoch(); ep != 0 {
		t.Fatalf("initial epoch = %d want 0", ep)
	}
	if err := g.SetIdentityPolicy("B/u", []policy.Statement{stAllow(nil, nil)}); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if g.Policy.Epoch() != 0 {
		t.Fatal("rejected set must not bump epoch")
	}
	mustOK(t, g.SetBoundary("B/u", nil))
	if g.Policy.Epoch() != 1 {
		t.Fatal("accepted empty-boundary set must bump epoch")
	}
	dec := g.Check("B/u", policy.ActionGet, "data", "pub/a")
	if dec.Epoch != 1 {
		t.Fatalf("decision epoch %d", dec.Epoch)
	}
}

// TestOrderingTenantBeforeFrozenBeforeDeny：租户暂停先于冻结先于 Deny。
func TestOrderingTenantBeforeFrozenBeforeDeny(t *testing.T) {
	g := New()
	mustOK(t, g.RegisterPrincipal("A/v", "A"))
	mustOK(t, g.RegisterPrincipal("B/u", "B"))
	mustOK(t, g.CreateBucket("A", "data"))
	mustOK(t, g.SetIdentityPolicy("A/v", []policy.Statement{stAllow([]string{"obj:*"}, []string{"*"})}))
	mustOK(t, g.SetIdentityPolicy("B/u", []policy.Statement{stAllow([]string{"obj:*"}, []string{"*"})}))
	mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bsDeny([]string{"*"}, []string{"obj:*"}, []string{"*"}),
		bsAllow([]string{"t:B"}, []string{"obj:*"}, []string{"*"}),
	}))
	mustOK(t, g.Suspend("A"))
	// A/v 先报租户暂停，即使桶有 Deny。
	if dec := g.Check("A/v", policy.ActionPut, "data", "x"); dec.Reason != ReasonTenantPaused {
		t.Fatalf("A/v got %q", dec.Reason)
	}
	// B/u 写：冻结先于 Deny。
	if dec := g.Check("B/u", policy.ActionPut, "data", "x"); dec.Reason != ReasonBucketFrozen {
		t.Fatalf("B/u write got %q", dec.Reason)
	}
	// B/u 读：未冻结，落到 Deny。
	if dec := g.Check("B/u", policy.ActionGet, "data", "x"); dec.Reason != ReasonDeny {
		t.Fatalf("B/u read got %q", dec.Reason)
	}
}

// TestProbesInvariant：无关动作语句在两档下，同一次 Check 的 probes 相同。
func TestProbesInvariant(t *testing.T) {
	build := func(irrelevant int) int {
		g := New()
		mustOK(t, g.RegisterPrincipal("A/v", "A"))
		mustOK(t, g.CreateBucket("A", "data"))
		stmts := []policy.Statement{stAllow([]string{policy.ActionGet}, []string{"*"})}
		for i := 0; i < irrelevant; i++ {
			stmts = append(stmts, stAllow([]string{policy.ActionPut, policy.ActionDelete}, []string{"*"}))
		}
		mustOK(t, g.SetIdentityPolicy("A/v", stmts))
		mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
			bsAllow([]string{"t:A"}, []string{policy.ActionGet}, []string{"*"}),
		}))
		dec := g.Check("A/v", policy.ActionGet, "data", "k")
		if !dec.Allowed {
			t.Fatalf("want allowed got %q", dec.Reason)
		}
		return g.LastProbes()
	}
	p10 := build(10)
	p198 := build(198) // 1 条 Get + 198 条仅写，顶到 200 上限
	if p10 != p198 {
		t.Fatalf("probes differ: %d vs %d", p10, p198)
	}
	if p10 != 2 { // 身份 Get 1 条 + 桶 Get 1 条
		t.Fatalf("probes = %d, want 2", p10)
	}
}

// TestConcurrentCheckEpoch：并发 Set/Check 下，每次 Check 的纪元对应某个完整快照。
func TestConcurrentCheckEpoch(t *testing.T) {
	g := New()
	mustOK(t, g.RegisterPrincipal("A/v", "A"))
	mustOK(t, g.CreateBucket("A", "data"))
	mustOK(t, g.SetIdentityPolicy("A/v", []policy.Statement{stAllow([]string{"obj:*"}, []string{"*"})}))
	mustOK(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bsAllow([]string{"t:A"}, []string{"obj:*"}, []string{"*"}),
	}))
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = g.SetBucketPolicy("data", []policy.BucketStatement{
					bsAllow([]string{"t:A"}, []string{"obj:*"}, []string{"*"}),
				})
			}
		}()
	}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				dec := g.Check("A/v", policy.ActionGet, "data", "k")
				if dec.Epoch < 1 || dec.Reason != "" {
					t.Errorf("epoch=%d reason=%q", dec.Epoch, dec.Reason)
				}
			}
		}()
	}
	wg.Wait()
}
