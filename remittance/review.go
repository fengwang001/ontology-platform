package remittance

// expiryQueue 为待审核汇款的过期队列。
// 提交的 now 单调不减，故截止时刻 deadline=submitNow+R 亦单调不减，
// 用普通队列即可，每条记录入队/出队各一次，摊还 O(1)。
type expiryQueue struct {
	items []expiryItem
	head  int
}

type expiryItem struct {
	deadline int64
	id       string
}

// push 追加一条待审核记录。
func (q *expiryQueue) push(deadline int64, id string) {
	q.items = append(q.items, expiryItem{deadline: deadline, id: id})
}

// popExpired 弹出全部 deadline < now（即在 now 已逾期）的记录。
func (q *expiryQueue) popExpired(now int64, yield func(id string)) {
	for q.head < len(q.items) && q.items[q.head].deadline < now {
		yield(q.items[q.head].id)
		q.head++
	}
	if q.head >= 64 && q.head*2 >= len(q.items) {
		q.items = append([]expiryItem(nil), q.items[q.head:]...)
		q.head = 0
	}
}

// remittance 为一条汇款记录。
type remittance struct {
	id        string
	remitter  string
	payee     string
	quoteID   string
	srcAmount int64
	rate      int64
	target    int64
	hold      int64
	day       int64
	submitNow int64
	deadline  int64
	status    Status
}
