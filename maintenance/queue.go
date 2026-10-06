package maintenance

// orderQueue 为待派工单优先队列：等级高者优先，同级提交早者优先，
// 再同按工单序号小者优先。
type orderQueue struct {
	items []*order
}

func (q *orderQueue) len() int { return len(q.items) }

func (q *orderQueue) push(o *order) {
	// 同一工单（如累计拒单升级、等级变化导致堆键失效）只能有一个堆条目：
	// 已存在则重新定位，绝不追加重复指针。
	for i, existing := range q.items {
		if existing == o {
			q.relocate(i)
			return
		}
	}
	q.items = append(q.items, o)
	q.up(len(q.items) - 1)
}

// relocate 在条目位置 i 的优先级可能变化后恢复堆序：先尝试上浮，再下沉。
func (q *orderQueue) relocate(i int) {
	q.up(i)
	q.down(i)
}

func (q *orderQueue) pop() *order {
	n := len(q.items)
	if n == 0 {
		return nil
	}
	top := q.items[0]
	last := q.items[n-1]
	q.items = q.items[:n-1]
	if n > 1 {
		q.items[0] = last
		q.down(0)
	}
	return top
}

func (q *orderQueue) less(i, j int) bool { return ahead(q.items[i], q.items[j]) }

func (q *orderQueue) swap(i, j int) { q.items[i], q.items[j] = q.items[j], q.items[i] }

func (q *orderQueue) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !q.less(i, parent) {
			return
		}
		q.swap(i, parent)
		i = parent
	}
}

func (q *orderQueue) down(i int) {
	n := len(q.items)
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		best := left
		if right := left + 1; right < n && q.less(right, left) {
			best = right
		}
		if !q.less(best, i) {
			return
		}
		q.swap(i, best)
		i = best
	}
}

// ahead 判断 a 是否应排在 b 之前。
func ahead(a, b *order) bool {
	if a.level != b.level {
		return a.level > b.level
	}
	if a.submittedAt != b.submittedAt {
		return a.submittedAt < b.submittedAt
	}
	return a.id < b.id
}
