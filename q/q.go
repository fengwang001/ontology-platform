// Package q 提供按优先级分组的 FIFO 队列及选最高优先级非空队列的纯操作。
package q

// Queues 按优先级分组的 FIFO 队列集合。
type Queues struct {
	m map[int][]int
}

// New 创建空队列集合。
func New() *Queues { return &Queues{m: map[int][]int{}} }

// Push 把 id 追加到 prio 对应的 FIFO 队列尾部。
func (qs *Queues) Push(prio, id int) {}

// At 返回 prio 队列中下标 pos 的事件 id。
func (qs *Queues) At(prio, pos int) int { return 0 }

// Len 返回 prio 队列的长度。
func (qs *Queues) Len(prio int) int { return 0 }

// Drop 删除 prio 对应的队列（该优先级批次耗尽后调用）。
func (qs *Queues) Drop(prio int) {}

// Highest 返回最高优先级的非空队列的优先级，checked 返回检查过的分档个数。
// 没有非空队列时 ok 为 false。
func (qs *Queues) Highest() (prio, checked int, ok bool) { return 0, 0, false }

// Prios 返回当前非空优先级列表（无序），仅用于状态展示。
func (qs *Queues) Prios() []int { return nil }
