package quota

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// op 是一次随机操作：同时应用到被测控制器与朴素模型，逐步对照。
type op struct {
	desc  string
	apply func(c *Controller) error
	model func(m *model) *Error
}

var (
	opNSPool    = []string{"ns-a", "ns-b"}
	opPodPool   = []string{"p0", "p1", "p2", "p3", "p4"}
	opQuotaPool = []string{"q0", "q1", "q2"}
	opResPool   = []string{"cpu", "mem", "disk"}
)

func randPodSpec(rng *rand.Rand) PodSpec {
	spec := PodSpec{}
	if rng.Intn(4) > 0 {
		spec.Requests = map[string]int64{}
		for _, res := range opResPool {
			if rng.Intn(2) == 0 {
				spec.Requests[res] = int64(rng.Intn(6))
			}
		}
	}
	if rng.Intn(4) > 0 {
		spec.Limits = map[string]int64{}
		for _, res := range opResPool {
			if rng.Intn(2) == 0 {
				spec.Limits[res] = int64(rng.Intn(6))
			}
		}
	}
	if rng.Intn(4) == 0 {
		spec.ActiveDeadlineSeconds = ptr(int64(rng.Intn(3)))
	}
	return spec
}

func randQuotaSpec(rng *rand.Rand) QuotaSpec {
	spec := QuotaSpec{Hard: map[ResourceName]int64{}}
	allScopes := []Scope{ScopeBestEffort, ScopeNotBestEffort, ScopeTerminating, ScopeNotTerminating}
	for _, sc := range allScopes {
		if rng.Intn(4) == 0 {
			spec.Scopes = append(spec.Scopes, sc)
		}
	}
	if rng.Intn(2) == 0 {
		spec.Hard[ResourcePods] = int64(rng.Intn(4))
	}
	for _, res := range opResPool {
		if rng.Intn(3) == 0 {
			spec.Hard[RequestsFor(res)] = int64(rng.Intn(8))
		}
		if rng.Intn(3) == 0 {
			spec.Hard[LimitsFor(res)] = int64(rng.Intn(8))
		}
	}
	if len(spec.Hard) == 0 {
		spec.Hard[ResourcePods] = int64(rng.Intn(4))
	}
	return spec
}

func randDefaults(rng *rand.Rand) Defaults {
	d := Defaults{}
	for _, res := range opResPool {
		if rng.Intn(3) != 0 {
			continue
		}
		rd := ResourceDefault{}
		if rng.Intn(2) == 0 {
			rd.Request = ptr(int64(rng.Intn(4)))
		}
		if rng.Intn(2) == 0 {
			rd.Limit = ptr(int64(rng.Intn(4)))
		}
		d[res] = rd
	}
	return d
}

func genOp(rng *rand.Rand) op {
	ns := opNSPool[rng.Intn(len(opNSPool))]
	pod := opPodPool[rng.Intn(len(opPodPool))]
	quota := opQuotaPool[rng.Intn(len(opQuotaPool))]

	switch rng.Intn(10) {
	case 0:
		return op{
			desc:  fmt.Sprintf("CreateNamespace(%s)", ns),
			apply: func(c *Controller) error { return c.CreateNamespace(ns) },
			model: func(m *model) *Error { return m.createNamespace(ns) },
		}
	case 1:
		d := randDefaults(rng)
		return op{
			desc:  fmt.Sprintf("SetDefaults(%s, %s)", ns, formatDefaults(d)),
			apply: func(c *Controller) error { return c.SetDefaults(ns, d) },
			model: func(m *model) *Error { return m.setDefaults(ns, d) },
		}
	case 2, 3, 4:
		spec := randPodSpec(rng)
		return op{
			desc:  fmt.Sprintf("CreatePod(%s, %s, %s)", ns, pod, formatPod(spec)),
			apply: func(c *Controller) error { return c.CreatePod(ns, pod, spec) },
			model: func(m *model) *Error { return m.createPod(ns, pod, spec) },
		}
	case 5, 6:
		spec := randPodSpec(rng)
		return op{
			desc:  fmt.Sprintf("UpdatePod(%s, %s, %s)", ns, pod, formatPod(spec)),
			apply: func(c *Controller) error { return c.UpdatePod(ns, pod, spec) },
			model: func(m *model) *Error { return m.updatePod(ns, pod, spec) },
		}
	case 7:
		return op{
			desc:  fmt.Sprintf("DeletePod(%s, %s)", ns, pod),
			apply: func(c *Controller) error { return c.DeletePod(ns, pod) },
			model: func(m *model) *Error { return m.deletePod(ns, pod) },
		}
	case 8:
		spec := randQuotaSpec(rng)
		if rng.Intn(2) == 0 {
			return op{
				desc:  fmt.Sprintf("CreateQuota(%s, %s, %s)", ns, quota, formatQuota(spec)),
				apply: func(c *Controller) error { return c.CreateQuota(ns, quota, spec) },
				model: func(m *model) *Error { return m.createQuota(ns, quota, spec) },
			}
		}
		return op{
			desc:  fmt.Sprintf("UpdateQuota(%s, %s, %s)", ns, quota, formatQuota(spec)),
			apply: func(c *Controller) error { return c.UpdateQuota(ns, quota, spec) },
			model: func(m *model) *Error { return m.updateQuota(ns, quota, spec) },
		}
	default:
		return op{
			desc:  fmt.Sprintf("DeleteQuota(%s, %s)", ns, quota),
			apply: func(c *Controller) error { return c.DeleteQuota(ns, quota) },
			model: func(m *model) *Error { return m.deleteQuota(ns, quota) },
		}
	}
}

func formatPod(p PodSpec) string {
	var sb strings.Builder
	sb.WriteString("req={")
	for k, v := range p.Requests {
		fmt.Fprintf(&sb, "%s:%d ", k, v)
	}
	sb.WriteString("} lim={")
	for k, v := range p.Limits {
		fmt.Fprintf(&sb, "%s:%d ", k, v)
	}
	sb.WriteString("}")
	if p.ActiveDeadlineSeconds != nil {
		fmt.Fprintf(&sb, " deadline=%d", *p.ActiveDeadlineSeconds)
	}
	return sb.String()
}

func formatQuota(q QuotaSpec) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "scopes=%v hard={", q.Scopes)
	for r, v := range q.Hard {
		fmt.Fprintf(&sb, "%s:%d ", r, v)
	}
	sb.WriteString("}")
	return sb.String()
}

func formatDefaults(d Defaults) string {
	var sb strings.Builder
	sb.WriteString("{")
	for k, rd := range d {
		fmt.Fprintf(&sb, "%s:(", k)
		if rd.Request != nil {
			fmt.Fprintf(&sb, "req=%d", *rd.Request)
		}
		if rd.Limit != nil {
			fmt.Fprintf(&sb, " lim=%d", *rd.Limit)
		}
		sb.WriteString(") ")
	}
	sb.WriteString("}")
	return sb.String()
}

func errBrief(err error) string {
	if err == nil {
		return "OK"
	}
	if e, ok := err.(*Error); ok {
		if e == nil {
			return "OK"
		}
		if e.Quota != "" {
			return fmt.Sprintf("%s(quota=%s resource=%s)", e.Kind, e.Quota, e.Resource)
		}
		return e.Kind.String()
	}
	return err.Error()
}

// verdict 给出判定依据的可读描述，用于测试日志。
func verdict(err error) string {
	if err == nil {
		return "成功：操作生效（准入场景下表示全部适用配额的全部受限资源核对通过，已计入用量）"
	}
	e, ok := err.(*Error)
	if !ok || e == nil {
		return "拒绝：非预期错误类型"
	}
	switch e.Kind {
	case KindMissingDeclaration:
		return fmt.Sprintf("拒绝：配额 %s 限制的 %s 在 Pod 补全后仍缺省（缺失声明优先）", e.Quota, e.Resource)
	case KindQuotaExceeded:
		return fmt.Sprintf("拒绝：配额 %s 资源 %s 已用量+本次量超过硬上限", e.Quota, e.Resource)
	default:
		return fmt.Sprintf("拒绝：%s（%s）", e.Kind, e.Message)
	}
}

// TestModelDifferential 在大量随机操作序列上，将被测控制器与
// 独立朴素模型逐步对照：每次操作的错误类别与定位、每个配额的
// 用量都必须一致；并周期性执行一致性自检。
// 日志打印每次操作的输入、实际输出与判定依据（go test -v 可见）。
func TestModelDifferential(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			c := NewController()
			m := newModel()

			const steps = 300
			for step := 0; step < steps; step++ {
				o := genOp(rng)
				gotErr := o.apply(c)
				wantErr := o.model(m)

				t.Logf("step %d: %s\n  实际输出: %s\n  模型输出: %s\n  判定依据: %s",
					step, o.desc, errBrief(gotErr), errBrief(wantErr), verdict(gotErr))

				if errMismatch(gotErr, wantErr) {
					t.Fatalf("step %d %s: 控制器=%s，模型=%s",
						step, o.desc, errBrief(gotErr), errBrief(wantErr))
				}
				compareAllUsage(t, c, m, step)
				if step%50 == 0 {
					if err := c.CheckConsistency(); err != nil {
						t.Fatalf("step %d 一致性自检失败: %v", step, err)
					}
				}
			}
			if err := c.CheckConsistency(); err != nil {
				t.Fatalf("最终一致性自检失败: %v", err)
			}
		})
	}
}

// errMismatch 比较两次错误是否等价：类别相同，准入错误的定位相同。
func errMismatch(got, want error) bool {
	got = unwrapNil(got)
	want = unwrapNil(want)
	if got == nil || want == nil {
		return got != want
	}
	ge, gok := got.(*Error)
	we, wok := want.(*Error)
	if !gok || !wok {
		return true
	}
	if ge.Kind != we.Kind {
		return true
	}
	if ge.Kind == KindMissingDeclaration || ge.Kind == KindQuotaExceeded {
		return ge.Quota != we.Quota || ge.Resource != we.Resource
	}
	return false
}

// unwrapNil 把类型化 nil 的 *Error 还原为 nil 接口。
func unwrapNil(err error) error {
	if e, ok := err.(*Error); ok && e == nil {
		return nil
	}
	return err
}

// compareAllUsage 对照控制器增量维护的用量与模型全量重算的用量。
func compareAllUsage(t *testing.T, c *Controller, m *model, step int) {
	t.Helper()
	for nsName, ns := range m.nss {
		for quotaName := range ns.quotas {
			want := m.usage(ns, quotaName)
			got, err := c.Usage(nsName, quotaName)
			if err != nil {
				t.Fatalf("step %d: 读取用量失败: %v", step, err)
			}
			for r, w := range want {
				if got[r] != w {
					t.Fatalf("step %d: 命名空间 %s 配额 %s 资源 %s: 控制器=%d，模型=%d",
						step, nsName, quotaName, r, got[r], w)
				}
			}
		}
	}
}
