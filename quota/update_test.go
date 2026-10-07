package quota

import "testing"

// TestUpdateMixedDelta 调整时的增减混合：变大的核对、变小的释放、不变的不核对。
func TestUpdateMixedDelta(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		"requests.cpu": 10, "limits.mem": 10, "requests.disk": 5,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 4, "disk": 5},
		Limits:   map[string]int64{"mem": 4},
	}}); err != nil {
		t.Fatal(err)
	}
	// cpu 4->8（变大，8<=10 通过），mem 4->1（释放），disk 5->5（不变不核对）。
	if err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 8, "disk": 5},
		Limits:   map[string]int64{"mem": 1},
	}}); err != nil {
		t.Fatalf("增减混合调整应通过: %v", err)
	}
	u := usageOf(t, c, "ns", "q")
	t.Logf("调整后用量=%v; 依据 cpu+4 mem-3 disk 不变", u)
	if u["requests.cpu"] != 8 || u["limits.mem"] != 1 || u["requests.disk"] != 5 {
		t.Fatalf("增量记账不符: %v", u)
	}
	// disk 已达上限 5：disk 不变、cpu 变大的调整不核对 disk。
	if err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 9, "disk": 5},
		Limits:   map[string]int64{"mem": 1},
	}}); err != nil {
		t.Fatalf("不变资源不应核对: %v", err)
	}
	// cpu 9->11 超过硬上限 10：拒绝且状态不变。
	err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 11, "disk": 5},
		Limits:   map[string]int64{"mem": 1},
	}})
	t.Logf("输出 err=%v; 依据 9+2>10 超限", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	u = usageOf(t, c, "ns", "q")
	if u["requests.cpu"] != 9 {
		t.Fatalf("被拒绝的调整不得改变状态: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestUpdateAllOrNothing 调整的全有或全无：一个资源超限则所有增量都不计入。
func TestUpdateAllOrNothing(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		"requests.cpu": 10, "limits.mem": 3,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1},
		Limits:   map[string]int64{"mem": 1},
	}}); err != nil {
		t.Fatal(err)
	}
	// cpu 增量可过，mem 增量超限：整体拒绝，cpu 也不得计入。
	err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 5},
		Limits:   map[string]int64{"mem": 9},
	}})
	t.Logf("输出 err=%v; 依据 mem 1->9 超过硬上限 3", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	u := usageOf(t, c, "ns", "q")
	if u["requests.cpu"] != 1 || u["limits.mem"] != 1 {
		t.Fatalf("被拒绝的调整不得部分计入: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestClassChangeMigration 等级变化导致适用配额迁移：从原配额释放、向新配额重新准入。
func TestClassChangeMigration(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{
		Name:   "be",
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{
		Name:   "nbe",
		Scopes: []Scope{ScopeNotBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 1, "requests.cpu": 10},
	}); err != nil {
		t.Fatal(err)
	}
	// 尽力型 Pod：计入 be。
	if err := c.CreatePod("ns", Pod{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	// 调整声明 cpu：变为非尽力型，应从 be 释放、向 nbe 准入。
	if err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 3},
	}}); err != nil {
		t.Fatalf("等级迁移应通过: %v", err)
	}
	uBe := usageOf(t, c, "ns", "be")
	uNbe := usageOf(t, c, "ns", "nbe")
	t.Logf("迁移后 be=%v nbe=%v; 依据 从 be 释放、向 nbe 重新准入", uBe, uNbe)
	if uBe[ResourcePods] != 0 {
		t.Fatalf("be 应已释放: %v", uBe)
	}
	if uNbe[ResourcePods] != 1 || uNbe["requests.cpu"] != 3 {
		t.Fatalf("nbe 应已计入: %v", uNbe)
	}
	// 再建一个非尽力型 Pod 占满 nbe 的 pods=1。
	if err := c.CreatePod("ns", Pod{Name: "p2", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 1},
	}}); err == nil {
		t.Fatal("nbe pods=1 已满, 应拒绝")
	}
	// 把 p 调回尽力型：nbe 释放、be 重新准入（be 还有空位）。
	if err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{}}); err != nil {
		t.Fatalf("迁回尽力型应通过: %v", err)
	}
	uBe = usageOf(t, c, "ns", "be")
	uNbe = usageOf(t, c, "ns", "nbe")
	t.Logf("迁回后 be=%v nbe=%v", uBe, uNbe)
	if uBe[ResourcePods] != 1 || uNbe[ResourcePods] != 0 {
		t.Fatalf("迁回记账不符: be=%v nbe=%v", uBe, uNbe)
	}
	mustSelfCheck(t, c, "ns")
}

// TestClassChangeMigrationRejected 迁移目标配额不允许时整体拒绝，原记账不变。
func TestClassChangeMigrationRejected(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{
		Name:   "be",
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddQuota("ns", Quota{
		Name:   "nbe",
		Scopes: []Scope{ScopeNotBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 1, "requests.cpu": 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	// 迁移到非尽力型时 requests.cpu=5 超过 nbe 硬上限 2：拒绝，be 记账不变。
	err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{
		Requests: map[string]int64{"cpu": 5},
	}})
	t.Logf("输出 err=%v; 依据 重新准入 nbe 时 0+5>2", err)
	if !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	uBe := usageOf(t, c, "ns", "be")
	uNbe := usageOf(t, c, "ns", "nbe")
	if uBe[ResourcePods] != 1 || uNbe[ResourcePods] != 0 {
		t.Fatalf("被拒绝的迁移不得改变记账: be=%v nbe=%v", uBe, uNbe)
	}
	mustSelfCheck(t, c, "ns")
}

// TestTerminatingMigration 期限属性变化同样触发配额迁移。
func TestTerminatingMigration(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{
		Name:   "term",
		Scopes: []Scope{ScopeTerminating},
		Hard:   map[ResourceName]int64{ResourcePods: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePod("ns", Pod{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	if u := usageOf(t, c, "ns", "term"); u[ResourcePods] != 0 {
		t.Fatalf("无期限 Pod 不应计入 term: %v", u)
	}
	// 声明截止时长：变为有期限，迁入 term。
	if err := c.UpdatePod("ns", Pod{Name: "p", Spec: PodSpec{DeadlineSeconds: i64(30)}}); err != nil {
		t.Fatal(err)
	}
	if u := usageOf(t, c, "ns", "term"); u[ResourcePods] != 1 {
		t.Fatalf("有期限 Pod 应计入 term: %v", u)
	}
	// 另一个有期限 Pod：term pods=1 已满，拒绝。
	if err := c.CreatePod("ns", Pod{Name: "p2", Spec: PodSpec{DeadlineSeconds: i64(5)}}); !IsKind(err, ErrQuotaExceeded) {
		t.Fatalf("期望额度超限, 得到 %v", err)
	}
	mustSelfCheck(t, c, "ns")
}

// TestUpdateNonexistentPod 调整不存在的 Pod 为参数非法。
func TestUpdateNonexistentPod(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	err := c.UpdatePod("ns", Pod{Name: "ghost"})
	t.Logf("输出 err=%v; 依据 引用不存在的 Pod 为参数非法", err)
	if !IsKind(err, ErrInvalidArgument) {
		t.Fatalf("期望参数非法, 得到 %v", err)
	}
	if err := c.DeletePod("ns", "ghost"); !IsKind(err, ErrInvalidArgument) {
		t.Fatalf("期望参数非法, 得到 %v", err)
	}
}
