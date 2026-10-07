package quota

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// podState 是已接纳 Pod 的记账状态：原始声明、补全结果与类别。
type podState struct {
	pod       Pod
	effective EffectivePod
	class     PodClass
}

// quotaState 是配额的运行时状态：配置、预排序的硬上限键与增量维护的已用量。
type quotaState struct {
	quota      Quota
	scopes     scopeSet
	sortedHard []ResourceName
	used       map[ResourceName]int64
}

type namespace struct {
	defaults Defaults
	quotas   map[string]*quotaState
	pods     map[string]*podState
}

// Controller 是配额准入控制器。所有公开方法都可并发调用，
// 内部以单一互斥锁串行化，效果等价于某个串行顺序。
type Controller struct {
	mu         sync.Mutex
	namespaces map[string]*namespace
	// podScans 统计对 Pod 集合的全量扫描次数，用于验证准入路径
	// 的开销与 Pod 数量无关（测试可读，不作为对外 API）。
	podScans int64
}

// NewController 返回一个空控制器。
func NewController() *Controller {
	return &Controller{namespaces: make(map[string]*namespace)}
}

// CreateNamespace 创建命名空间。
func (c *Controller) CreateNamespace(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.createNamespaceLocked(name)
}

// DeleteNamespace 删除命名空间及其全部配额与 Pod。
func (c *Controller) DeleteNamespace(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteNamespaceLocked(name)
}

// SetDefaults 设置（或整体替换）命名空间的默认值规则。
// 只影响之后的准入与调整，不回溯已接纳 Pod 的记账值。
func (c *Controller) SetDefaults(ns string, def Defaults) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.setDefaultsLocked(ns, def)
}

// AddQuota 新增配额，并按当前已存在的 Pod 重新得出其用量。
func (c *Controller) AddQuota(ns string, q Quota) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addQuotaLocked(ns, q)
}

// UpdateQuota 按名称整体替换配额的作用域与硬上限，并重算用量。
// 硬上限被调到低于当前用量是允许的，已有 Pod 不被驱逐。
func (c *Controller) UpdateQuota(ns string, q Quota) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updateQuotaLocked(ns, q)
}

// DeleteQuota 删除配额。
func (c *Controller) DeleteQuota(ns, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteQuotaLocked(ns, name)
}

// CreatePod 创建准入：全有或全无。
func (c *Controller) CreatePod(ns string, p Pod) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.createPodLocked(ns, p)
}

// UpdatePod 原位调整已存在 Pod 的请求量与上限量：全有或全无。
func (c *Controller) UpdatePod(ns string, p Pod) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updatePodLocked(ns, p)
}

// DeletePod 删除 Pod 并释放其在各适用配额下的全部用量。
func (c *Controller) DeletePod(ns, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deletePodLocked(ns, name)
}

// Usage 返回某配额当前各受限资源的已用量副本。
func (c *Controller) Usage(ns, quota string) (map[ResourceName]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usageLocked(ns, quota)
}

// SelfCheck 自检：重算每个配额每个受限资源的已用量并与记账值比对。
func (c *Controller) SelfCheck(ns string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selfCheckLocked(ns)
}

// ---- 锁内实现 ----

func (c *Controller) createNamespaceLocked(name string) error {
	if name == "" {
		return &Error{Kind: ErrInvalidArgument, Msg: "命名空间名称不能为空"}
	}
	if _, dup := c.namespaces[name]; dup {
		return &Error{Kind: ErrAlreadyExists, Msg: fmt.Sprintf("命名空间 %q 已存在", name)}
	}
	c.namespaces[name] = &namespace{
		quotas: make(map[string]*quotaState),
		pods:   make(map[string]*podState),
	}
	return nil
}

func (c *Controller) deleteNamespaceLocked(name string) error {
	if _, ok := c.namespaces[name]; !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", name)}
	}
	delete(c.namespaces, name)
	return nil
}

func (c *Controller) setDefaultsLocked(ns string, def Defaults) error {
	if err := validateDefaults(def); err != nil {
		return err
	}
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	n.defaults = def
	return nil
}

func (c *Controller) addQuotaLocked(ns string, q Quota) error {
	if err := validateQuotaArgs(q); err != nil {
		return err
	}
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	if _, dup := n.quotas[q.Name]; dup {
		return &Error{Kind: ErrAlreadyExists, Msg: fmt.Sprintf("配额 %q 已存在", q.Name)}
	}
	qs, err := buildQuotaState(q)
	if err != nil {
		return err
	}
	c.podScans++
	n.recomputeUsage(qs)
	n.quotas[q.Name] = qs
	return nil
}

func (c *Controller) updateQuotaLocked(ns string, q Quota) error {
	if err := validateQuotaArgs(q); err != nil {
		return err
	}
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	if _, exists := n.quotas[q.Name]; !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("配额 %q 不存在", q.Name)}
	}
	qs, err := buildQuotaState(q)
	if err != nil {
		return err
	}
	c.podScans++
	n.recomputeUsage(qs)
	n.quotas[q.Name] = qs
	return nil
}

func (c *Controller) deleteQuotaLocked(ns, name string) error {
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	if _, exists := n.quotas[name]; !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("配额 %q 不存在", name)}
	}
	delete(n.quotas, name)
	return nil
}

func (c *Controller) createPodLocked(ns string, p Pod) error {
	if err := validatePod(p); err != nil {
		return err
	}
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	if _, dup := n.pods[p.Name]; dup {
		return &Error{Kind: ErrAlreadyExists, Msg: fmt.Sprintf("Pod %q 已存在", p.Name)}
	}
	eff := defaultPod(p.Spec, n.defaults)
	class := classify(eff)
	if err := n.admissionCheck(eff, class, nil); err != nil {
		return err
	}
	n.charge(eff, class, +1)
	n.pods[p.Name] = &podState{pod: p, effective: eff, class: class}
	return nil
}

func (c *Controller) updatePodLocked(ns string, p Pod) error {
	if err := validatePod(p); err != nil {
		return err
	}
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	old, exists := n.pods[p.Name]
	if !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("Pod %q 不存在", p.Name)}
	}
	newEff := defaultPod(p.Spec, n.defaults)
	newClass := classify(newEff)

	if newClass == old.class {
		// 类别未变：适用配额集合不变，仅就变大的资源核对超限，
		// 变小的资源释放用量，无变化的资源不核对。
		if err := n.deltaCheck(old.effective, newEff, newClass); err != nil {
			return err
		}
		for _, qs := range n.quotas {
			if !qs.scopes.matches(newClass) {
				continue
			}
			for r := range qs.quota.Hard {
				qs.used[r] += contribution(newEff, r) - contribution(old.effective, r)
			}
		}
	} else {
		// 类别变化：视为从原适用配额中释放、对新适用配额重新准入。
		if err := n.admissionCheck(newEff, newClass, old); err != nil {
			return err
		}
		n.charge(old.effective, old.class, -1)
		n.charge(newEff, newClass, +1)
	}
	n.pods[p.Name] = &podState{pod: p, effective: newEff, class: newClass}
	return nil
}

func (c *Controller) deletePodLocked(ns, name string) error {
	if name == "" {
		return &Error{Kind: ErrInvalidArgument, Msg: "Pod 名称不能为空"}
	}
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	ps, exists := n.pods[name]
	if !exists {
		return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("Pod %q 不存在", name)}
	}
	n.charge(ps.effective, ps.class, -1)
	delete(n.pods, name)
	return nil
}

func (c *Controller) usageLocked(ns, quota string) (map[ResourceName]int64, error) {
	n, ok := c.namespaces[ns]
	if !ok {
		return nil, &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	qs, exists := n.quotas[quota]
	if !exists {
		return nil, &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("配额 %q 不存在", quota)}
	}
	out := make(map[ResourceName]int64, len(qs.used))
	for r, v := range qs.used {
		out[r] = v
	}
	return out, nil
}

func (c *Controller) selfCheckLocked(ns string) error {
	n, ok := c.namespaces[ns]
	if !ok {
		return &Error{Kind: ErrNamespaceNotFound, Msg: fmt.Sprintf("命名空间 %q 不存在", ns)}
	}
	want := make(map[string]map[ResourceName]int64, len(n.quotas))
	for qn, qs := range n.quotas {
		w := make(map[ResourceName]int64, len(qs.quota.Hard))
		for r := range qs.quota.Hard {
			w[r] = 0
		}
		want[qn] = w
	}
	c.podScans++
	for _, ps := range n.pods {
		for qn, qs := range n.quotas {
			if !qs.scopes.matches(ps.class) {
				continue
			}
			for r := range qs.quota.Hard {
				want[qn][r] += contribution(ps.effective, r)
			}
		}
	}
	var problems []string
	for qn, qs := range n.quotas {
		for r, used := range qs.used {
			w, limited := want[qn][r]
			if !limited {
				problems = append(problems,
					fmt.Sprintf("配额 %q 的资源 %s 不在硬上限中却有已用量 %d", qn, r, used))
				continue
			}
			if w != used {
				problems = append(problems,
					fmt.Sprintf("配额 %q 资源 %s 记账值 %d 与重算值 %d 不一致", qn, r, used, w))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("自检失败: %s", strings.Join(problems, "; "))
	}
	return nil
}

// ---- 命名空间内部的准入与记账 ----

// admissionCheck 对补全后的 Pod 做全量准入核对（创建与类别迁移时的重新准入共用）。
// old 非空表示重新准入：核对时先把 old 在其原适用配额中的贡献从已用量中扣除。
// 缺失声明优先于额度超限；同类问题取配额名称升序、其下资源名称升序的第一处。
func (n *namespace) admissionCheck(eff EffectivePod, class PodClass, old *podState) error {
	var missing, exceeded *Error
	for _, qn := range n.sortedQuotaNames() {
		qs := n.quotas[qn]
		if !qs.scopes.matches(class) {
			continue
		}
		oldApplies := old != nil && qs.scopes.matches(old.class)
		for _, r := range qs.sortedHard {
			hard := qs.quota.Hard[r]
			if !hasDeclaration(eff, r) {
				if missing == nil {
					missing = &Error{Kind: ErrMissingDeclaration, Quota: qn, Resource: r,
						Msg: fmt.Sprintf("配额 %q 限制了 %s，但 Pod 补全后该资源仍缺省", qn, r)}
				}
				continue
			}
			used := qs.used[r]
			if oldApplies {
				used -= contribution(old.effective, r)
			}
			// 用量为零的准入不受影响：只有确实增加用量且超过硬上限才拒绝。
			if amount := contribution(eff, r); amount > 0 && used+amount > hard {
				if exceeded == nil {
					exceeded = &Error{Kind: ErrQuotaExceeded, Quota: qn, Resource: r,
						Msg: fmt.Sprintf("配额 %q 资源 %s 已用量 %d + 本 Pod %d 超过硬上限 %d",
							qn, r, used, amount, hard)}
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

// deltaCheck 是类别不变时的增量核对：只检查变大的资源。
func (n *namespace) deltaCheck(oldEff, newEff EffectivePod, class PodClass) error {
	var exceeded *Error
	for _, qn := range n.sortedQuotaNames() {
		qs := n.quotas[qn]
		if !qs.scopes.matches(class) {
			continue
		}
		for _, r := range qs.sortedHard {
			delta := contribution(newEff, r) - contribution(oldEff, r)
			if delta > 0 && qs.used[r]+delta > qs.quota.Hard[r] && exceeded == nil {
				exceeded = &Error{Kind: ErrQuotaExceeded, Quota: qn, Resource: r,
					Msg: fmt.Sprintf("配额 %q 资源 %s 已用量 %d + 增量 %d 超过硬上限 %d",
						qn, r, qs.used[r], delta, qs.quota.Hard[r])}
			}
		}
	}
	if exceeded != nil {
		return exceeded
	}
	return nil
}

// charge 把 Pod 的贡献按 sign（+1/-1）计入各适用配额。
func (n *namespace) charge(eff EffectivePod, class PodClass, sign int64) {
	for _, qs := range n.quotas {
		if !qs.scopes.matches(class) {
			continue
		}
		for r := range qs.quota.Hard {
			qs.used[r] += sign * contribution(eff, r)
		}
	}
}

// recomputeUsage 全量重算单个配额的用量（新增/修改配额时调用）。
func (n *namespace) recomputeUsage(qs *quotaState) {
	used := make(map[ResourceName]int64, len(qs.quota.Hard))
	for r := range qs.quota.Hard {
		used[r] = 0
	}
	for _, ps := range n.pods {
		if !qs.scopes.matches(ps.class) {
			continue
		}
		for r := range qs.quota.Hard {
			used[r] += contribution(ps.effective, r)
		}
	}
	qs.used = used
}

func (n *namespace) sortedQuotaNames() []string {
	names := make([]string, 0, len(n.quotas))
	for name := range n.quotas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ---- 纯函数：贡献量、声明存在性、校验 ----

// contribution 计算补全后的 Pod 对资源 r 的记账量；缺省按 0 计。
func contribution(eff EffectivePod, r ResourceName) int64 {
	kind, name, ok := parseResourceName(r)
	if !ok {
		return 0
	}
	switch kind {
	case kindPods:
		return 1
	case kindRequests:
		return eff.Requests[name]
	case kindLimits:
		return eff.Limits[name]
	}
	return 0
}

// hasDeclaration 判断资源 r 对该 Pod 是否有声明（补全后非缺省）。
// pods 与无法解析的资源名永远视为有声明。
func hasDeclaration(eff EffectivePod, r ResourceName) bool {
	kind, name, ok := parseResourceName(r)
	if !ok {
		return true
	}
	switch kind {
	case kindRequests:
		_, ok := eff.Requests[name]
		return ok
	case kindLimits:
		_, ok := eff.Limits[name]
		return ok
	}
	return true
}

func validatePod(p Pod) error {
	if p.Name == "" {
		return &Error{Kind: ErrInvalidArgument, Msg: "Pod 名称不能为空"}
	}
	for x, v := range p.Spec.Requests {
		if !isValidBareResource(x) {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("非法资源名 %q", x)}
		}
		if v < 0 {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("资源 %q 的请求量为负", x)}
		}
	}
	for x, v := range p.Spec.Limits {
		if !isValidBareResource(x) {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("非法资源名 %q", x)}
		}
		if v < 0 {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("资源 %q 的上限量为负", x)}
		}
	}
	if p.Spec.DeadlineSeconds != nil && *p.Spec.DeadlineSeconds < 0 {
		return &Error{Kind: ErrInvalidArgument, Msg: "存活截止时长不能为负"}
	}
	return nil
}

func validateDefaults(def Defaults) error {
	for x, rule := range def.Rules {
		if !isValidBareResource(x) {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("非法资源名 %q", x)}
		}
		if rule.Request != nil && *rule.Request < 0 {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("资源 %q 的默认请求量为负", x)}
		}
		if rule.Limit != nil && *rule.Limit < 0 {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("资源 %q 的默认上限量为负", x)}
		}
	}
	return nil
}

// validateQuotaArgs 只做参数级校验（优先级高于非法配置）。
func validateQuotaArgs(q Quota) error {
	if q.Name == "" {
		return &Error{Kind: ErrInvalidArgument, Msg: "配额名称不能为空"}
	}
	for r, v := range q.Hard {
		if v < 0 {
			return &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("资源 %s 的硬上限为负", r)}
		}
	}
	return nil
}

// buildQuotaState 做配置级校验并构造运行时状态。
func buildQuotaState(q Quota) (*quotaState, error) {
	scopes, err := newScopeSet(q.Scopes)
	if err != nil {
		return nil, err
	}
	for r := range q.Hard {
		if _, _, ok := parseResourceName(r); !ok {
			return nil, &Error{Kind: ErrInvalidConfiguration,
				Msg: fmt.Sprintf("硬上限资源名 %q 非法，只允许 pods、requests.X、limits.X", r)}
		}
	}
	if _, bestEffort := scopes[ScopeBestEffort]; bestEffort {
		for r := range q.Hard {
			if r != ResourcePods {
				return nil, &Error{Kind: ErrInvalidConfiguration,
					Msg: fmt.Sprintf("含尽力型作用域的配额硬上限只允许 pods，出现 %q", r)}
			}
		}
	}
	sortedHard := make([]ResourceName, 0, len(q.Hard))
	for r := range q.Hard {
		sortedHard = append(sortedHard, r)
	}
	sort.Slice(sortedHard, func(i, j int) bool { return sortedHard[i] < sortedHard[j] })
	return &quotaState{quota: q, scopes: scopes, sortedHard: sortedHard}, nil
}
