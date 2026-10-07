package quota

import "testing"

// TestAddQuotaRecomputesUsage 新增配额时按当前已存在的 Pod 重新得出用量。
func TestAddQuotaRecomputesUsage(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	// 先建 Pod，再加配额。
	if err := c.CreatePod("ns", Pod{Name: "p1", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 2},
		Limits:   map[string]int64{"cpu": 4},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p2", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 3},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		ResourcePods: 10, "requests.cpu": 100, "limits.cpu": 100,
	}}); err != nil {
		t.Fatal(err)
	}
	u := usageOf(t, c, "ns", "q")
	t.Logf("新增配额后用量=%v; 依据 按已存在 Pod 重算: pods=2, requests.cpu=5, limits.cpu=4(p2 缺省计 0)", u)
	if u[ResourcePods] != 2 || u["requests.cpu"] != 5 || u["limits.cpu"] != 4 {
		t.Fatalf("重算用量不符: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestLowerHardBelowUsage 上限下调到低于当前用量：允许，已有 Pod 不驱逐，
// 之后增加该资源用量的准入被拒，不增加的准入不受影响。
func TestLowerHardBelowUsage(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		"requests.cpu": 10, ResourcePods: 10,
	}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"p1", "p2"} {
		if err := c.CreatePod("ns", Pod{Name: name, Spec: PodSpec{
			Requests: map[string]int64{"cpu": 4},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// 下调 requests.cpu 到 5（当前用量 8）。
	if err := c.UpdateQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		"requests.cpu": 5, ResourcePods: 10,
	}}); err != nil {
		t.Fatalf("下调硬上限应允许: %v", err)
	}
	u := usageOf(t, c, "ns", "q")
	t.Logf("下调后用量=%v; 依据 已有 Pod 不被驱逐, 用量仍为 8", u)
	if u["requests.cpu"] != 8 {
		t.Fatalf("已有 Pod 不应被驱逐: %v", u)
	}
	// 增加该资源用量的创建被拒。
	err := c.CreatePod("ns", Pod{Name: "p3", Spec: PodSpec{Requests: map[string]int64{"cpu": 1}}})
	t.Logf("输出 err=%v; 依据 8+1>5 超限", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	// 不增加该资源用量的创建（cpu=0）不受影响。
	if err := c.CreatePod("ns", Pod{Name: "p4", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 0},
	}}); err != nil {
		t.Fatalf("不增加用量的准入应通过: %v", err)
	}
	// 减小用量的原位调整允许。
	if err := c.UpdatePod("ns", Pod{Name: "p1", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1},
	}}); err != nil {
		t.Fatalf("减小用量应允许: %v", err)
	}
	// 增大用量的原位调整被拒。
	err = c.UpdatePod("ns", Pod{Name: "p2", Spec: PodSpec{Requests: map[string]int64{"cpu": 5}}})
	t.Logf("输出 err=%v; 依据 当前 5(8-4+1)+增量 4>5", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	u = usageOf(t, c, "ns", "q")
	t.Logf("最终用量=%v; 依据 p1 4->1 释放 3, p4 +0", u)
	if u["requests.cpu"] != 5 {
		t.Fatalf("用量不符: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestDeleteQuota 删除配额后不再限制。
func TestDeleteQuota(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{ResourcePods: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p2"}); !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	if err := c.DeleteQuota("ns", "q"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p2"}); err != nil {
		t.Fatalf("删除配额后不应再限制: %v", err)
	}
	// 删除不存在的配额。
	err := c.DeleteQuota("ns", "q")
	t.Logf("输出 err=%v; 依据 删除不存在的配额为参数非法", err)
	if !IsKind(err, ErrInvalidArgument) {
		t.Fatalf("期望参数非法, 得到 %v", err)
	}
	mustSelfCheck(t, c, "ns")
}

// TestUpdateQuotaScopeChange 修改配额作用域后用量按新作用域重算。
func TestUpdateQuotaScopeChange(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.CreatePod("ns", Pod{Name: "be"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "nbe", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{ResourcePods: 10}}); err != nil {
		t.Fatal(err)
	}
	if u := usageOf(t, c, "ns", "q"); u[ResourcePods] != 2 {
		t.Fatalf("空作用域应适用全部: %v", u)
	}
	// 改为只适用尽力型。
	if err := c.UpdateQuota("ns", Quota{
		Name:   "q",
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 10},
	}); err != nil {
		t.Fatal(err)
	}
	u := usageOf(t, c, "ns", "q")
	t.Logf("改作用域后用量=%v; 依据 只统计尽力型 Pod", u)
	if u[ResourcePods] != 1 {
		t.Fatalf("重算不符: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestNamespaceLifecycle 命名空间不存在与重复创建。
func TestNamespaceLifecycle(t *testing.T) {
	c := NewController()
	if err := c.CreateNamespace(""); !IsKind(err, ErrInvalidArgument) {
		t.Fatalf("空名称应为参数非法, 得到 %v", err)
	}
	if err := c.CreateNamespace("ns"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateNamespace("ns"); !IsKind(err, ErrAlreadyExists) {
		t.Fatalf("重复创建命名空间, 得到 %v", err)
	}
	if err := c.SetDefaults("ghost", Defaults{}); !IsKind(err, ErrNamespaceNotFound) {
		t.Fatalf("期望命名空间不存在, 得到 %v", err)
	}
	if err := c.DeleteNamespace("ghost"); !IsKind(err, ErrNamespaceNotFound) {
		t.Fatalf("期望命名空间不存在, 得到 %v", err)
	}
	if err := c.DeleteNamespace("ns"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p"}); !IsKind(err, ErrNamespaceNotFound) {
		t.Fatalf("期望命名空间不存在, 得到 %v", err)
	}
}

// TestSetDefaultsAffectsAdmission 默认值参与补全与记账。
func TestSetDefaultsAffectsAdmission(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.SetDefaults("ns", Defaults{Rules: map[string]DefaultRule{
		"cpu": {Request: i64(1), Limit: i64(2)},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		"requests.cpu": 3, "limits.cpu": 3,
	}}); err != nil {
		t.Fatal(err)
	}
	// 未声明的 Pod 补全为 request=2(取默认上限)、limit=2。
	if err := c.CreatePod("ns", Pod{Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	u := usageOf(t, c, "ns", "q")
	t.Logf("用量=%v; 依据 缺省上限取默认上限 2, 缺省请求取补全后上限 2", u)
	if u["requests.cpu"] != 2 || u["limits.cpu"] != 2 {
		t.Fatalf("补全记账不符: %v", u)
	}
	// 第二个同样补全为 2：2+2>3 拒绝。
	if err := c.CreatePod("ns", Pod{Name: "p2"}); !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	mustSelfCheck(t, c, "ns")
}
