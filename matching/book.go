package matching

// batch 是价位显示队列上的一个显示批。
// 普通委托全量占一个批；冰山委托当前显示批占一个批，批次吃完后补新批排队尾。
type batch struct {
	order *order
	left  int64 // 该显示批剩余可成交量
	prev  *batch
	next  *batch
}

// hiddenNode 是价位上一个隐藏委托的排队节点，按序号排序（用链表天然保持插入序）。
type hiddenNode struct {
	order *order
	prev  *hiddenNode
	next  *hiddenNode
}

// level 是一个价位上的全部簿内容。
type level struct {
	price int64

	visHead *batch
	visTail *batch
	visSize int64 // 显示批剩余量汇总，即公开显示总量

	hiddenHead *hiddenNode
	hiddenTail *hiddenNode

	// treap 树结构
	priority uint64
	left     *level
	right    *level
}

func (l *level) empty() bool {
	return l.visHead == nil && l.hiddenHead == nil
}

// ---- 显示批链表：O(1) 头取 / 尾插 / 定点移除 ----

func (l *level) pushTailBatch(b *batch) {
	b.prev = l.visTail
	b.next = nil
	if l.visTail != nil {
		l.visTail.next = b
	} else {
		l.visHead = b
	}
	l.visTail = b
	l.visSize += b.left
}

func (l *level) removeBatch(b *batch) {
	if b.prev != nil {
		b.prev.next = b.next
	} else {
		l.visHead = b.next
	}
	if b.next != nil {
		b.next.prev = b.prev
	} else {
		l.visTail = b.prev
	}
	b.prev, b.next = nil, nil
	l.visSize -= b.left
}

// headBatch 取出队头批；冰山吃完后由调用方负责补批。
func (l *level) headBatch() *batch { return l.visHead }

// ---- 隐藏委托链表：按序号（插入序）排队，O(1) 尾插 / 定点移除 ----

func (l *level) pushTailHidden(h *hiddenNode) {
	h.prev = l.hiddenTail
	h.next = nil
	if l.hiddenTail != nil {
		l.hiddenTail.next = h
	} else {
		l.hiddenHead = h
	}
	l.hiddenTail = h
}

func (l *level) removeHidden(h *hiddenNode) {
	if h.prev != nil {
		h.prev.next = h.next
	} else {
		l.hiddenHead = h.next
	}
	if h.next != nil {
		h.next.prev = h.prev
	} else {
		l.hiddenTail = h.prev
	}
	h.prev, h.next = nil, nil
}
