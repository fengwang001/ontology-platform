package quota

import (
	"fmt"
	"sort"
)

// model 是独立的朴素参照实现：不维护任何增量状态，
// 每次需要用量时都从原始声明出发全量重算。
// 它与被测控制器共享经过单元测试的纯函数（补全、属性、作用域匹配），
// 但记账与准入路径完全独立。
type model struct {
	nss map[string]*modelNS
}

type modelNS struct {
	defaults Defaults
	pods     map[string]PodSpec
	quotas   map[string]QuotaSpec
}

func newModel() *model { return &model{nss: map[string]*modelNS{}} }

func newModelNS() *modelNS {
	return &modelNS{defaults: Defaults{}, pods: map[string]PodSpec{}, quotas: map[string]QuotaSpec{}}
}

// usage 全量重算配额用量。
func (m *model) usage(ns *modelNS, quotaName string) map[ResourceName]int64 {
	q := ns.quotas[quotaName]
	used := zeroUsage(q.Hard)
	scopes := newScopeSet(q.Scopes)
	for _, spec := range ns.pods {
		comp := complete(spec, ns.defaults)
		if !scopes.matches(attributesOf(comp, spec)) {
			continue
		}
		for r := range q.Hard {
			used[r] += modelContribution(comp, r)
		}
	}
	return used
}

func modelContribution(comp completed, r ResourceName) int64 {
	if r == ResourcePods {
		return 1
	}
	v, _ := comp.value(r)
	return v
}

func (m *model) applicable(ns *modelNS, a attributes) []string {
	var names []string
	for name, q := range ns.quotas {
		if newScopeSet(q.Scopes).matches(a) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (m *model) createNamespace(name string) *Error {
	if name == "" {
		return invalidArg("命名空间名称不能为空")
	}
	if _, ok := m.nss[name]; ok {
		return alreadyExists("命名空间", name)
	}
	m.nss[name] = newModelNS()
	return nil
}

func (m *model) deleteNamespace(name string) *Error {
	if name == "" {
		return invalidArg("命名空间名称不能为空")
	}
	if _, ok := m.nss[name]; !ok {
		return nsNotFound(name)
	}
	delete(m.nss, name)
	return nil
}

func (m *model) setDefaults(nsName string, d Defaults) *Error {
	if err := d.validate(); err != nil {
		return err
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	ns.defaults = cloneDefaults(d)
	return nil
}

// modelCheckMissing 缺失声明核对（配额名升序、资源名升序的第一处）。
func modelCheckMissing(ns *modelNS, quotaNames []string, comp completed) *Error {
	for _, qn := range quotaNames {
		for _, r := range sortedResources(ns.quotas[qn].Hard) {
			if r == ResourcePods {
				continue
			}
			if _, ok := comp.value(r); !ok {
				return missingDeclaration(qn, r)
			}
		}
	}
	return nil
}

func (m *model) createPod(nsName, name string, spec PodSpec) *Error {
	if name == "" {
		return invalidArg("pod 名称不能为空")
	}
	if err := spec.validate(); err != nil {
		return err
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	if _, dup := ns.pods[name]; dup {
		return alreadyExists("pod", name)
	}
	comp := complete(spec, ns.defaults)
	quotas := m.applicable(ns, attributesOf(comp, spec))
	if err := modelCheckMissing(ns, quotas, comp); err != nil {
		return err
	}
	for _, qn := range quotas {
		q := ns.quotas[qn]
		used := m.usage(ns, qn)
		for _, r := range sortedResources(q.Hard) {
			add := modelContribution(comp, r)
			if used[r]+add > q.Hard[r] {
				return quotaExceeded(qn, r, used[r], add, q.Hard[r])
			}
		}
	}
	ns.pods[name] = spec.Clone()
	return nil
}

func (m *model) updatePod(nsName, name string, spec PodSpec) *Error {
	if name == "" {
		return invalidArg("pod 名称不能为空")
	}
	if err := spec.validate(); err != nil {
		return err
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	oldSpec, ok := ns.pods[name]
	if !ok {
		return invalidArg(fmt.Sprintf("pod %q 不存在", name))
	}
	oldComp := complete(oldSpec, ns.defaults)
	oldAttrs := attributesOf(oldComp, oldSpec)
	newComp := complete(spec, ns.defaults)
	newAttrs := attributesOf(newComp, spec)

	if newAttrs == oldAttrs {
		quotas := m.applicable(ns, newAttrs)
		if err := modelCheckMissing(ns, quotas, newComp); err != nil {
			return err
		}
		for _, qn := range quotas {
			q := ns.quotas[qn]
			used := m.usage(ns, qn)
			for _, r := range sortedResources(q.Hard) {
				delta := modelContribution(newComp, r) - modelContribution(oldComp, r)
				if delta > 0 && used[r]+delta > q.Hard[r] {
					return quotaExceeded(qn, r, used[r], delta, q.Hard[r])
				}
			}
		}
	} else {
		oldSet := map[string]bool{}
		for _, qn := range m.applicable(ns, oldAttrs) {
			oldSet[qn] = true
		}
		newQuotas := m.applicable(ns, newAttrs)
		if err := modelCheckMissing(ns, newQuotas, newComp); err != nil {
			return err
		}
		for _, qn := range newQuotas {
			q := ns.quotas[qn]
			used := m.usage(ns, qn)
			for _, r := range sortedResources(q.Hard) {
				base := used[r]
				if oldSet[qn] {
					base -= modelContribution(oldComp, r)
				}
				add := modelContribution(newComp, r)
				if base+add > q.Hard[r] {
					return quotaExceeded(qn, r, base, add, q.Hard[r])
				}
			}
		}
	}
	ns.pods[name] = spec.Clone()
	return nil
}

func (m *model) deletePod(nsName, name string) *Error {
	if name == "" {
		return invalidArg("pod 名称不能为空")
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	if _, ok := ns.pods[name]; !ok {
		return invalidArg(fmt.Sprintf("pod %q 不存在", name))
	}
	delete(ns.pods, name)
	return nil
}

func (m *model) createQuota(nsName, name string, spec QuotaSpec) *Error {
	if name == "" {
		return invalidArg("配额名称不能为空")
	}
	if err := validateQuotaParams(spec); err != nil {
		return err
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	if _, dup := ns.quotas[name]; dup {
		return alreadyExists("配额", name)
	}
	if err := validateQuotaConfig(spec); err != nil {
		return err
	}
	ns.quotas[name] = spec.Clone()
	return nil
}

func (m *model) updateQuota(nsName, name string, spec QuotaSpec) *Error {
	if name == "" {
		return invalidArg("配额名称不能为空")
	}
	if err := validateQuotaParams(spec); err != nil {
		return err
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	if _, ok := ns.quotas[name]; !ok {
		return invalidArg(fmt.Sprintf("配额 %q 不存在", name))
	}
	if err := validateQuotaConfig(spec); err != nil {
		return err
	}
	ns.quotas[name] = spec.Clone()
	return nil
}

func (m *model) deleteQuota(nsName, name string) *Error {
	if name == "" {
		return invalidArg("配额名称不能为空")
	}
	ns, ok := m.nss[nsName]
	if !ok {
		return nsNotFound(nsName)
	}
	if _, ok := ns.quotas[name]; !ok {
		return invalidArg(fmt.Sprintf("配额 %q 不存在", name))
	}
	delete(ns.quotas, name)
	return nil
}
