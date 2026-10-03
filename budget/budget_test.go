package budget

import (
	"errors"
	"testing"

	"ontology/cluster"
)

func setup(t *testing.T) (*Authorizer, *cluster.Merger) {
	t.Helper()
	m, err := cluster.New(50, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	a := New(m)

	m.Ingest("alice-tenant", "open file a.txt ok")
	m.Ingest("alice-tenant", "open file b.txt ok")
	m.Ingest("bob-tenant", "close door now")
	m.SetTheta(60)
	m.Ingest("alice-tenant", "open file 7.txt ok")
	m.Ingest("alice-tenant", "delete all files") // 新叶子 → gen=1 新建 id2
	return a, m
}

func TestGrantAndTemplates(t *testing.T) {
	a, _ := setup(t)
	if err := a.Grant("svc1", "alice-tenant"); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("svc1", "alice-tenant"); err != nil { // 幂等
		t.Fatalf("重复 Grant 应幂等成功: %v", err)
	}
	entries, overflow, err := a.Templates("svc1", "alice-tenant")
	if err != nil || overflow != 0 || len(entries) != 2 {
		t.Fatalf("Templates 异常: entries=%+v overflow=%d err=%v", entries, overflow, err)
	}
	e := entries[0]
	if e.ID != 1 || e.Text != "open file <*> ok" || e.Count != 3 || e.Gen != 0 {
		t.Fatalf("模板字段不符: %+v", e)
	}
	if entries[1].ID != 2 || entries[1].Gen != 1 {
		t.Fatalf("新建模板应带创建时 Gen=1: %+v", entries[1])
	}
	t.Logf("授权读取成功: caller=svc1 tenant=alice-tenant => {ID:%d Text:%q Count:%d Gen:%d}",
		e.ID, e.Text, e.Count, e.Gen)
}

func TestRejectOrder(t *testing.T) {
	a, _ := setup(t)

	// 1. 参数非法最先（即使未授权、租户不存在）。
	if _, _, err := a.Templates("", "ghost"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空 caller 应报参数非法, got %v", err)
	}
	if _, _, err := a.Templates("svc", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空 tenant 应报参数非法, got %v", err)
	}
	if err := a.Grant("", "x"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空参 Grant 应报参数非法, got %v", err)
	}
	// 2. 未授权优先于租户不存在：不得借此探测租户。
	if _, _, err := a.Templates("nobody", "alice-tenant"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("已存在租户未授权应报未授权, got %v", err)
	}
	if _, _, err := a.Templates("nobody", "ghost"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("不存在租户也应先报未授权, got %v", err)
	}
	// 3. 已授权但租户不存在。
	if err := a.Grant("svc2", "ghost"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Templates("svc2", "ghost"); !errors.Is(err, cluster.ErrNoSuchTenant) {
		t.Fatalf("授权但租户不存在应透传 ErrNoSuchTenant, got %v", err)
	}
	t.Log("拒绝顺序断言通过: 参数非法 > 未授权 > 租户不存在")
}

func TestExactGrantNoCrossTenant(t *testing.T) {
	a, _ := setup(t)
	if err := a.Grant("svc1", "alice-tenant"); err != nil {
		t.Fatal(err)
	}
	// 仅有 alice 授权不能读 bob；授权精确到 (caller, tenant)。
	if _, _, err := a.Templates("svc1", "bob-tenant"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("不得由其他租户授权推出: %v", err)
	}
	if err := a.Grant("svc2", "bob-tenant"); err != nil {
		t.Fatal(err)
	}
	bob, _, err := a.Templates("svc2", "bob-tenant")
	if err != nil || len(bob) != 1 || bob[0].Text != "close door now" {
		t.Fatalf("svc2 应只能读 bob: %+v err=%v", bob, err)
	}
	if _, _, err := a.Templates("svc2", "alice-tenant"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("svc2 不得读 alice: %v", err)
	}
	t.Logf("精确匹配断言: svc1→alice=%q, svc2→bob=%q, 交叉读取均被拒",
		"open <*> <*> <*>", bob[0].Text)
}

func TestOverflowVisible(t *testing.T) {
	m, err := cluster.New(99, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	a := New(m)
	m.Ingest("t", "a b c d")
	m.Ingest("t", "x x x x") // 无近似命中且 Tmax=1 → 溢出
	if err := a.Grant("ro", "t"); err != nil {
		t.Fatal(err)
	}
	entries, overflow, err := a.Templates("ro", "t")
	if err != nil || len(entries) != 1 || overflow != 1 {
		t.Fatalf("溢出桶计数应可见: entries=%v overflow=%d err=%v", entries, overflow, err)
	}
	if entries[0].Text != "a b c d" || entries[0].Count != 1 {
		t.Fatalf("原模板应不受溢出影响: %+v", entries[0])
	}
	t.Logf("授权可见溢出桶: overflow=%d, 模板未被污染: %q", overflow, entries[0].Text)
}
