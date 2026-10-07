package quota

import "testing"

func newControllerWithNS(t *testing.T, ns string) *Controller {
	t.Helper()
	c := NewController()
	if err := c.CreateNamespace(ns); err != nil {
		t.Fatalf("创建命名空间失败: %v", err)
	}
	return c
}

func mustSelfCheck(t *testing.T, c *Controller, ns string) {
	t.Helper()
	if err := c.SelfCheck(ns); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func usageOf(t *testing.T, c *Controller, ns, quota string) map[ResourceName]int64 {
	t.Helper()
	u, err := c.Usage(ns, quota)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	return u
}

// TestCreatePodBasicAccounting 创建准入成功后的记账与删除释放。
func TestCreatePodBasicAccounting(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	q := Quota{Name: "q", Hard: map[ResourceName]int64{
		ResourcePods: 2, "requests.cpu": 4, "limits.cpu": 8,
	}}
	if err := c.AddQuota("ns", q); err != nil {
		t.Fatalf("新增配额失败: %v", err)
	}
	p := Pod{Name: "p1", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 2},
		Limits:   map[string]int64{"cpu": 3},
	}}
	if err := c.CreatePod("ns", p); err != nil {
		t.Fatalf("创建 Pod 失败: %v", err)
	}
	u := usageOf(t, c, "ns", "q")
	t.Logf("创建 p1 后用量=%v; 依据 pods+1, requests.cpu+2, limits.cpu+3", u)
	if u[ResourcePods] != 1 || u["requests.cpu"] != 2 || u["limits.cpu"] != 3 {
		t.Fatalf("用量不符: %v", u)
	}
	if err := c.DeletePod("ns", "p1"); err != nil {
		t.Fatalf("删除 Pod 失败: %v", err)
	}
	u = usageOf(t, c, "ns", "q")
	t.Logf("删除 p1 后用量=%v; 依据 删除释放全部用量", u)
	if u[ResourcePods] != 0 || u["requests.cpu"] != 0 || u["limits.cpu"] != 0 {
		t.Fatalf("删除后用量未释放: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestMissingDeclarationBeatsExceeded 缺失声明优先于额度超限。
func TestMissingDeclarationBeatsExceeded(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	// qa: requests.cpu 会超限；qb: limits.mem 缺失声明。
	if err := c.AddQuota("ns", Quota{Name: "qa", Hard: map[ResourceName]int64{"requests.cpu": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{Name: "qb", Hard: map[ResourceName]int64{"limits.mem": 10}}); err != nil {
		t.Fatal(err)
	}
	p := Pod{Name: "p", Spec: PodSpec{Requests: map[string]int64{"cpu": 5}}}
	err := c.CreatePod("ns", p)
	t.Logf("输入 pod=%+v; 输出 err=%v; 依据 缺失声明(qb/limits.mem) 优先于 额度超限(qa/requests.cpu)", p.Spec, err)
	var qe *Error
	if !asError(err, &qe) || qe.Kind != ErrMissingDeclaration || qe.Quota != "qb" || qe.Resource != "limits.mem" {
		t.Fatalf("期望 qb/limits.mem 缺失声明, 得到 %v", err)
	}
	mustSelfCheck(t, c, "ns")
}

// TestSameKindOrdering 同类问题取配额名升序、资源名升序的第一处。
func TestSameKindOrdering(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	// 两个配额都会缺失声明；qb 内两个资源都缺失，应报 limits.cpu（升序第一）。
	for _, q := range []Quota{
		{Name: "qb", Hard: map[ResourceName]int64{"requests.mem": 1, "limits.cpu": 1}},
		{Name: "qa", Hard: map[ResourceName]int64{"requests.disk": 1}},
	} {
		if err := c.AddQuota("ns", q); err != nil {
			t.Fatal(err)
		}
	}
	err := c.CreatePod("ns", Pod{Name: "p", Spec: PodSpec{}})
	t.Logf("输出 err=%v; 依据 配额名升序 qa<qb, qa 下仅 requests.disk", err)
	var qe *Error
	if !asError(err, &qe) || qe.Quota != "qa" || qe.Resource != "requests.disk" {
		t.Fatalf("期望 qa/requests.disk, 得到 %v", err)
	}

	// 去掉 qa 后应报 qb 下资源名升序第一处 limits.cpu。
	if err := c.DeleteQuota("ns", "qa"); err != nil {
		t.Fatal(err)
	}
	err = c.CreatePod("ns", Pod{Name: "p", Spec: PodSpec{}})
	t.Logf("输出 err=%v; 依据 qb 下 limits.cpu < requests.mem", err)
	if !asError(err, &qe) || qe.Quota != "qb" || qe.Resource != "limits.cpu" {
		t.Fatalf("期望 qb/limits.cpu, 得到 %v", err)
	}
	mustSelfCheck(t, c, "ns")
}

// TestAllOrNothingMultiQuota 多配额多资源：任一不通过则不计入任何用量。
func TestAllOrNothingMultiQuota(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q1", Hard: map[ResourceName]int64{
		"requests.cpu": 10, "limits.mem": 10,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{Name: "q2", Hard: map[ResourceName]int64{
		ResourcePods: 1, "requests.mem": 10,
	}}); err != nil {
		t.Fatal(err)
	}
	ok := Pod{Name: "ok", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1, "mem": 1},
		Limits:   map[string]int64{"mem": 1},
	}}
	if err := c.CreatePod("ns", ok); err != nil {
		t.Fatalf("首个 Pod 应准入: %v", err)
	}
	// 第二个 Pod 通过 q1 但超过 q2 的 pods 上限。
	p2 := Pod{Name: "p2", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1, "mem": 1},
		Limits:   map[string]int64{"mem": 1},
	}}
	err := c.CreatePod("ns", p2)
	t.Logf("输出 err=%v; 依据 q2 pods 1+1>1 超限", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	u1 := usageOf(t, c, "ns", "q1")
	u2 := usageOf(t, c, "ns", "q2")
	t.Logf("拒绝后 q1 用量=%v q2 用量=%v; 依据 全有或全无, q1 不得计入 p2", u1, u2)
	if u1["requests.cpu"] != 1 || u1["limits.mem"] != 1 {
		t.Fatalf("q1 被部分计入: %v", u1)
	}
	if u2[ResourcePods] != 1 || u2["requests.mem"] != 1 {
		t.Fatalf("q2 被部分计入: %v", u2)
	}
	mustSelfCheck(t, c, "ns")
}

// TestScopeScopedAccounting 配额只对满足其全部作用域的 Pod 记账与限制。
func TestScopeScopedAccounting(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{
		Name:   "be",
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{
		Name:   "term",
		Scopes: []Scope{ScopeTerminating},
		Hard:   map[ResourceName]int64{ResourcePods: 1, "requests.cpu": 100},
	}); err != nil {
		t.Fatal(err)
	}
	// 尽力型 Pod：只计入 be。
	if err := c.CreatePod("ns", Pod{Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	// 非尽力型无期限 Pod：两个配额都不适用（be 不适用非尽力型，term 不适用无期限）。
	if err := c.CreatePod("ns", Pod{Name: "p2", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 50},
	}}); err != nil {
		t.Fatal(err)
	}
	// 非尽力型有期限 Pod：计入 term。
	if err := c.CreatePod("ns", Pod{Name: "p3", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 60}, DeadlineSeconds: i64(10),
	}}); err != nil {
		t.Fatal(err)
	}
	// 第二个尽力型 Pod：be 的 pods 已达 1，拒绝。
	err := c.CreatePod("ns", Pod{Name: "p4"})
	t.Logf("输出 err=%v; 依据 be 配额 pods 上限 1 已被 p1 占满", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	uBe := usageOf(t, c, "ns", "be")
	uTerm := usageOf(t, c, "ns", "term")
	t.Logf("be 用量=%v term 用量=%v; 依据 按作用域分别记账", uBe, uTerm)
	if uBe[ResourcePods] != 1 {
		t.Fatalf("be 用量不符: %v", uBe)
	}
	if uTerm[ResourcePods] != 1 || uTerm["requests.cpu"] != 60 {
		t.Fatalf("term 用量不符: %v", uTerm)
	}
	mustSelfCheck(t, c, "ns")
}

// TestErrorPriority 错误类别优先级：参数非法 > 命名空间不存在 > 重复创建 > 非法配置。
func TestErrorPriority(t *testing.T) {
	c := NewController()
	// 参数非法优先于命名空间不存在。
	err := c.CreatePod("ghost", Pod{Name: "p", Spec: PodSpec{Requests: map[string]int64{"cpu": -1}}})
	t.Logf("输出 err=%v; 依据 负数量为参数非法, 优先于命名空间不存在", err)
	if !IsKind(err, ErrInvalidArgument) {
		t.Fatalf("期望参数非法, 得到 %v", err)
	}
	// 命名空间不存在。
	err = c.CreatePod("ghost", Pod{Name: "p"})
	if !IsKind(err, ErrNamespaceNotFound) {
		t.Fatalf("期望命名空间不存在, 得到 %v", err)
	}
	// 重复创建优先于准入失败。
	if err := c.CreateNamespace("ns"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{ResourcePods: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p"}); err != nil {
		t.Fatalf("首个 Pod 应准入: %v", err)
	}
	// pods 已达上限时重复创建同名 Pod：报重复创建而非额度超限。
	err = c.CreatePod("ns", Pod{Name: "p"})
	t.Logf("输出 err=%v; 依据 重复创建优先于额度超限", err)
	if !IsKind(err, ErrAlreadyExists) {
		t.Fatalf("期望重复创建, 得到 %v", err)
	}
	// 重复创建优先于非法配置（新增同名且配置非法的配额）。
	err = c.AddQuota("ns", Quota{Name: "q", Scopes: []Scope{ScopeBestEffort, ScopeNotBestEffort}})
	t.Logf("输出 err=%v; 依据 重复创建优先于非法配置", err)
	if !IsKind(err, ErrAlreadyExists) {
		t.Fatalf("期望重复创建, 得到 %v", err)
	}
	// 非法配置：矛盾作用域。
	err = c.AddQuota("ns", Quota{Name: "q2", Scopes: []Scope{ScopeBestEffort, ScopeNotBestEffort}})
	if !IsKind(err, ErrInvalidConfiguration) {
		t.Fatalf("期望非法配置, 得到 %v", err)
	}
	mustSelfCheck(t, c, "ns")
}

// TestRejectedOpsChangeNoState 被拒绝的操作不得改变任何状态。
func TestRejectedOpsChangeNoState(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		ResourcePods: 1, "requests.cpu": 2,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p1", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 2},
	}}); err != nil {
		t.Fatal(err)
	}
	before := usageOf(t, c, "ns", "q")
	// 会被拒绝的创建。
	if err := c.CreatePod("ns", Pod{Name: "p2", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1},
	}}); !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	// 会被拒绝的配额修改（非法配置）。
	if err := c.UpdateQuota("ns", Quota{Name: "q", Scopes: []Scope{ScopeBestEffort, ScopeNotBestEffort}}); !IsKind(err, ErrInvalidConfiguration) {
		t.Fatalf("期望非法配置, 得到 %v", err)
	}
	after := usageOf(t, c, "ns", "q")
	t.Logf("拒绝前后用量 %v -> %v; 依据 被拒绝的操作不得改变任何状态", before, after)
	if before[ResourcePods] != after[ResourcePods] || before["requests.cpu"] != after["requests.cpu"] {
		t.Fatalf("状态被改变: %v -> %v", before, after)
	}
	mustSelfCheck(t, c, "ns")
}

func asError(err error, target **Error) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	*target = e
	return true
}
