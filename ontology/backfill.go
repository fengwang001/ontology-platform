package ontology

// pendingBackfill 是回填队列中的一项，gen 记录入队时的声明修订序号。
type pendingBackfill struct {
	id  string
	gen int64
}

// queue 是需要异步回填的存量实例队列，按入队（内部）顺序逐个处理。
//
// 队列中的项可能已经陈旧：出队时目标实例可能已被并发写入抢先回填，
// 也可能已被删除。陈旧项不提前摘除，而是保留到出队那一刻由 Store 在
// 互斥锁内基于实例当前状态识别并跳过——这样"抢先回填"与"回填期间被
// 删除"两种竞争都能被回填流程显式观察到：不覆盖、不复活、不报错误。
type queue struct {
	items []pendingBackfill
}

func newQueue() *queue {
	return &queue{}
}

// push 将实例追加到队尾。
func (q *queue) push(id string, gen int64) {
	q.items = append(q.items, pendingBackfill{id: id, gen: gen})
}

func (q *queue) len() int { return len(q.items) }

// pop 取出队首一项；陈旧/失效项的识别由调用方在锁内完成。
func (q *queue) pop() (pendingBackfill, bool) {
	if len(q.items) == 0 {
		return pendingBackfill{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}
