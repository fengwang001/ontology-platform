package usage

import (
	"container/list"
	"sort"
)

// consumer 记录单个消费者对单个数据集的迁移状态。
type consumer struct {
	name       string
	lastAccess int64 // 最近一次被放行的访问时刻
	ackedAt    int64 // 最近一次 Ack 的时刻
	acked      bool  // 是否曾确认（lastAccess 可能恰为 0，不能用 ackedAt==0 判别）
	element    *list.Element
}

// datasetUsers 按 lastAccess 升序维护该数据集的消费者。
type datasetUsers struct {
	order   *list.List // *consumer，按 lastAccess 升序
	byName  map[string]*consumer
	scanned int // 上一次 ActiveConsumers 枚举过的消费者数（非导出计数器）
}

// Tracker 管理全部数据集的消费者访问登记。非并发安全，由 sunset 加锁调用。
type Tracker struct {
	users map[string]*datasetUsers
}

// New 创建空的访问登记器。
func New() *Tracker { return &Tracker{users: make(map[string]*datasetUsers)} }

func (t *Tracker) forDataset(d string) *datasetUsers {
	u, ok := t.users[d]
	if !ok {
		u = &datasetUsers{order: list.New(), byName: make(map[string]*consumer)}
		t.users[d] = u
	}
	return u
}

// Record 记录 consumer 在 now 对 d 的一次成功（被放行）访问。
// 成功访问会使之前的 Ack 作废：判定活跃时 ackedAt < lastAccess 即视为未迁移。
func (t *Tracker) Record(consumerName, d string, now int64) {
	u := t.forDataset(d)
	if c, ok := u.byName[consumerName]; ok {
		c.lastAccess = now
		u.order.MoveToBack(c.element)
		return
	}
	c := &consumer{name: consumerName, lastAccess: now}
	c.element = u.order.PushBack(c)
	u.byName[consumerName] = c
}

// HasAccessed 报告 consumer 是否对 d 有过被放行的访问。
func (t *Tracker) HasAccessed(consumerName, d string) bool {
	u, ok := t.users[d]
	if !ok {
		return false
	}
	_, ok = u.byName[consumerName]
	return ok
}

// Ack 登记 consumer 在 now 对 d 的迁移确认。
func (t *Tracker) Ack(consumerName, d string, now int64) {
	u := t.forDataset(d)
	c, ok := u.byName[consumerName]
	if !ok {
		c = &consumer{name: consumerName}
		c.element = u.order.PushBack(c)
		u.byName[consumerName] = c
	}
	c.acked = true
	c.ackedAt = now
}

// ActiveConsumers 返回 t=now、活跃判定期为 q 时 d 的全部活跃消费者（字节序升序）。
// 消费者活跃当且仅当 lastAccess > now-q，且（未确认 或 最后一次确认早于最后一次成功访问）。
func (t *Tracker) ActiveConsumers(d string, now, q int64) []string {
	u, ok := t.users[d]
	if !ok {
		return nil
	}
	cutoff := now - q
	u.scanned = 0
	var active []string
	for e := u.order.Back(); e != nil; e = e.Prev() {
		u.scanned++
		c := e.Value.(*consumer)
		if c.lastAccess <= cutoff {
			break
		}
		if !c.acked || c.ackedAt < c.lastAccess {
			active = append(active, c.name)
		}
	}
	sort.Strings(active)
	return active
}

// Scanned 返回该数据集上一次 ActiveConsumers 考察的消费者数。
func (t *Tracker) Scanned(d string) int {
	u, ok := t.users[d]
	if !ok {
		return 0
	}
	return u.scanned
}
