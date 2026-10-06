package quota

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Controller 是多租户命名空间的资源配额准入控制器。
//
// 并发：全部公开方法由一把互斥锁串行化，任意并发调用的结果
// 等价于某个串行顺序；配额用量计数在锁内增量维护，
// 不会因并发准入而超过硬上限。
//
// 性能：Pod 的创建 / 调整 / 删除只访问"适用配额 x 硬上限资源"，
// 开销与命名空间内已有 Pod 数量无关；podsScanned 计数器记录
// 全部对 Pod 集合的扫描，供测试证明上述性质。
type Controller struct {
	mu          sync.Mutex
	namespaces  map[string]*namespace
	podsScanned atomic.Int64
}

type namespace struct {
	defaults Defaults
	pods     map[string]*podEntry
	quotas   map[string]*quotaEntry
}

type podEntry struct {
	spec      PodSpec
	completed completed
	attrs     attributes
}

type quotaEntry struct {
	name   string
	spec   QuotaSpec
	scopes scopeSet
	used   map[ResourceName]int64 // 仅跟踪硬上限中出现的资源
}

// NewController 返回一个空的控制器。
func NewController() *Controller {
	return &Controller{namespaces: make(map[string]*namespace)}
}

// PodsScanned 返回至今对 Pod 集合的扫描次数（测试插桩）。
// Pod 的创建 / 调整 / 删除不会增加该计数。
func (c *Controller) PodsScanned() int64 { return c.podsScanned.Load() }

// CreateNamespace 创建命名空间。
func (c *Controller) CreateNamespace(name string) error {
	if name == "" {
		return invalidArg("命名空间名称不能为空")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.namespaces[name]; ok {
		return alreadyExists("命名空间", name)
	}
	c.namespaces[name] = &namespace{
		defaults: Defaults{},
		pods:     map[string]*podEntry{},
		quotas:   map[string]*quotaEntry{},
	}
	return nil
}

// DeleteNamespace 删除命名空间及其全部 Pod 与配额。
func (c *Controller) DeleteNamespace(name string) error {
	if name == "" {
		return invalidArg("命名空间名称不能为空")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.namespaces[name]; !ok {
		return nsNotFound(name)
	}
	delete(c.namespaces, name)
	return nil
}

// SetDefaults 设置命名空间的默认值规则。
// 默认值变化会改变既有 Pod 的补全结果与属性（进而改变适用配额集合），
// 因此重建全部配额用量；既有 Pod 不重新准入、不被驱逐。
func (c *Controller) SetDefaults(nsName string, d Defaults) error {
	if err := d.validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	ns.defaults = cloneDefaults(d)
	for _, pe := range ns.pods {
		c.podsScanned.Add(1)
		pe.completed = complete(pe.spec, ns.defaults)
		pe.attrs = attributesOf(pe.completed, pe.spec)
	}
	for _, q := range ns.quotas {
		c.recomputeLocked(ns, q)
	}
	return nil
}

// getNS 取命名空间；调用方须已持有锁。
func (c *Controller) getNS(name string) (*namespace, *Error) {
	ns, ok := c.namespaces[name]
	if !ok {
		return nil, nsNotFound(name)
	}
	return ns, nil
}

// CreatePod 创建准入：补全后对所有适用配额做全有或全无的核对，
// 全部通过才计入用量；任一不通过则不计入任何用量。
func (c *Controller) CreatePod(nsName, name string, spec PodSpec) error {
	if name == "" {
		return invalidArg("pod 名称不能为空")
	}
	if err := spec.validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	if _, dup := ns.pods[name]; dup {
		return alreadyExists("pod", name)
	}
	pe := newPodEntry(spec, ns.defaults)
	quotas := ns.applicable(pe.attrs)
	if err := checkMissing(quotas, pe); err != nil {
		return err
	}
	for _, q := range quotas {
		for _, r := range sortedResources(q.spec.Hard) {
			add := contribution(pe, r)
			if q.used[r]+add > q.spec.Hard[r] {
				return quotaExceeded(q.name, r, q.used[r], add, q.spec.Hard[r])
			}
		}
	}
	for _, q := range quotas {
		q.add(pe, +1)
	}
	ns.pods[name] = pe
	return nil
}

// UpdatePod 原位调整已存在 Pod 的请求量与上限量（及存活截止时长）。
// 属性不变时仅就变大的资源核对超限，变小的资源释放用量；
// 属性（服务等级 / 期限）变化时视为从原适用配额释放、对新适用配额
// 重新准入。调整同样全有或全无。
func (c *Controller) UpdatePod(nsName, name string, spec PodSpec) error {
	if name == "" {
		return invalidArg("pod 名称不能为空")
	}
	if err := spec.validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	old, ok := ns.pods[name]
	if !ok {
		return invalidArg(fmt.Sprintf("pod %q 不存在", name))
	}
	pe := newPodEntry(spec, ns.defaults)
	if pe.attrs == old.attrs {
		return c.updateSameAttrs(ns, name, old, pe)
	}
	return c.updateAttrsChanged(ns, name, old, pe)
}

// updateSameAttrs 处理属性不变的原位调整：增量核算。
func (c *Controller) updateSameAttrs(ns *namespace, name string, old, pe *podEntry) error {
	quotas := ns.applicable(pe.attrs)
	if err := checkMissing(quotas, pe); err != nil {
		return err
	}
	for _, q := range quotas {
		for _, r := range sortedResources(q.spec.Hard) {
			delta := contribution(pe, r) - contribution(old, r)
			if delta > 0 && q.used[r]+delta > q.spec.Hard[r] {
				return quotaExceeded(q.name, r, q.used[r], delta, q.spec.Hard[r])
			}
		}
	}
	for _, q := range quotas {
		q.add(old, -1)
		q.add(pe, +1)
	}
	ns.pods[name] = pe
	return nil
}

// updateAttrsChanged 处理属性变化的原位调整：
// 从原适用配额释放，对新适用配额重新准入（全量核对）。
func (c *Controller) updateAttrsChanged(ns *namespace, name string, old, pe *podEntry) error {
	oldQuotas := ns.applicable(old.attrs)
	newQuotas := ns.applicable(pe.attrs)
	if err := checkMissing(newQuotas, pe); err != nil {
		return err
	}
	inOld := make(map[*quotaEntry]bool, len(oldQuotas))
	for _, q := range oldQuotas {
		inOld[q] = true
	}
	for _, q := range newQuotas {
		for _, r := range sortedResources(q.spec.Hard) {
			base := q.used[r]
			if inOld[q] {
				base -= contribution(old, r)
			}
			add := contribution(pe, r)
			if base+add > q.spec.Hard[r] {
				return quotaExceeded(q.name, r, base, add, q.spec.Hard[r])
			}
		}
	}
	for _, q := range oldQuotas {
		q.add(old, -1)
	}
	for _, q := range newQuotas {
		q.add(pe, +1)
	}
	ns.pods[name] = pe
	return nil
}

// DeletePod 删除 Pod 并释放其在各适用配额下的全部用量。
func (c *Controller) DeletePod(nsName, name string) error {
	if name == "" {
		return invalidArg("pod 名称不能为空")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	pe, ok := ns.pods[name]
	if !ok {
		return invalidArg(fmt.Sprintf("pod %q 不存在", name))
	}
	for _, q := range ns.applicable(pe.attrs) {
		q.add(pe, -1)
	}
	delete(ns.pods, name)
	return nil
}

// CreateQuota 新增配额，其用量按当前已存在的 Pod 重新得出。
func (c *Controller) CreateQuota(nsName, name string, spec QuotaSpec) error {
	if name == "" {
		return invalidArg("配额名称不能为空")
	}
	if err := validateQuotaParams(spec); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	if _, dup := ns.quotas[name]; dup {
		return alreadyExists("配额", name)
	}
	if err := validateQuotaConfig(spec); err != nil {
		return err
	}
	q := &quotaEntry{name: name, spec: spec.Clone(), scopes: newScopeSet(spec.Scopes)}
	c.recomputeLocked(ns, q)
	ns.quotas[name] = q
	return nil
}

// UpdateQuota 修改配额的作用域与硬上限。
// 上限被修改到低于当前用量时允许：既有 Pod 不被驱逐，
// 之后任何会使该资源用量增加的准入都被拒绝。
func (c *Controller) UpdateQuota(nsName, name string, spec QuotaSpec) error {
	if name == "" {
		return invalidArg("配额名称不能为空")
	}
	if err := validateQuotaParams(spec); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	q, ok := ns.quotas[name]
	if !ok {
		return invalidArg(fmt.Sprintf("配额 %q 不存在", name))
	}
	if err := validateQuotaConfig(spec); err != nil {
		return err
	}
	q.spec = spec.Clone()
	q.scopes = newScopeSet(spec.Scopes)
	c.recomputeLocked(ns, q)
	return nil
}

// DeleteQuota 删除配额。
func (c *Controller) DeleteQuota(nsName, name string) error {
	if name == "" {
		return invalidArg("配额名称不能为空")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return err
	}
	if _, ok := ns.quotas[name]; !ok {
		return invalidArg(fmt.Sprintf("配额 %q 不存在", name))
	}
	delete(ns.quotas, name)
	return nil
}

// Usage 返回配额当前各硬上限资源的已用量快照。
func (c *Controller) Usage(nsName, quotaName string) (map[ResourceName]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, err := c.getNS(nsName)
	if err != nil {
		return nil, err
	}
	q, ok := ns.quotas[quotaName]
	if !ok {
		return nil, invalidArg(fmt.Sprintf("配额 %q 不存在", quotaName))
	}
	out := make(map[ResourceName]int64, len(q.used))
	for r, v := range q.used {
		out[r] = v
	}
	return out, nil
}

// CheckConsistency 自检：由存储的 Pod 声明出发重新补全、重新判定
// 适用配额并汇总用量，与增量维护的计数逐一比对。
// 任何时刻每个配额每个资源的已用量必须等于其适用 Pod 补全后的量之和。
func (c *Controller) CheckConsistency() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for nsName, ns := range c.namespaces {
		fresh := make(map[string]map[ResourceName]int64, len(ns.quotas))
		for _, q := range ns.quotas {
			fresh[q.name] = zeroUsage(q.spec.Hard)
		}
		for podName, pe := range ns.pods {
			c.podsScanned.Add(1)
			reCompleted := complete(pe.spec, ns.defaults)
			reAttrs := attributesOf(reCompleted, pe.spec)
			if !equalCompleted(reCompleted, pe.completed) || reAttrs != pe.attrs {
				return fmt.Errorf("命名空间 %q pod %q: 缓存的补全结果 / 属性与重算不一致", nsName, podName)
			}
			for _, q := range ns.quotas {
				if q.scopes.matches(reAttrs) {
					for r := range q.spec.Hard {
						fresh[q.name][r] += contribution(pe, r)
					}
				}
			}
		}
		for _, q := range ns.quotas {
			for r, want := range fresh[q.name] {
				if got := q.used[r]; got != want {
					return fmt.Errorf("命名空间 %q 配额 %q 资源 %s: 增量计数 %d != 重算 %d",
						nsName, q.name, r, got, want)
				}
			}
		}
	}
	return nil
}

// newPodEntry 补全并推导属性，构造 Pod 的内部表示。
func newPodEntry(spec PodSpec, defs Defaults) *podEntry {
	pe := &podEntry{spec: spec.Clone()}
	pe.completed = complete(pe.spec, defs)
	pe.attrs = attributesOf(pe.completed, pe.spec)
	return pe
}

// applicable 返回属性 a 命中的配额，按配额名称升序。
func (ns *namespace) applicable(a attributes) []*quotaEntry {
	out := make([]*quotaEntry, 0, len(ns.quotas))
	for _, q := range ns.quotas {
		if q.scopes.matches(a) {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// checkMissing 缺失声明核对：对适用配额（按名称升序）下每个被限制的
// requests.X / limits.X（按资源名升序），Pod 补全后仍缺省即拒绝。
func checkMissing(quotas []*quotaEntry, pe *podEntry) *Error {
	for _, q := range quotas {
		for _, r := range sortedResources(q.spec.Hard) {
			if r == ResourcePods {
				continue
			}
			if _, ok := pe.completed.value(r); !ok {
				return missingDeclaration(q.name, r)
			}
		}
	}
	return nil
}

// contribution 返回 Pod 对配额资源 r 的被记账量；缺省按 0 计。
func contribution(pe *podEntry, r ResourceName) int64 {
	if r == ResourcePods {
		return 1
	}
	v, _ := pe.completed.value(r)
	return v
}

// add 把 Pod 对各硬上限资源的记账量按 sign（+1/-1）计入配额用量。
func (q *quotaEntry) add(pe *podEntry, sign int64) {
	for r := range q.spec.Hard {
		q.used[r] += sign * contribution(pe, r)
	}
}

// recomputeLocked 扫描命名空间内全部 Pod，重建配额用量；调用方须已持有锁。
func (c *Controller) recomputeLocked(ns *namespace, q *quotaEntry) {
	used := zeroUsage(q.spec.Hard)
	for _, pe := range ns.pods {
		c.podsScanned.Add(1)
		if q.scopes.matches(pe.attrs) {
			for r := range q.spec.Hard {
				used[r] += contribution(pe, r)
			}
		}
	}
	q.used = used
}

func zeroUsage(hard map[ResourceName]int64) map[ResourceName]int64 {
	used := make(map[ResourceName]int64, len(hard))
	for r := range hard {
		used[r] = 0
	}
	return used
}

func sortedResources(m map[ResourceName]int64) []ResourceName {
	out := make([]ResourceName, 0, len(m))
	for r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalCompleted(a, b completed) bool {
	return equalAmounts(a.requests, b.requests) && equalAmounts(a.limits, b.limits)
}

func equalAmounts(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
