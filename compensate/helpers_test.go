package compensate

import (
	"context"
	"strconv"
	"sync/atomic"
)

func testEvent(id, action string, payload map[string]string) Event {
	return Event{EventID: id, ActionType: action, Payload: payload}
}

// countingEffect 是测试用补偿副作用：业务键由事件负载中的 object 动态派生，
// 因而单一 ActionType 的规格可以正确作用于不同对象（注册表不会互相覆盖）。
type countingEffect struct {
	businessKey string // 为空时从 ev.Payload["object"] 派生 "biz/<object>"
	applied     *atomic.Int32
}

func (e countingEffect) bizKey(ev Event) string {
	if e.businessKey != "" {
		return e.businessKey
	}
	return "biz/" + ev.Payload["object"]
}

func (e countingEffect) OwnedKey(ev Event, index int) string {
	return DefaultEffectKey(ev, index)
}

func (e countingEffect) Apply(_ context.Context, txn Txn, ev Event, index int) error {
	marker := e.OwnedKey(ev, index)
	// 用原子条件插入领取本项副作用的独占标记：并发的重复投递/续作中，
	// 只有一个事务能让 CreateIfAbsent 成功并施加业务变更。
	if claimer, ok := txn.(EffectClaimer); ok {
		if !claimer.CreateIfAbsent(marker, []byte("1")) {
			return nil
		}
	} else {
		if _, err := txn.Get(marker); err == nil {
			return nil
		}
		txn.Put(marker, []byte("1"))
	}
	businessKey := e.bizKey(ev)
	cur := []byte{}
	if raw, err := txn.Get(businessKey); err == nil {
		cur = raw
	}
	cur = append(append(cur, "fx"+strconv.Itoa(index)+":"...), ev.EventID...)
	cur = append(cur, ';')
	txn.Put(businessKey, cur)
	return nil
}

// targetWithExtraField：目标对象有「存活标记键」与一个无关的「负载字段键」。
// 无关并发修改只动负载字段，用于验证续作不会因对象被改过就拒绝。
func targetKeys(ev Event) (aliveKey, extraKey string) {
	obj := ev.Payload["object"]
	return "obj/" + obj + "/alive", "obj/" + obj + "/payload"
}

func multiSpec(reg *Registry, action string, n int, businessKey string, counters []atomic.Int32) CompensationSpec {
	effects := make([]Effect, n)
	for i := 0; i < n; i++ {
		effects[i] = countingEffect{businessKey: businessKey, applied: &counters[i]}
	}
	spec := CompensationSpec{
		ActionType: action,
		Effects:    effects,
		TargetExists: func(txn Txn, ev Event) bool {
			alive, _ := targetKeys(ev)
			_, err := txn.Get(alive)
			return err == nil
		},
	}
	reg.Register(spec)
	return spec
}

// dynamicSpec 注册业务键由事件负载 object 派生的规格，适用于同 ActionType
// 作用于多个不同对象的场景（避免注册表按 ActionType 互相覆盖）。
func dynamicSpec(reg *Registry, action string, n int, counters []atomic.Int32) CompensationSpec {
	return multiSpec(reg, action, n, "", counters)
}

func createTarget(store Store, object string) {
	_ = store.Update(func(txn Txn) error {
		txn.Put("obj/"+object+"/alive", []byte("1"))
		txn.Put("obj/"+object+"/payload", []byte("v0"))
		return nil
	})
}

func deleteTarget(store Store, object string) {
	_ = store.Update(func(txn Txn) error {
		txn.Delete("obj/" + object + "/alive")
		return nil
	})
}

func mustHandle(p *Processor, ev Event) HandleResult {
	return p.Handle(context.Background(), ev)
}

func counterSum(xs []atomic.Int32) int {
	n := 0
	for i := range xs {
		n += int(xs[i].Load())
	}
	return n
}

// committedEffectCount 返回某事件某项副作用的独占标记是否已提交（0/1）。
// 这是崩溃/并发下唯一可靠的「是否恰好生效一次」的外部可观察证据。
func committedEffectCount(store *MemoryStore, ev Event, index int) int {
	if _, ok := store.SnapshotGet(DefaultEffectKey(ev, index)); ok {
		return 1
	}
	return 0
}

// committedBusinessOccurrences 统计业务键中某 effect 标记的出现次数。
func committedBusinessOccurrences(store *MemoryStore, businessKey string) map[string]int {
	raw, ok := store.SnapshotGet(businessKey)
	out := map[string]int{}
	if !ok {
		return out
	}
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ';' {
			if i > start {
				out[string(raw[start:i])]++
			}
			start = i + 1
		}
	}
	return out
}

func totalOcc(occ map[string]int) int {
	n := 0
	for _, v := range occ {
		n += v
	}
	return n
}
