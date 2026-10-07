package quota

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// ---- 独立朴素模型 ----
//
// naiveController 与被测控制器共享纯函数（补全、分类、资源解析、参数校验），
// 但准入与记账完全独立实现：不维护任何增量计数，每次判定都对已有 Pod 全量求和。
// 二者在随机操作序列上逐步对照，交叉验证增量记账的正确性。

type naivePod struct {
	eff   EffectivePod
	class PodClass
}

type naiveNS struct {
	defaults Defaults
	quotas   map[string]Quota
	pods     map[string]naivePod
}

type naiveController struct {
	namespaces map[string]*naiveNS
}

func newNaive() *naiveController {
	return &naiveController{namespaces: make(map[string]*naiveNS)}
}

// naiveMatches 独立实现的作用域匹配（不复用 scopeSet）。
func naiveMatches(scopes []Scope, c PodClass) bool {
	for _, s := range scopes {
		switch s {
		case ScopeBestEffort:
			if !c.BestEffort {
				return false
			}
		case ScopeNotBestEffort:
			if c.BestEffort {
				return false
			}
		case ScopeTerminating:
			if !c.Terminating {
				return false
			}
		case ScopeNotTerminating:
			if c.Terminating {
				return false
			}
		}
	}
	return true
}

func sortedKeysOf(m map[string]Quota) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedResourcesOf(hard map[ResourceName]int64) []ResourceName {
	keys := make([]ResourceName, 0, len(hard))
	for k := range hard {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// naiveSum 全量求和：配额 q 的资源 r 在所有适用 Pod（可排除一个）上的贡献之和。
func naiveSum(n *naiveNS, q Quota, r ResourceName, exclude string) int64 {
	sum := int64(0)
	for name, p := range n.pods {
		if name == exclude {
			continue
		}
		if !naiveMatches(q.Scopes, p.class) {
			continue
		}
		sum += contribution(p.eff, r)
	}
	return sum
}

func (m *naiveController) createNamespace(name string) error {
	if name == "" {
		return &Error{Kind: ErrInvalidArgument, Msg: "命名空间名称不能为空"}
	}
	if _, dup := m.namespaces[name]; dup {
		return &Error{Kind: ErrAlreadyExists, Msg: "命名空间已存在"}
	}
	m.namespaces[name] = &naiveNS{quotas: map[string]Quota{}, pods: map[string]naivePod{}}
	return nil
}

func (m *naiveController) deleteNamespace(name string) error {
	if _, ok := m.namespaces[name]; !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	delete(m.namespaces, name)
	return nil
}

func (m *naiveController) setDefaults(ns string, def Defaults) error {
	if err := validateDefaults(def); err != nil {
		return err
	}
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	n.defaults = def
	return nil
}

func (m *naiveController) addQuota(ns string, q Quota) error {
	if err := validateQuotaArgs(q); err != nil {
		return err
	}
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	if _, dup := n.quotas[q.Name]; dup {
		return &Error{Kind: ErrAlreadyExists}
	}
	if _, err := buildQuotaState(q); err != nil {
		return err
	}
	n.quotas[q.Name] = q
	return nil
}

func (m *naiveController) updateQuota(ns string, q Quota) error {
	if err := validateQuotaArgs(q); err != nil {
		return err
	}
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	if _, exists := n.quotas[q.Name]; !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: "配额不存在"}
	}
	if _, err := buildQuotaState(q); err != nil {
		return err
	}
	n.quotas[q.Name] = q
	return nil
}

func (m *naiveController) deleteQuota(ns, name string) error {
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	if _, exists := n.quotas[name]; !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: "配额不存在"}
	}
	delete(n.quotas, name)
	return nil
}

// naiveAdmit 全量准入核对：对每个适用配额、每个受限资源重新求和。
// exclude 非空表示重新准入，先把该 Pod 的既有贡献排除。
func naiveAdmit(n *naiveNS, eff EffectivePod, class PodClass, exclude string) error {
	var missing, exceeded *Error
	for _, qn := range sortedKeysOf(n.quotas) {
		q := n.quotas[qn]
		if !naiveMatches(q.Scopes, class) {
			continue
		}
		for _, r := range sortedResourcesOf(q.Hard) {
			if !hasDeclaration(eff, r) {
				if missing == nil {
					missing = &Error{Kind: ErrMissingDeclaration, Quota: qn, Resource: r}
				}
				continue
			}
			amount := contribution(eff, r)
			if amount > 0 && naiveSum(n, q, r, exclude)+amount > q.Hard[r] {
				if exceeded == nil {
					exceeded = &Error{Kind: ErrQuotaExceeded, Quota: qn, Resource: r}
				}
			}
		}
	}
	if missing != nil {
		return missing
	}
	if exceeded != nil {
		return exceeded
	}
	return nil
}

func (m *naiveController) createPod(ns string, p Pod) error {
	if err := validatePod(p); err != nil {
		return err
	}
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	if _, dup := n.pods[p.Name]; dup {
		return &Error{Kind: ErrAlreadyExists}
	}
	eff := defaultPod(p.Spec, n.defaults)
	class := classify(eff)
	if err := naiveAdmit(n, eff, class, ""); err != nil {
		return err
	}
	n.pods[p.Name] = naivePod{eff: eff, class: class}
	return nil
}

func (m *naiveController) updatePod(ns string, p Pod) error {
	if err := validatePod(p); err != nil {
		return err
	}
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	old, exists := n.pods[p.Name]
	if !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: "Pod 不存在"}
	}
	newEff := defaultPod(p.Spec, n.defaults)
	newClass := classify(newEff)
	if newClass == old.class {
		// 类别不变：仅就变大的资源核对（全量求和，含旧贡献）。
		var exceeded *Error
		for _, qn := range sortedKeysOf(n.quotas) {
			q := n.quotas[qn]
			if !naiveMatches(q.Scopes, newClass) {
				continue
			}
			for _, r := range sortedResourcesOf(q.Hard) {
				delta := contribution(newEff, r) - contribution(old.eff, r)
				if delta > 0 && naiveSum(n, q, r, "")+delta > q.Hard[r] && exceeded == nil {
					exceeded = &Error{Kind: ErrQuotaExceeded, Quota: qn, Resource: r}
				}
			}
		}
		if exceeded != nil {
			return exceeded
		}
	} else {
		// 类别变化：排除旧 Pod 后重新准入。
		if err := naiveAdmit(n, newEff, newClass, p.Name); err != nil {
			return err
		}
	}
	n.pods[p.Name] = naivePod{eff: newEff, class: newClass}
	return nil
}

func (m *naiveController) deletePod(ns, name string) error {
	if name == "" {
		return &Error{Kind: ErrInvalidArgument, Msg: "Pod 名称不能为空"}
	}
	n, ok := m.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound}
	}
	if _, exists := n.pods[name]; !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: "Pod 不存在"}
	}
	delete(n.pods, name)
	return nil
}

// naiveUsage 全量重算某配额的用量表。
func naiveUsage(n *naiveNS, q Quota) map[ResourceName]int64 {
	out := make(map[ResourceName]int64, len(q.Hard))
	for r := range q.Hard {
		out[r] = naiveSum(n, q, r, "")
	}
	return out
}

// ---- 随机操作生成 ----

type rndOp struct {
	kind     string
	ns       string
	name     string
	pod      Pod
	quota    Quota
	defaults Defaults
}

func (op rndOp) String() string {
	switch op.kind {
	case "CreateNamespace", "DeleteNamespace":
		return fmt.Sprintf("%s(ns=%q)", op.kind, op.ns)
	case "SetDefaults":
		return fmt.Sprintf("%s(ns=%q, defaults=%+v)", op.kind, op.ns, op.defaults)
	case "AddQuota", "UpdateQuota":
		return fmt.Sprintf("%s(ns=%q, quota=%+v)", op.kind, op.ns, op.quota)
	case "DeleteQuota":
		return fmt.Sprintf("%s(ns=%q, quota=%q)", op.kind, op.ns, op.name)
	case "CreatePod", "UpdatePod":
		return fmt.Sprintf("%s(ns=%q, pod=%+v)", op.kind, op.ns, op.pod)
	case "DeletePod":
		return fmt.Sprintf("%s(ns=%q, pod=%q)", op.kind, op.ns, op.name)
	}
	return op.kind
}

var (
	rndNamespaces   = []string{"a", "b", "ghost"}
	rndQuotaNames   = []string{"q0", "q1", "q2", "q3"}
	rndPodNames     = []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7"}
	rndBareResource = []string{"cpu", "mem", "disk"}
	rndHardResource = []ResourceName{
		ResourcePods, "requests.cpu", "limits.cpu",
		"requests.mem", "limits.mem", "requests.disk",
	}
	rndScopes = []Scope{ScopeBestEffort, ScopeNotBestEffort, ScopeTerminating, ScopeNotTerminating}
)

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.Intn(len(xs))] }

func genPod(r *rand.Rand) Pod {
	p := Pod{Name: pick(r, rndPodNames)}
	if r.Intn(100) < 80 {
		p.Spec.Requests = map[string]int64{}
		for _, x := range rndBareResource {
			if r.Intn(100) < 45 {
				p.Spec.Requests[x] = int64(r.Intn(4))
			}
		}
		if r.Intn(100) < 3 {
			p.Spec.Requests[pick(r, rndBareResource)] = -1 // 偶发非法参数
		}
	}
	if r.Intn(100) < 70 {
		p.Spec.Limits = map[string]int64{}
		for _, x := range rndBareResource {
			if r.Intn(100) < 40 {
				p.Spec.Limits[x] = int64(r.Intn(4))
			}
		}
	}
	switch d := r.Intn(100); {
	case d < 50:
	case d < 65:
		p.Spec.DeadlineSeconds = i64(0)
	case d < 95:
		p.Spec.DeadlineSeconds = i64(int64(1 + r.Intn(60)))
	default:
		p.Spec.DeadlineSeconds = i64(-1) // 偶发非法参数
	}
	return p
}

func genQuota(r *rand.Rand) Quota {
	q := Quota{Name: pick(r, rndQuotaNames)}
	for _, s := range rndScopes {
		if r.Intn(100) < 25 {
			q.Scopes = append(q.Scopes, s)
		}
	}
	q.Hard = map[ResourceName]int64{}
	for _, res := range rndHardResource {
		if r.Intn(100) < 35 {
			q.Hard[res] = int64(r.Intn(7))
		}
	}
	if r.Intn(100) < 4 {
		q.Hard[pick(r, rndHardResource)] = -1 // 偶发非法参数
	}
	if r.Intn(100) < 4 {
		q.Hard["cpu"] = 1 // 偶发非法资源名
	}
	return q
}

func genDefaults(r *rand.Rand) Defaults {
	def := Defaults{Rules: map[string]DefaultRule{}}
	for _, x := range []string{"cpu", "mem"} {
		if r.Intn(100) < 60 {
			rule := DefaultRule{}
			if r.Intn(100) < 50 {
				rule.Request = i64(int64(r.Intn(4)))
			}
			if r.Intn(100) < 50 {
				rule.Limit = i64(int64(r.Intn(4)))
			}
			if r.Intn(100) < 3 {
				rule.Request = i64(-1) // 偶发非法参数
			}
			def.Rules[x] = rule
		}
	}
	return def
}

func genOp(r *rand.Rand, seq int) rndOp {
	ns := pick(r, rndNamespaces)
	switch d := r.Intn(100); {
	case d < 4:
		name := ns
		if r.Intn(100) < 20 {
			name = fmt.Sprintf("n%d", seq%5)
		}
		return rndOp{kind: "CreateNamespace", ns: name}
	case d < 7:
		return rndOp{kind: "DeleteNamespace", ns: ns}
	case d < 15:
		return rndOp{kind: "SetDefaults", ns: ns, defaults: genDefaults(r)}
	case d < 27:
		return rndOp{kind: "AddQuota", ns: ns, quota: genQuota(r)}
	case d < 37:
		return rndOp{kind: "UpdateQuota", ns: ns, quota: genQuota(r)}
	case d < 43:
		return rndOp{kind: "DeleteQuota", ns: ns, name: pick(r, rndQuotaNames)}
	case d < 68:
		return rndOp{kind: "CreatePod", ns: ns, pod: genPod(r)}
	case d < 86:
		return rndOp{kind: "UpdatePod", ns: ns, pod: genPod(r)}
	default:
		return rndOp{kind: "DeletePod", ns: ns, name: pick(r, rndPodNames)}
	}
}

func applyOp(c *Controller, op rndOp) error {
	switch op.kind {
	case "CreateNamespace":
		return c.CreateNamespace(op.ns)
	case "DeleteNamespace":
		return c.DeleteNamespace(op.ns)
	case "SetDefaults":
		return c.SetDefaults(op.ns, op.defaults)
	case "AddQuota":
		return c.AddQuota(op.ns, op.quota)
	case "UpdateQuota":
		return c.UpdateQuota(op.ns, op.quota)
	case "DeleteQuota":
		return c.DeleteQuota(op.ns, op.name)
	case "CreatePod":
		return c.CreatePod(op.ns, op.pod)
	case "UpdatePod":
		return c.UpdatePod(op.ns, op.pod)
	case "DeletePod":
		return c.DeletePod(op.ns, op.name)
	}
	panic("未知操作 " + op.kind)
}

func applyNaive(m *naiveController, op rndOp) error {
	switch op.kind {
	case "CreateNamespace":
		return m.createNamespace(op.ns)
	case "DeleteNamespace":
		return m.deleteNamespace(op.ns)
	case "SetDefaults":
		return m.setDefaults(op.ns, op.defaults)
	case "AddQuota":
		return m.addQuota(op.ns, op.quota)
	case "UpdateQuota":
		return m.updateQuota(op.ns, op.quota)
	case "DeleteQuota":
		return m.deleteQuota(op.ns, op.name)
	case "CreatePod":
		return m.createPod(op.ns, op.pod)
	case "UpdatePod":
		return m.updatePod(op.ns, op.pod)
	case "DeletePod":
		return m.deletePod(op.ns, op.name)
	}
	panic("未知操作 " + op.kind)
}

// sameError 比较两个操作结果：类别、配额、资源三元组一致即视为相同。
func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ea, oka := a.(*Error)
	eb, okb := b.(*Error)
	if !oka || !okb {
		return false
	}
	return ea.Kind == eb.Kind && ea.Quota == eb.Quota && ea.Resource == eb.Resource
}

func errString(err error) string {
	if err == nil {
		return "成功"
	}
	return err.Error()
}

// rationale 给出该结果的判定依据。
func rationale(err error) string {
	if err == nil {
		return "校验与准入全部通过，状态已变更"
	}
	e, ok := err.(*Error)
	if !ok {
		return "非结构化错误"
	}
	switch e.Kind {
	case ErrInvalidArgument:
		return "参数非法（优先级最高），未触碰状态"
	case ErrNamespaceNotFound:
		return "命名空间不存在，未触碰状态"
	case ErrAlreadyExists:
		return "重复创建，优先于非法配置与准入失败"
	case ErrInvalidConfiguration:
		return "配额配置非法（矛盾/未知作用域、非法资源名、尽力型限非 pods）"
	case ErrMissingDeclaration:
		return "缺失声明优先于额度超限；取配额名、资源名升序第一处"
	case ErrQuotaExceeded:
		return "已用量加本 Pod 的量超过硬上限；全有或全无，不计入任何用量"
	}
	return "未知类别"
}

// compareState 逐步对照：命名空间集合、配额集合、Pod 集合与每配额每资源用量。
func compareState(t *testing.T, c *Controller, m *naiveController, seq int) {
	t.Helper()
	if len(c.namespaces) != len(m.namespaces) {
		t.Fatalf("op#%d 命名空间集合不一致: 实现 %v vs 模型 %v",
			seq, keysOf(c.namespaces), keysOfNaive(m.namespaces))
	}
	for ns, n := range c.namespaces {
		mn, ok := m.namespaces[ns]
		if !ok {
			t.Fatalf("op#%d 模型缺少命名空间 %q", seq, ns)
		}
		if len(n.quotas) != len(mn.quotas) || len(n.pods) != len(mn.pods) {
			t.Fatalf("op#%d ns=%q 配额/Pod 数量不一致: 实现 %d/%d vs 模型 %d/%d",
				seq, ns, len(n.quotas), len(n.pods), len(mn.quotas), len(mn.pods))
		}
		for name, ps := range n.pods {
			mp, ok := mn.pods[name]
			if !ok {
				t.Fatalf("op#%d ns=%q 模型缺少 Pod %q", seq, ns, name)
			}
			if !reflect.DeepEqual(ps.effective, mp.eff) || ps.class != mp.class {
				t.Fatalf("op#%d ns=%q Pod %q 记账视图不一致: 实现 %+v/%+v vs 模型 %+v/%+v",
					seq, ns, name, ps.effective, ps.class, mp.eff, mp.class)
			}
		}
		for qn, qs := range n.quotas {
			mq, ok := mn.quotas[qn]
			if !ok {
				t.Fatalf("op#%d ns=%q 模型缺少配额 %q", seq, ns, qn)
			}
			want := naiveUsage(mn, mq)
			if !reflect.DeepEqual(qs.used, want) {
				t.Fatalf("op#%d ns=%q 配额 %q 用量不一致: 实现 %v vs 模型 %v",
					seq, ns, qn, qs.used, want)
			}
		}
		if err := c.selfCheckLocked(ns); err != nil {
			t.Fatalf("op#%d ns=%q 自检失败: %v", seq, ns, err)
		}
	}
}

func keysOf(m map[string]*namespace) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func keysOfNaive(m map[string]*naiveNS) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestRandomSequenceAgainstNaiveModel 大量随机操作序列上逐步对照实现与朴素模型。
func TestRandomSequenceAgainstNaiveModel(t *testing.T) {
	seeds := []int64{1, 7, 42, 20261007, 987654321}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			c := NewController()
			m := newNaive()
			r := rand.New(rand.NewSource(seed))
			const steps = 1500
			for i := 0; i < steps; i++ {
				op := genOp(r, i)
				errImpl := applyOp(c, op)
				errModel := applyNaive(m, op)
				t.Logf("op#%d 输入=%s | 实际输出=%s | 判定依据=%s",
					i, op, errString(errImpl), rationale(errImpl))
				if !sameError(errImpl, errModel) {
					t.Fatalf("op#%d %s\n实现返回 %v\n模型返回 %v", i, op, errImpl, errModel)
				}
				compareState(t, c, m, i)
			}
			t.Logf("seed=%d 完成 %d 步随机操作, 实现与朴素模型逐步一致", seed, steps)
		})
	}
}
