package quota

import (
	"fmt"
	"testing"
)

func ptr(v int64) *int64 { return &v }

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

func mustKind(t *testing.T, err error, k Kind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误类别 %s，得到成功", k)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("期望 *Error(%s)，得到 %T: %v", k, err, err)
	}
	if e.Kind != k {
		t.Fatalf("期望错误类别 %s，得到 %s: %v", k, e.Kind, err)
	}
	return e
}

// mustConsistent 在每次状态变更后调用自检接口。
func mustConsistent(t *testing.T, c *Controller) {
	t.Helper()
	if err := c.CheckConsistency(); err != nil {
		t.Fatalf("一致性自检失败: %v", err)
	}
}

func mustUsage(t *testing.T, c *Controller, ns, quota string, want map[ResourceName]int64) {
	t.Helper()
	got, err := c.Usage(ns, quota)
	mustOK(t, err)
	if len(got) != len(want) {
		t.Fatalf("配额 %s 用量条目数: 得到 %v，期望 %v", quota, got, want)
	}
	for r, w := range want {
		if got[r] != w {
			t.Fatalf("配额 %s 资源 %s 用量: 得到 %d，期望 %d（全部: %v）", quota, r, got[r], w, got)
		}
	}
}

func newNS(t *testing.T, c *Controller, name string) {
	t.Helper()
	mustOK(t, c.CreateNamespace(name))
}

// TestComplete 覆盖补全的各分支。
func TestComplete(t *testing.T) {
	defs := Defaults{
		"cpu": {Request: ptr(100), Limit: ptr(200)},
		"mem": {Limit: ptr(500)},
		"gpu": {Request: ptr(1)},
	}

	t.Run("已声明的值不被改变", func(t *testing.T) {
		c := complete(PodSpec{
			Requests: map[string]int64{"cpu": 50},
			Limits:   map[string]int64{"mem": 700},
		}, defs)
		if c.requests["cpu"] != 50 {
			t.Fatalf("已声明请求量被改变: %d", c.requests["cpu"])
		}
		if c.limits["mem"] != 700 {
			t.Fatalf("已声明上限量被改变: %d", c.limits["mem"])
		}
		// cpu 上限缺省 -> 默认上限 200
		if c.limits["cpu"] != 200 {
			t.Fatalf("缺省上限量未取默认上限: %d", c.limits["cpu"])
		}
		// mem 请求缺省但已有上限 700 -> 取上限
		if c.requests["mem"] != 700 {
			t.Fatalf("缺省请求量未取已有上限: %d", c.requests["mem"])
		}
	})

	t.Run("请求缺省取补全后的上限", func(t *testing.T) {
		c := complete(PodSpec{}, defs)
		// mem 上限缺省 -> 默认 500；请求缺省 -> 取补全后的上限 500
		if c.limits["mem"] != 500 || c.requests["mem"] != 500 {
			t.Fatalf("mem 补全错误: req=%d lim=%d", c.requests["mem"], c.limits["mem"])
		}
		// cpu: 上限缺省 -> 默认 200；请求缺省且已有上限 -> 取上限 200（默认请求被忽略）
		if c.requests["cpu"] != 200 || c.limits["cpu"] != 200 {
			t.Fatalf("cpu 补全错误: req=%d lim=%d", c.requests["cpu"], c.limits["cpu"])
		}
		// gpu 只有默认请求 -> req 1，上限保持缺省
		if c.requests["gpu"] != 1 {
			t.Fatalf("gpu 请求补全错误: %d", c.requests["gpu"])
		}
		if _, has := c.limits["gpu"]; has {
			t.Fatalf("gpu 上限应保持缺省")
		}
	})

	t.Run("声明上限时请求取声明的上限而非默认请求", func(t *testing.T) {
		c := complete(PodSpec{Limits: map[string]int64{"cpu": 300}}, defs)
		if c.requests["cpu"] != 300 || c.limits["cpu"] != 300 {
			t.Fatalf("cpu 补全错误: req=%d lim=%d", c.requests["cpu"], c.limits["cpu"])
		}
	})

	t.Run("无声明无默认则保持缺省", func(t *testing.T) {
		c := complete(PodSpec{Requests: map[string]int64{"disk": 9}}, Defaults{})
		if _, has := c.limits["disk"]; has {
			t.Fatalf("disk 上限应保持缺省")
		}
		if _, has := c.requests["other"]; has {
			t.Fatalf("other 应保持缺省")
		}
	})
}

// TestBestEffort 覆盖服务等级的判定。
func TestBestEffort(t *testing.T) {
	cases := []struct {
		name string
		spec PodSpec
		defs Defaults
		want bool
	}{
		{"全缺省", PodSpec{}, nil, true},
		{"显式为零", PodSpec{
			Requests: map[string]int64{"cpu": 0},
			Limits:   map[string]int64{"cpu": 0},
		}, nil, true},
		{"非零请求", PodSpec{Requests: map[string]int64{"cpu": 1}}, nil, false},
		{"非零上限", PodSpec{Limits: map[string]int64{"cpu": 1}}, nil, false},
		{"默认值使空Pod变为非尽力型", PodSpec{}, Defaults{"cpu": {Request: ptr(1)}}, false},
		{"零值默认仍为尽力型", PodSpec{}, Defaults{"cpu": {Request: ptr(0), Limit: ptr(0)}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := complete(tc.spec, tc.defs).bestEffort()
			if got != tc.want {
				t.Fatalf("bestEffort=%v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestAttributes 覆盖期限属性判定。
func TestAttributes(t *testing.T) {
	if a := attributesOf(complete(PodSpec{}, nil), PodSpec{}); a.terminating {
		t.Fatalf("未声明截止时长应为无期限")
	}
	if a := attributesOf(complete(PodSpec{}, nil), PodSpec{ActiveDeadlineSeconds: ptr(0)}); a.terminating {
		t.Fatalf("截止时长为零应为无期限")
	}
	if a := attributesOf(complete(PodSpec{}, nil), PodSpec{ActiveDeadlineSeconds: ptr(30)}); !a.terminating {
		t.Fatalf("截止时长为正应为有期限")
	}
}

// TestScopeMatches 覆盖四种作用域的组合匹配。
func TestScopeMatches(t *testing.T) {
	be := attributes{bestEffort: true}
	nbe := attributes{bestEffort: false}
	term := attributes{terminating: true}
	nbeTerm := attributes{bestEffort: false, terminating: true}

	cases := []struct {
		scopes []Scope
		attr   attributes
		want   bool
	}{
		{nil, be, true}, // 空作用域适用全部
		{nil, nbeTerm, true},
		{[]Scope{ScopeBestEffort}, be, true},
		{[]Scope{ScopeBestEffort}, nbe, false},
		{[]Scope{ScopeNotBestEffort}, nbe, true},
		{[]Scope{ScopeNotBestEffort}, be, false},
		{[]Scope{ScopeTerminating}, term, true},
		{[]Scope{ScopeTerminating}, be, false},
		{[]Scope{ScopeNotTerminating}, be, true},
		{[]Scope{ScopeNotTerminating}, term, false},
		{[]Scope{ScopeBestEffort, ScopeTerminating}, attributes{true, true}, true},
		{[]Scope{ScopeBestEffort, ScopeTerminating}, attributes{true, false}, false},
		{[]Scope{ScopeNotBestEffort, ScopeNotTerminating}, nbe, true},
		{[]Scope{ScopeNotBestEffort, ScopeNotTerminating}, nbeTerm, false},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			if got := newScopeSet(tc.scopes).matches(tc.attr); got != tc.want {
				t.Fatalf("scopes=%v attr=%+v: 得到 %v，期望 %v", tc.scopes, tc.attr, got, tc.want)
			}
		})
	}
}

// TestQuotaValidation 覆盖非法作用域配置与参数校验。
func TestQuotaValidation(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")

	t.Run("矛盾的服务等级作用域", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Scopes: []Scope{ScopeBestEffort, ScopeNotBestEffort},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}), KindInvalidConfiguration)
	})
	t.Run("矛盾的期限作用域", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Scopes: []Scope{ScopeTerminating, ScopeNotTerminating},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}), KindInvalidConfiguration)
	})
	t.Run("尽力型配额限制pods以外资源", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Scopes: []Scope{ScopeBestEffort},
			Hard:   map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 1},
		}), KindInvalidConfiguration)
	})
	t.Run("尽力型配额仅限制pods合法", func(t *testing.T) {
		mustOK(t, c.CreateQuota("ns", "be", QuotaSpec{
			Scopes: []Scope{ScopeBestEffort},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}))
	})
	t.Run("未知作用域字面量", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Scopes: []Scope{"Sometimes"},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}), KindInvalidArgument)
	})
	t.Run("硬上限资源名形式非法", func(t *testing.T) {
		for _, bad := range []ResourceName{"cpu", "requests.", "limits.", "Pods", "requests.cpu.extra"} {
			err := c.CreateQuota("ns", "q", QuotaSpec{Hard: map[ResourceName]int64{bad: 1}})
			if bad == "requests.cpu.extra" {
				// "requests.cpu.extra" 的基础资源名为 "cpu.extra"，形式合法
				mustOK(t, err)
				mustOK(t, c.DeleteQuota("ns", "q"))
				continue
			}
			mustKind(t, err, KindInvalidArgument)
		}
	})
	t.Run("硬上限为负", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Hard: map[ResourceName]int64{ResourcePods: -1},
		}), KindInvalidArgument)
	})
	t.Run("修改配额同样校验配置", func(t *testing.T) {
		mustOK(t, c.CreateQuota("ns", "m", QuotaSpec{Hard: map[ResourceName]int64{ResourcePods: 1}}))
		mustKind(t, c.UpdateQuota("ns", "m", QuotaSpec{
			Scopes: []Scope{ScopeTerminating, ScopeNotTerminating},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}), KindInvalidConfiguration)
	})
	mustConsistent(t, c)
}

// TestCreateAdmission 覆盖创建准入：缺失声明与超限的先后、
// 多处同类问题的报告顺序、多配额多资源的全有或全无。
func TestCreateAdmission(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.SetDefaults("ns", Defaults{"cpu": {Request: ptr(100), Limit: ptr(100)}}))
	mustOK(t, c.CreateQuota("ns", "qa", QuotaSpec{
		Hard: map[ResourceName]int64{ResourcePods: 5, RequestsFor("cpu"): 200},
	}))
	mustOK(t, c.CreateQuota("ns", "qb", QuotaSpec{
		Scopes: []Scope{ScopeNotBestEffort},
		Hard:   map[ResourceName]int64{LimitsFor("mem"): 100},
	}))

	// p1: cpu 请求 100（上限补全 100），mem 上限 50 -> 两个配额都记账
	mustOK(t, c.CreatePod("ns", "p1", PodSpec{
		Requests: map[string]int64{"cpu": 100},
		Limits:   map[string]int64{"mem": 50},
	}))
	mustUsage(t, c, "ns", "qa", map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 100})
	mustUsage(t, c, "ns", "qb", map[ResourceName]int64{LimitsFor("mem"): 50})

	t.Run("超限拒绝且不计入任何用量", func(t *testing.T) {
		// cpu 150 使 qa 的 requests.cpu 达到 250 > 200
		e := mustKind(t, c.CreatePod("ns", "p2", PodSpec{
			Requests: map[string]int64{"cpu": 150},
			Limits:   map[string]int64{"mem": 10},
		}), KindQuotaExceeded)
		if e.Quota != "qa" || e.Resource != RequestsFor("cpu") {
			t.Fatalf("报告位置错误: %v", e)
		}
		// 全有或全无：qb 也不计入 p2 的 mem
		mustUsage(t, c, "ns", "qa", map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 100})
		mustUsage(t, c, "ns", "qb", map[ResourceName]int64{LimitsFor("mem"): 50})
		mustConsistent(t, c)
	})

	t.Run("作用域不匹配的配额不记账", func(t *testing.T) {
		// p2 声明 mem 上限但 cpu 显式为零 -> 补全后仍全零 -> 尽力型，不进 qb
		mustOK(t, c.CreatePod("ns", "p2", PodSpec{
			Requests: map[string]int64{"cpu": 0},
			Limits:   map[string]int64{"cpu": 0, "mem": 0},
		}))
		mustUsage(t, c, "ns", "qa", map[ResourceName]int64{ResourcePods: 2, RequestsFor("cpu"): 100})
		mustUsage(t, c, "ns", "qb", map[ResourceName]int64{LimitsFor("mem"): 50})
		mustConsistent(t, c)
	})

	t.Run("缺失声明优先于额度超限", func(t *testing.T) {
		// qc 限制 requests.disk；p3 既缺 disk 声明又使 qa 的 cpu 超限
		mustOK(t, c.CreateQuota("ns", "qc", QuotaSpec{
			Hard: map[ResourceName]int64{RequestsFor("disk"): 10},
		}))
		e := mustKind(t, c.CreatePod("ns", "p3", PodSpec{
			Requests: map[string]int64{"cpu": 150},
			Limits:   map[string]int64{"mem": 10},
		}), KindMissingDeclaration)
		if e.Quota != "qc" || e.Resource != RequestsFor("disk") {
			t.Fatalf("报告位置错误: %v", e)
		}
		mustConsistent(t, c)
	})

	t.Run("多处超限按配额名升序报第一处", func(t *testing.T) {
		// p3 使 qa.cpu 超限（100+150>200），也使 qb.mem 超限（50+60>100）
		e := mustKind(t, c.CreatePod("ns", "p3", PodSpec{
			Requests: map[string]int64{"cpu": 150},
			Limits:   map[string]int64{"mem": 60, "disk": 1},
		}), KindQuotaExceeded)
		if e.Quota != "qa" || e.Resource != RequestsFor("cpu") {
			t.Fatalf("报告位置错误: %v", e)
		}
		mustConsistent(t, c)
	})

	t.Run("同配额多资源超限按资源名升序报第一处", func(t *testing.T) {
		mustOK(t, c.CreateQuota("ns", "qz", QuotaSpec{
			Hard: map[ResourceName]int64{LimitsFor("mem"): 1, LimitsFor("disk"): 1},
		}))
		e := mustKind(t, c.CreatePod("ns", "p4", PodSpec{
			Limits: map[string]int64{"mem": 5, "disk": 5},
		}), KindQuotaExceeded)
		// limits.disk < limits.mem，先报 disk
		if e.Quota != "qz" || e.Resource != LimitsFor("disk") {
			t.Fatalf("报告位置错误: %v", e)
		}
		mustOK(t, c.DeleteQuota("ns", "qz"))
		mustConsistent(t, c)
	})

	t.Run("通过全部核对才接纳并同时计入", func(t *testing.T) {
		mustOK(t, c.CreatePod("ns", "p5", PodSpec{
			Requests: map[string]int64{"cpu": 100},
			Limits:   map[string]int64{"mem": 50, "disk": 1},
		}))
		mustUsage(t, c, "ns", "qa", map[ResourceName]int64{ResourcePods: 3, RequestsFor("cpu"): 200})
		mustUsage(t, c, "ns", "qb", map[ResourceName]int64{LimitsFor("mem"): 100})
		mustUsage(t, c, "ns", "qc", map[ResourceName]int64{RequestsFor("disk"): 1})
		mustConsistent(t, c)
	})
	mustConsistent(t, c)
}

// TestUpdatePod 覆盖原位调整：增减混合的增量核算、属性变化导致的
// 适用配额迁移、调整的全有或全无。
func TestUpdatePod(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{RequestsFor("cpu"): 200, LimitsFor("mem"): 100},
	}))
	mustOK(t, c.CreatePod("ns", "p", PodSpec{
		Requests: map[string]int64{"cpu": 100},
		Limits:   map[string]int64{"mem": 50},
	}))

	t.Run("增减混合一次生效", func(t *testing.T) {
		// cpu +50（变大需核对），mem -20（变小释放）
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 150},
			Limits:   map[string]int64{"mem": 30},
		}))
		mustUsage(t, c, "ns", "q", map[ResourceName]int64{RequestsFor("cpu"): 150, LimitsFor("mem"): 30})
		mustConsistent(t, c)
	})

	t.Run("变大超限则整体拒绝", func(t *testing.T) {
		e := mustKind(t, c.UpdatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 250},
			Limits:   map[string]int64{"mem": 10},
		}), KindQuotaExceeded)
		if e.Resource != RequestsFor("cpu") {
			t.Fatalf("报告位置错误: %v", e)
		}
		// 全有或全无：mem 的释放也不生效
		mustUsage(t, c, "ns", "q", map[ResourceName]int64{RequestsFor("cpu"): 150, LimitsFor("mem"): 30})
		mustConsistent(t, c)
	})
	mustConsistent(t, c)
}

// TestUpdateAttrMigration 覆盖等级 / 期限属性变化导致的适用配额迁移。
func TestUpdateAttrMigration(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "qbe", QuotaSpec{
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 2},
	}))
	mustOK(t, c.CreateQuota("ns", "qnbe", QuotaSpec{
		Scopes: []Scope{ScopeNotBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 2, RequestsFor("cpu"): 100},
	}))
	mustOK(t, c.CreateQuota("ns", "qterm", QuotaSpec{
		Scopes: []Scope{ScopeTerminating},
		Hard:   map[ResourceName]int64{ResourcePods: 1},
	}))

	// p 初始为尽力型、无期限：只进 qbe
	mustOK(t, c.CreatePod("ns", "p", PodSpec{}))
	mustUsage(t, c, "ns", "qbe", map[ResourceName]int64{ResourcePods: 1})
	mustUsage(t, c, "ns", "qnbe", map[ResourceName]int64{ResourcePods: 0, RequestsFor("cpu"): 0})

	t.Run("等级变化迁移配额", func(t *testing.T) {
		// 声明 cpu 请求 -> 非尽力型：从 qbe 释放，向 qnbe 重新准入
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{Requests: map[string]int64{"cpu": 10}}))
		mustUsage(t, c, "ns", "qbe", map[ResourceName]int64{ResourcePods: 0})
		mustUsage(t, c, "ns", "qnbe", map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 10})
		mustConsistent(t, c)
	})

	t.Run("期限变化迁移配额", func(t *testing.T) {
		// 加截止时长 -> 有期限：进入 qterm
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{
			Requests:              map[string]int64{"cpu": 10},
			ActiveDeadlineSeconds: ptr(30),
		}))
		mustUsage(t, c, "ns", "qterm", map[ResourceName]int64{ResourcePods: 1})
		mustConsistent(t, c)
	})

	t.Run("迁移目标超限则整体拒绝", func(t *testing.T) {
		// p2 已占满 qterm 的 pods=1；p 仍是有期限，先确认占用
		// 把 p 改回无期限，再让 p2 成为有期限占满 qterm
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{Requests: map[string]int64{"cpu": 10}}))
		mustOK(t, c.CreatePod("ns", "p2", PodSpec{ActiveDeadlineSeconds: ptr(5)}))
		mustUsage(t, c, "ns", "qterm", map[ResourceName]int64{ResourcePods: 1})
		// p 重新加回截止时长 -> 需重新准入 qterm，但 pods 已满
		e := mustKind(t, c.UpdatePod("ns", "p", PodSpec{
			Requests:              map[string]int64{"cpu": 10},
			ActiveDeadlineSeconds: ptr(30),
		}), KindQuotaExceeded)
		if e.Quota != "qterm" || e.Resource != ResourcePods {
			t.Fatalf("报告位置错误: %v", e)
		}
		// 全有或全无：p 仍在 qnbe 中，qterm 不变
		mustUsage(t, c, "ns", "qnbe", map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 10})
		mustUsage(t, c, "ns", "qterm", map[ResourceName]int64{ResourcePods: 1})
		mustConsistent(t, c)
	})
	mustConsistent(t, c)
}

// TestQuotaLowered 覆盖配额上限下调后的行为：
// 允许下调到低于当前用量；既有 Pod 不被驱逐；
// 之后增加该资源用量的准入被拒绝，不增加的准入不受影响。
func TestQuotaLowered(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{RequestsFor("cpu"): 200, LimitsFor("mem"): 100},
	}))
	mustOK(t, c.CreatePod("ns", "p", PodSpec{
		Requests: map[string]int64{"cpu": 150},
		Limits:   map[string]int64{"mem": 50},
	}))

	// 下调 requests.cpu 到 100，低于当前用量 150：允许
	mustOK(t, c.UpdateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{RequestsFor("cpu"): 100, LimitsFor("mem"): 100},
	}))
	mustUsage(t, c, "ns", "q", map[ResourceName]int64{RequestsFor("cpu"): 150, LimitsFor("mem"): 50})

	t.Run("新建使cpu增加被拒", func(t *testing.T) {
		mustKind(t, c.CreatePod("ns", "p2", PodSpec{
			Requests: map[string]int64{"cpu": 1},
			Limits:   map[string]int64{"mem": 1},
		}), KindQuotaExceeded)
		mustConsistent(t, c)
	})

	t.Run("不增加cpu的调整不受影响", func(t *testing.T) {
		// mem +30 未超限，cpu 不变（150 虽超上限但未变大）
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 150},
			Limits:   map[string]int64{"mem": 80},
		}))
		mustUsage(t, c, "ns", "q", map[ResourceName]int64{RequestsFor("cpu"): 150, LimitsFor("mem"): 80})
		// cpu 变小：释放用量，允许
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 90},
			Limits:   map[string]int64{"mem": 80},
		}))
		mustUsage(t, c, "ns", "q", map[ResourceName]int64{RequestsFor("cpu"): 90, LimitsFor("mem"): 80})
		mustConsistent(t, c)
	})

	t.Run("下调后cpu再变大仍被拒", func(t *testing.T) {
		// 当前用量 90、上限 100：变大到 101 使 90+11>100
		mustKind(t, c.UpdatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 101},
			Limits:   map[string]int64{"mem": 80},
		}), KindQuotaExceeded)
		// 变大到 100 恰好不超上限：允许
		mustOK(t, c.UpdatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 100},
			Limits:   map[string]int64{"mem": 80},
		}))
		mustConsistent(t, c)
	})
	mustConsistent(t, c)
}

// TestDeletePod 覆盖删除释放全部用量。
func TestDeletePod(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{ResourcePods: 5, RequestsFor("cpu"): 100},
	}))
	mustOK(t, c.CreatePod("ns", "p", PodSpec{Requests: map[string]int64{"cpu": 40}}))
	mustUsage(t, c, "ns", "q", map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 40})
	mustOK(t, c.DeletePod("ns", "p"))
	mustUsage(t, c, "ns", "q", map[ResourceName]int64{ResourcePods: 0, RequestsFor("cpu"): 0})
	// 删除不存在的 Pod
	mustKind(t, c.DeletePod("ns", "p"), KindInvalidArgument)
	mustConsistent(t, c)
}

// TestQuotaCRUD 覆盖配额新增（按既有 Pod 重算用量）、修改与删除。
func TestQuotaCRUD(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.SetDefaults("ns", Defaults{"cpu": {Request: ptr(10)}}))
	mustOK(t, c.CreatePod("ns", "p1", PodSpec{Requests: map[string]int64{"cpu": 5}}))
	mustOK(t, c.CreatePod("ns", "p2", PodSpec{})) // 补全后 cpu 请求 10

	t.Run("新增配额按既有Pod重算", func(t *testing.T) {
		mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
			Hard: map[ResourceName]int64{ResourcePods: 10, RequestsFor("cpu"): 100},
		}))
		mustUsage(t, c, "ns", "q", map[ResourceName]int64{ResourcePods: 2, RequestsFor("cpu"): 15})
		mustConsistent(t, c)
	})

	t.Run("重复创建配额", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Hard: map[ResourceName]int64{ResourcePods: 1},
		}), KindAlreadyExists)
	})

	t.Run("修改作用域后重算", func(t *testing.T) {
		// 只统计尽力型 -> 两个 Pod 都是非尽力型，用量归零
		mustOK(t, c.UpdateQuota("ns", "q", QuotaSpec{
			Scopes: []Scope{ScopeBestEffort},
			Hard:   map[ResourceName]int64{ResourcePods: 10},
		}))
		mustUsage(t, c, "ns", "q", map[ResourceName]int64{ResourcePods: 0})
		mustConsistent(t, c)
	})

	t.Run("删除配额", func(t *testing.T) {
		mustOK(t, c.DeleteQuota("ns", "q"))
		_, err := c.Usage("ns", "q")
		mustKind(t, err, KindInvalidArgument)
		mustKind(t, c.DeleteQuota("ns", "q"), KindInvalidArgument)
		mustConsistent(t, c)
	})
	mustConsistent(t, c)
}

// TestSetDefaults 覆盖默认值规则变更后的重算：
// 既有 Pod 的补全结果与属性可能变化，适用配额随之迁移。
func TestSetDefaults(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "qbe", QuotaSpec{
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 5},
	}))
	mustOK(t, c.CreateQuota("ns", "qnbe", QuotaSpec{
		Scopes: []Scope{ScopeNotBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 5, RequestsFor("cpu"): 100},
	}))
	mustOK(t, c.CreatePod("ns", "p", PodSpec{}))
	mustUsage(t, c, "ns", "qbe", map[ResourceName]int64{ResourcePods: 1})
	mustUsage(t, c, "ns", "qnbe", map[ResourceName]int64{ResourcePods: 0, RequestsFor("cpu"): 0})

	// 设置默认值后，p 补全出 cpu 请求 -> 变为非尽力型，迁移到 qnbe
	mustOK(t, c.SetDefaults("ns", Defaults{"cpu": {Request: ptr(7)}}))
	mustUsage(t, c, "ns", "qbe", map[ResourceName]int64{ResourcePods: 0})
	mustUsage(t, c, "ns", "qnbe", map[ResourceName]int64{ResourcePods: 1, RequestsFor("cpu"): 7})
	mustConsistent(t, c)

	// 清除默认值，p 回到尽力型
	mustOK(t, c.SetDefaults("ns", Defaults{}))
	mustUsage(t, c, "ns", "qbe", map[ResourceName]int64{ResourcePods: 1})
	mustUsage(t, c, "ns", "qnbe", map[ResourceName]int64{ResourcePods: 0, RequestsFor("cpu"): 0})
	mustConsistent(t, c)
}

// TestErrorPriority 覆盖错误类别的优先级：
// 参数非法 > 命名空间不存在 > 重复创建 > 非法配置 > 缺失声明 > 额度超限。
func TestErrorPriority(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{RequestsFor("cpu"): 10},
	}))
	mustOK(t, c.CreatePod("ns", "dup", PodSpec{Requests: map[string]int64{"cpu": 1}}))

	t.Run("参数非法优先于命名空间不存在", func(t *testing.T) {
		mustKind(t, c.CreatePod("no-such", "p", PodSpec{
			Requests: map[string]int64{"cpu": -1},
		}), KindInvalidArgument)
	})
	t.Run("命名空间不存在优先于重复创建", func(t *testing.T) {
		mustKind(t, c.CreatePod("no-such", "dup", PodSpec{}), KindNamespaceNotFound)
	})
	t.Run("重复创建优先于准入失败", func(t *testing.T) {
		// dup 重名；即使新 spec 会超限，也报重复创建
		mustKind(t, c.CreatePod("ns", "dup", PodSpec{
			Requests: map[string]int64{"cpu": 100},
		}), KindAlreadyExists)
	})
	t.Run("重复创建优先于非法配置", func(t *testing.T) {
		mustKind(t, c.CreateQuota("ns", "q", QuotaSpec{
			Scopes: []Scope{ScopeBestEffort, ScopeNotBestEffort},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}), KindAlreadyExists)
	})
	t.Run("非法配置优先于缺失声明与超限", func(t *testing.T) {
		// 在配额写入路径上验证：矛盾作用域报非法配置
		mustKind(t, c.CreateQuota("ns", "q2", QuotaSpec{
			Scopes: []Scope{ScopeTerminating, ScopeNotTerminating},
			Hard:   map[ResourceName]int64{ResourcePods: 1},
		}), KindInvalidConfiguration)
	})
	t.Run("缺失声明优先于额度超限", func(t *testing.T) {
		mustOK(t, c.CreateQuota("ns", "q3", QuotaSpec{
			Hard: map[ResourceName]int64{RequestsFor("disk"): 1},
		}))
		// 既缺 disk 声明，cpu 又超限
		mustKind(t, c.CreatePod("ns", "p", PodSpec{
			Requests: map[string]int64{"cpu": 100},
		}), KindMissingDeclaration)
	})
	mustConsistent(t, c)
}

// TestNamespaceErrors 覆盖命名空间层面的错误。
func TestNamespaceErrors(t *testing.T) {
	c := NewController()
	mustKind(t, c.CreateNamespace(""), KindInvalidArgument)
	mustKind(t, c.DeleteNamespace("x"), KindNamespaceNotFound)
	mustKind(t, c.SetDefaults("x", Defaults{}), KindNamespaceNotFound)
	mustOK(t, c.CreateNamespace("x"))
	mustKind(t, c.CreateNamespace("x"), KindAlreadyExists)
	mustKind(t, c.SetDefaults("x", Defaults{"cpu": {Request: ptr(-1)}}), KindInvalidArgument)
	mustOK(t, c.DeleteNamespace("x"))
	mustKind(t, c.CreatePod("x", "p", PodSpec{}), KindNamespaceNotFound)
	mustConsistent(t, c)
}
