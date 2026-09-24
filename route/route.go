// Package route 负责专属分片分配、迁移（触发时机与触发事件归属）与二次路由。
// 依赖 shard。
package route

import "ontology/shard"

// Router 在 shard.Engine 之上维护专属分片分配。非并发安全，由上层加锁。
type Router struct {
	eng *shard.Engine
	// dedicated 记录已迁移键的专属分片号。
	dedicated map[string]int
	// dedicatedCount 是已分配专属分片数；下一个专属分片号 = S + dedicatedCount。
	dedicatedCount int
}

// New 构造路由器。
func New(eng *shard.Engine) *Router {
	return &Router{eng: eng, dedicated: make(map[string]int)}
}

// Feed 处理一个事件：先按当前热点状态定落点并计数，再判定是否触发迁移。
// 迁移原子性由此顺序保证：触发迁移的那个事件落在基础分片，
// 从下一个该键事件起才落专属分片；每个事件恰好计数一次。
func (r *Router) Feed(e shard.Event) {
	if r.eng.Migrated(e.Key) {
		r.eng.Add(r.dedicated[e.Key])
		return
	}
	r.eng.Add(e.Base)
	if r.eng.ObserveBase(e.Key) {
		r.eng.MarkHot(e.Key)
		r.dedicated[e.Key] = r.eng.NewShard() // = S + dedicatedCount
		r.dedicatedCount++
	}
}

// Dedicated 返回键的专属分片号；未迁移时 ok 为 false。
func (r *Router) Dedicated(key string) (int, bool) {
	s, ok := r.dedicated[key]
	return s, ok
}

// DedicatedCount 返回已分配的专属分片数。
func (r *Router) DedicatedCount() int { return r.dedicatedCount }
