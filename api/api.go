// Package api 是事件总线对外门面：构造、广播、单订阅者消费、计数与自检。
// 依赖方向 api → bus → fanout，反向依赖一律不允许。
package api

import "ontology/bus"

// 三类可判定哨兵错误（与 bus 哨兵为同一值），互不相同：
// N 非法、C 非法、订阅者编号越界（越界以 panic 承载，recover 后 errors.Is 判定）。
var (
	ErrInvalidN        = bus.ErrInvalidN
	ErrInvalidC        = bus.ErrInvalidC
	ErrSubscriberIndex = bus.ErrSubscriberIndex
)

// EventBus 是并发安全的进程内事件总线。
type EventBus struct{ b *bus.Bus }

// New 创建 N 个订阅者、每订阅者容量 C 的总线。N<=0 / C<=0 整体失败、不留状态。
func New(n, c int) (*EventBus, error) {
	bb, err := bus.New(n, c)
	if err != nil {
		return nil, err
	}
	return &EventBus{bb}, nil
}

// Publish 把 ev 广播给所有订阅者；满则只 tail-drop 该订阅者那份。永不阻塞、不返回错误。
func (e *EventBus) Publish(ev int64) { e.b.Publish(ev) }

// Consume 弹出 si 队首事件；空队列 ok=false；si 越界 panic(ErrSubscriberIndex)。
func (e *EventBus) Consume(si int) (int64, bool) { return e.b.Consume(si) }

// DropCount 返回 si 累计 tail-drop 数；si 越界 panic(ErrSubscriberIndex)。
func (e *EventBus) DropCount(si int) int { return e.b.DropCount(si) }

// QueueLen 返回 si 当前队列长度；si 越界 panic(ErrSubscriberIndex)。
func (e *EventBus) QueueLen(si int) int { return e.b.QueueLen(si) }

// SelfCheck 重放内置操作序列核验四条不变量，全部成立返回 true。
func (e *EventBus) SelfCheck() bool { return e.b.SelfCheck() }
