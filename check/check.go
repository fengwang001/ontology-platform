// Package check 提供朴素参照实现：每次 Dequeue 按注册序线性扫描全部 flow。
package check

import "ontology/flowq"

type nf struct {
	q                flowq.Queue
	quantum, deficit int
	sent, enqueued   int64
}

// Naive 不维护活动表，每次 Dequeue 线性扫描全部已注册 flow，跳过空 flow。
type Naive struct {
	flows map[int]*nf
	order []int
	last  int // 上次访问位置，-1 表示尚无
}

func NewNaive() *Naive { return &Naive{flows: map[int]*nf{}, last: -1} }

func (n *Naive) AddFlow(id, quantum int) {
	if _, ok := n.flows[id]; !ok {
		n.flows[id] = &nf{quantum: quantum}
		n.order = append(n.order, id)
	}
}

func (n *Naive) Enqueue(id, size int) {
	f := n.flows[id]
	f.q.Push(size)
	f.enqueued += int64(size)
}

// EnqueueIf 仅在 ok 为真时入队，用于与 drr 的错误判定保持同步。
func (n *Naive) EnqueueIf(ok bool, id, size int) {
	if ok {
		n.Enqueue(id, size)
	}
}

func (n *Naive) Dequeue() (id, size int, ok bool) {
	nonempty := 0
	for _, f := range n.flows {
		if f.q.Len() > 0 {
			nonempty++
		}
	}
	for visited := 0; visited < nonempty; visited++ {
		var f *nf
		for i := 0; i < len(n.order); i++ {
			n.last = (n.last + 1) % len(n.order)
			if c := n.flows[n.order[n.last]]; c.q.Len() > 0 {
				f = c
				break
			}
		}
		f.deficit += f.quantum
		for head, has := f.q.Front(); has && head <= f.deficit; head, has = f.q.Front() {
			head, _ = f.q.Pop()
			f.deficit, size, f.sent = f.deficit-head, size+head, f.sent+int64(head)
		}
		if f.q.Len() == 0 {
			f.deficit = 0
		}
		if size > 0 {
			return n.order[n.last], size, true
		}
	}
	return 0, 0, false
}

func (n *Naive) Sent(id int) int64 { return n.flows[id].sent }
