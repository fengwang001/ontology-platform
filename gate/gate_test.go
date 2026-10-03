package gate_test

import (
	"errors"
	"testing"

	"ontology/gate"
	"ontology/policy"
)

func stAllow(actions, resources []string) policy.Statement {
	return policy.Statement{Effect: policy.Allow, Actions: actions, Resources: resources}
}

func stDeny(actions, resources []string) policy.Statement {
	return policy.Statement{Effect: policy.Deny, Actions: actions, Resources: resources}
}

func bkAllow(principals, actions, resources []string) policy.BucketStatement {
	return policy.BucketStatement{Statement: stAllow(actions, resources), Principals: principals}
}

func bkDeny(principals, actions, resources []string) policy.BucketStatement {
	return policy.BucketStatement{Statement: stDeny(actions, resources), Principals: principals}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResourcePatterns a/* 与 a、空前缀、* 的边界。
func TestResourcePatterns(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "T"))
	must(t, g.CreateBucket("T", "b"))
	must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"*"}, []string{policy.ActionGet, policy.ActionList}, []string{"a/*"}),
	}))

	checkReason(t, g, "p", policy.ActionGet, "b", "a", gate.ReasonNoPermit)
	checkReason(t, g, "p", policy.ActionGet, "b", "a/", gate.ReasonOK)
	checkReason(t, g, "p", policy.ActionGet, "b", "a/x", gate.ReasonOK)
	checkReason(t, g, "p", policy.ActionGet, "b", "ab", gate.ReasonNoPermit)

	// List 资源为空前缀，a/* 不匹配空串。
	checkReason(t, g, "p", policy.ActionList, "b", "", gate.ReasonNoPermit)

	// * 匹配空串（List 空前缀）与任意键；非 List 空键非法。
	must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"*"}, []string{policy.ActionGet, policy.ActionList}, []string{"*"}),
	}))
	checkReason(t, g, "p", policy.ActionList, "b", "", gate.ReasonOK)
	checkReason(t, g, "p", policy.ActionGet, "b", "", gate.ReasonBadRequest)
	checkReason(t, g, "p", policy.ActionGet, "b", "anything", gate.ReasonOK)

	// 空串精确模式只匹配空前缀。
	must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"*"}, []string{policy.ActionList}, []string{""}),
	}))
	checkReason(t, g, "p", policy.ActionList, "b", "", gate.ReasonOK)
	checkReason(t, g, "p", policy.ActionList, "b", "x", gate.ReasonNoPermit)

	// * 出现在非末尾位置为非法。
	err := g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"*"}, []string{policy.ActionGet}, []string{"a*b"}),
	})
	if !errors.Is(err, policy.ErrBadPolicy) {
		t.Fatalf("a*b should be illegal, got %v", err)
	}
}

// TestDenyPrecedence Deny 压过任何 Allow，含边界与桶策略 Deny。
func TestDenyPrecedence(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "B"))
	must(t, g.CreateBucket("A", "b"))
	must(t, g.SetIdentityPolicy("p", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"*"}),
	}))
	checkReason(t, g, "p", policy.ActionGet, "b", "k", gate.ReasonOK)

	// 身份 Deny。
	must(t, g.SetIdentityPolicy("p", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
		stDeny([]string{policy.ActionGet}, []string{"secret*"}),
	}))
	checkReason(t, g, "p", policy.ActionGet, "b", "secret/x", gate.ReasonDeny)

	// 边界 Deny 先于越出边界。
	must(t, g.SetIdentityPolicy("p", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	must(t, g.SetBoundary("p", []policy.Statement{
		stDeny([]string{policy.ActionAll}, []string{"*"}),
	}))
	checkReason(t, g, "p", policy.ActionGet, "b", "k", gate.ReasonDeny)

	// 空边界 + 桶 Deny：桶 Deny 先于越出边界；不适用的主体模式不命中。
	must(t, g.SetBoundary("p", []policy.Statement{}))
	must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"*"}),
		bkDeny([]string{"p:z"}, []string{policy.ActionGet}, []string{"*"}),
		bkDeny([]string{"t:B"}, []string{policy.ActionGet}, []string{"block*"}),
	}))
	checkReason(t, g, "p", policy.ActionGet, "b", "k", gate.ReasonOutOfBound)
	must(t, g.SetBoundary("p", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	checkReason(t, g, "p", policy.ActionGet, "b", "k", gate.ReasonOK)
	checkReason(t, g, "p", policy.ActionGet, "b", "blocked", gate.ReasonDeny)
}

// TestSuspensionOrder 租户暂停 > 冻结 > Deny。
func TestSuspensionOrder(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "B"))
	must(t, g.CreateBucket("A", "b"))
	must(t, g.SetIdentityPolicy("p", []policy.Statement{
		stAllow([]string{policy.ActionPut}, []string{"*"}),
		stDeny([]string{policy.ActionPut}, []string{"*"}),
	}))
	must(t, g.SetBucketPolicy("b", []policy.BucketStatement{
		bkAllow([]string{"t:B"}, []string{policy.ActionPut}, []string{"*"}),
		bkDeny([]string{"*"}, []string{policy.ActionPut}, []string{"*"}),
	}))

	must(t, g.Suspend("A")) // 属主暂停：写冻结
	checkReason(t, g, "p", policy.ActionPut, "b", "k", gate.ReasonFrozen)
	must(t, g.Suspend("B")) // 主体租户暂停：先于冻结
	checkReason(t, g, "p", policy.ActionPut, "b", "k", gate.ReasonTenantSusp)

	must(t, g.Resume("B"))
	checkReason(t, g, "p", policy.ActionPut, "b", "k", gate.ReasonFrozen)
	must(t, g.Resume("A"))
	checkReason(t, g, "p", policy.ActionPut, "b", "k", gate.ReasonDeny)

	// 暂停/恢复幂等且不耗纪元。
	epoch := g.Epoch()
	must(t, g.Suspend("A"))
	must(t, g.Suspend("A"))
	must(t, g.Resume("A"))
	must(t, g.Resume("A"))
	if g.Epoch() != epoch {
		t.Fatalf("suspend/resume changed epoch: %d != %d", g.Epoch(), epoch)
	}
}

// TestReasonOrder 参数非法 > 桶不存在 > 租户暂停 > 冻结。
func TestReasonOrder(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "B"))
	must(t, g.CreateBucket("A", "b"))
	must(t, g.Suspend("B"))
	must(t, g.Suspend("A"))

	checkReason(t, g, "p", "obj:Evil", "b", "k", gate.ReasonBadRequest)
	checkReason(t, g, "p", policy.ActionGet, "b", "", gate.ReasonBadRequest)
	checkReason(t, g, "ghost", policy.ActionGet, "b", "k", gate.ReasonBadRequest)
	checkReason(t, g, "p", policy.ActionGet, "nobucket", "k", gate.ReasonNoBucket)

	// 已登记主体但桶不存在：桶不存在在暂停之前。
	must(t, g.Resume("B"))
	must(t, g.Resume("A"))
	checkReason(t, g, "p", policy.ActionGet, "nobucket", "k", gate.ReasonNoBucket)
	must(t, g.Suspend("B"))
	checkReason(t, g, "p", policy.ActionGet, "nobucket", "k", gate.ReasonNoBucket)

	// 不认识的租户 Suspend/Resume 报不存在。
	if err := g.Suspend("Z"); !errors.Is(err, gate.ErrTenantNotFound) {
		t.Fatalf("Suspend unknown = %v, want not found", err)
	}
	if err := g.Resume("Z"); !errors.Is(err, gate.ErrTenantNotFound) {
		t.Fatalf("Resume unknown = %v, want not found", err)
	}
}

// TestRegistrationErrors 重复登记、未知对象设置策略、校验次序。
func TestRegistrationErrors(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "T"))
	must(t, g.CreateBucket("T", "b"))

	if err := g.RegisterPrincipal("p", "X"); !errors.Is(err, gate.ErrAlreadyExists) {
		t.Fatalf("dup principal = %v", err)
	}
	if err := g.CreateBucket("T2", "b"); !errors.Is(err, gate.ErrAlreadyExists) {
		t.Fatalf("dup bucket = %v", err)
	}

	if err := g.SetIdentityPolicy("ghost", nil); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("set pol unknown principal = %v", err)
	}
	if err := g.SetBoundary("ghost", nil); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("set boundary unknown principal = %v", err)
	}
	if err := g.SetBucketPolicy("ghost", nil); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("set pol unknown bucket = %v", err)
	}

	// 次序：不存在 > 参数非法 > 过多。
	badAndMany := make([]policy.Statement, policy.MaxStatements+1)
	for i := range badAndMany {
		badAndMany[i] = stAllow([]string{"obj:Bogus"}, []string{"*"})
	}
	if err := g.SetIdentityPolicy("ghost", badAndMany); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("order: want not found, got %v", err)
	}
	if err := g.SetIdentityPolicy("p", badAndMany); !errors.Is(err, gate.ErrBadPolicy) {
		t.Fatalf("order: want bad policy, got %v", err)
	}
	many := make([]policy.Statement, policy.MaxStatements+1)
	for i := range many {
		many[i] = stAllow([]string{policy.ActionGet}, []string{"*"})
	}
	if err := g.SetIdentityPolicy("p", many); !errors.Is(err, gate.ErrTooManyStatements) {
		t.Fatalf("want too many, got %v", err)
	}

	// 非法语句其他形态。
	for _, bad := range [][]policy.Statement{
		{{Effect: "Maybe", Actions: []string{policy.ActionGet}, Resources: []string{"*"}}},
		{stAllow(nil, []string{"*"})},
		{stAllow([]string{policy.ActionGet}, nil)},
	} {
		if err := g.SetIdentityPolicy("p", bad); !errors.Is(err, gate.ErrBadPolicy) {
			t.Fatalf("bad stmt %v -> %v", bad, err)
		}
	}
}

// TestEpoch 接受的整份替换 +1；被拒绝不耗纪元；Decision 携带纪元。
func TestEpoch(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("p", "T"))
	must(t, g.CreateBucket("T", "b"))
	if e := g.Epoch(); e != 0 {
		t.Fatalf("initial epoch = %d", e)
	}
	must(t, g.SetIdentityPolicy("p", nil))
	must(t, g.SetBoundary("p", nil))
	must(t, g.SetBucketPolicy("b", nil))
	if e := g.Epoch(); e != 3 {
		t.Fatalf("epoch after 3 sets = %d", e)
	}
	// 再次整份替换（包括相同内容）仍 +1。
	must(t, g.SetIdentityPolicy("p", nil))
	if e := g.Epoch(); e != 4 {
		t.Fatalf("epoch after replace = %d", e)
	}
	// 被拒绝不耗纪元。
	_ = g.SetIdentityPolicy("ghost", nil)
	_ = g.SetIdentityPolicy("p", []policy.Statement{
		{Effect: policy.Allow, Actions: []string{"x"}, Resources: []string{"*"}},
	})
	if e := g.Epoch(); e != 4 {
		t.Fatalf("epoch changed after rejected sets: %d", e)
	}
	d := g.Check("p", policy.ActionGet, "b", "k")
	if d.Epoch != 4 {
		t.Fatalf("decision epoch = %d, want 4", d.Epoch)
	}
}

func checkReason(t *testing.T, g *gate.Gate, p, action, bucket, resource string, want gate.Reason) {
	t.Helper()
	d := g.Check(p, action, bucket, resource)
	if d.Reason != want {
		t.Fatalf("Check(%s,%s,%s,%q) = %s (epoch %d), want %s",
			p, action, bucket, resource, d.Reason, d.Epoch, want)
	}
}

// TestExamples 题目给出的端到端例子。
func TestExamples(t *testing.T) {
	g := gate.New()
	must(t, g.RegisterPrincipal("B/u", "B"))
	must(t, g.RegisterPrincipal("A/v", "A"))
	must(t, g.CreateBucket("A", "data"))

	must(t, g.SetIdentityPolicy("B/u", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))
	must(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bkAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"pub/*"}),
	}))

	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/x", gate.ReasonOK)
	checkReason(t, g, "B/u", policy.ActionGet, "data", "priv/x", gate.ReasonNoResource)
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub", gate.ReasonNoResource)

	// 无身份策略：缺身份许可。
	must(t, g.SetIdentityPolicy("B/u", nil))
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/x", gate.ReasonNoIdentity)
	must(t, g.SetIdentityPolicy("B/u", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"*"}),
	}))

	// 同租户并集：仅桶策略 Allow 即可；Deny 压过身份 Allow。
	must(t, g.SetBucketPolicy("data", []policy.BucketStatement{
		bkAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"pub/*"}),
		bkAllow([]string{"t:A"}, []string{policy.ActionPut}, []string{"*"}),
		bkDeny([]string{"*"}, []string{policy.ActionAll}, []string{"pub/keep*"}),
	}))
	must(t, g.SetIdentityPolicy("A/v", []policy.Statement{
		stAllow([]string{policy.ActionDelete}, []string{"*"}),
	}))
	checkReason(t, g, "A/v", policy.ActionPut, "data", "anything", gate.ReasonOK)
	checkReason(t, g, "A/v", policy.ActionDelete, "data", "pub/keep1", gate.ReasonDeny)

	// 暂停 A：A/v 一切先报租户已暂停；B/u 读仍允许，写报桶被冻结。
	must(t, g.Suspend("A"))
	checkReason(t, g, "A/v", policy.ActionGet, "data", "x", gate.ReasonTenantSusp)
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/x", gate.ReasonOK)
	checkReason(t, g, "B/u", policy.ActionPut, "data", "pub/x", gate.ReasonFrozen)
	must(t, g.Resume("A"))
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/x", gate.ReasonOK)
	checkReason(t, g, "A/v", policy.ActionDelete, "data", "pub/keep1", gate.ReasonDeny)

	// 边界例子。
	must(t, g.SetBoundary("B/u", []policy.Statement{
		stAllow([]string{policy.ActionGet}, []string{"pub/a*"}),
	}))
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/x", gate.ReasonOutOfBound)
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/ab", gate.ReasonOK)
	must(t, g.SetBoundary("B/u", []policy.Statement{}))
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/ab", gate.ReasonOutOfBound)
	must(t, g.SetBoundary("B/u", []policy.Statement{
		stDeny([]string{policy.ActionAll}, []string{"*"}),
	}))
	checkReason(t, g, "B/u", policy.ActionGet, "data", "pub/ab", gate.ReasonDeny)
}

// TestSameTenantUnion 同租户身份/桶 Allow 的四种组合。
func TestSameTenantUnion(t *testing.T) {
	cases := []struct {
		name  string
		idPol []policy.Statement
		bkPol []policy.BucketStatement
		want  gate.Reason
	}{
		{"none-none", nil, nil, gate.ReasonNoPermit},
		{"id-only",
			[]policy.Statement{stAllow([]string{policy.ActionGet}, []string{"*"})},
			nil, gate.ReasonOK},
		{"bucket-only",
			nil,
			[]policy.BucketStatement{bkAllow([]string{"*"}, []string{policy.ActionGet}, []string{"*"})},
			gate.ReasonOK},
		{"both",
			[]policy.Statement{stAllow([]string{policy.ActionGet}, []string{"*"})},
			[]policy.BucketStatement{bkAllow([]string{"*"}, []string{policy.ActionGet}, []string{"*"})},
			gate.ReasonOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := gate.New()
			must(t, g.RegisterPrincipal("p", "T"))
			must(t, g.CreateBucket("T", "b"))
			if c.idPol != nil {
				must(t, g.SetIdentityPolicy("p", c.idPol))
			}
			if c.bkPol != nil {
				must(t, g.SetBucketPolicy("b", c.bkPol))
			}
			checkReason(t, g, "p", policy.ActionGet, "b", "k", c.want)
		})
	}
}

// TestCrossTenantIntersection 跨租户身份/桶 Allow 的四种组合。
func TestCrossTenantIntersection(t *testing.T) {
	cases := []struct {
		name string
		idOK bool
		bkOK bool
		want gate.Reason
	}{
		{"none-none", false, false, gate.ReasonNoIdentity},
		{"id-only", true, false, gate.ReasonNoResource},
		{"bucket-only", false, true, gate.ReasonNoIdentity},
		{"both", true, true, gate.ReasonOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := gate.New()
			must(t, g.RegisterPrincipal("p", "B"))
			must(t, g.CreateBucket("A", "b"))
			if c.idOK {
				must(t, g.SetIdentityPolicy("p",
					[]policy.Statement{stAllow([]string{policy.ActionGet}, []string{"*"})}))
			}
			if c.bkOK {
				must(t, g.SetBucketPolicy("b",
					[]policy.BucketStatement{bkAllow([]string{"t:B"}, []string{policy.ActionGet}, []string{"*"})}))
			}
			checkReason(t, g, "p", policy.ActionGet, "b", "k", c.want)
		})
	}
}
