package gc

import (
	"sort"
	"time"
)

// settle 把级联收敛到唯一稳定状态：反复应用
// 「前台传播」与「可移除即移除」两条规则，直到都不再产生变化。
// 规则是单调的（对象只会 存活->删除中->移除，策略只会 后台->前台 升级），
// 因此收敛结果与触发先后无关。调用方必须持有锁。
func (c *Controller) settle(now time.Time) {
	for len(c.propQ) > 0 || len(c.remQ) > 0 {
		for len(c.propQ) > 0 {
			d := c.propQ[len(c.propQ)-1]
			c.propQ = c.propQ[:len(c.propQ)-1]
			c.steps++
			if c.objects[d.id] != d {
				continue // 已被移除的过期引用
			}
			// 前台传播：所有属主均不存在或处于删除中，
			// 且至少一个属主处于前台删除中。
			if d.liveOwners == 0 && d.fgOwners > 0 {
				c.applyDeleteRequest(d, Foreground, now)
			}
		}
		for len(c.remQ) > 0 {
			o := c.remQ[len(c.remQ)-1]
			c.remQ = c.remQ[:len(c.remQ)-1]
			c.steps++
			if c.objects[o.id] != o {
				continue
			}
			c.tryRemove(o, now)
		}
	}
}

// applyDeleteRequest 实现「删除请求」的统一语义：
// 对象未删除时打上删除中标记并记录策略与请求时刻；
// 已删除时是幂等成功，仅「后台 -> 前台」会升级策略，其余组合不变。
// 删除 API、前台传播与属主移除后的连带删除都走这里。
func (c *Controller) applyDeleteRequest(o *object, policy Policy, now time.Time) {
	if !o.deleting {
		o.deleting = true
		o.policy = policy
		o.reqTime = now
		c.onBecomeDeleting(o)
		return
	}
	if o.policy == Background && policy == Foreground {
		o.policy = Foreground // 升级，请求时刻保持不变
		c.onBecomeForeground(o)
	}
}

// onBecomeDeleting 维护计数器并登记后续检查。
func (c *Controller) onBecomeDeleting(o *object) {
	// o 不再存活：其阻塞型引用不再阻塞各属主的前台删除。
	for ownerID, block := range o.owners {
		c.steps++
		if block {
			if owner, ok := c.objects[ownerID]; ok {
				owner.blockingDeps--
				c.remQ = append(c.remQ, owner)
			}
		}
	}
	// o 进入删除中：其依赖者的存活属主计数减一，
	// 若 o 是前台删除，依赖者的前台属主计数加一。
	for _, depID := range c.sortedDependents(o.id) {
		c.steps++
		d := c.objects[depID]
		d.liveOwners--
		if o.policy == Foreground {
			d.fgOwners++
		}
		c.propQ = append(c.propQ, d)
	}
	c.remQ = append(c.remQ, o)
}

// onBecomeForeground 处理「后台升级前台」的增量影响。
func (c *Controller) onBecomeForeground(o *object) {
	for _, depID := range c.sortedDependents(o.id) {
		c.steps++
		d := c.objects[depID]
		d.fgOwners++
		c.propQ = append(c.propQ, d)
	}
	c.remQ = append(c.remQ, o)
}

// tryRemove 在移除条件满足时立即移除对象，并按策略处理其依赖者。
func (c *Controller) tryRemove(o *object, now time.Time) {
	if !o.deleting || len(o.finalizers) > 0 {
		return
	}
	if o.policy == Foreground && o.blockingDeps > 0 {
		return // 前台删除须等待阻塞型存活依赖者
	}
	// 摘除每个依赖者指向 o 的引用。
	for _, depID := range c.sortedDependents(o.id) {
		c.steps++
		d := c.objects[depID]
		delete(d.owners, o.id)
		if o.policy == Foreground {
			d.fgOwners--
		}
		c.removeDependent(o.id, depID)
		if o.policy != Orphan && len(d.owners) == 0 {
			// 原本有属主而现在一个也不剩：连带后台删除。
			c.applyDeleteRequest(d, Background, now)
		}
	}
	// 从 o 自身属主的反向索引中摘除（o 进入删除中时
	// 已把这些属主的 blockingDeps 减过，此处只清理索引）。
	for ownerID := range o.owners {
		c.steps++
		c.removeDependent(ownerID, o.id)
	}
	delete(c.objects, o.id)
}

// sortedDependents 返回依赖者 ID 的稳定排序快照，保证行为可复现。
func (c *Controller) sortedDependents(ownerID string) []string {
	set := c.dependents[ownerID]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
