package mirror

import "sort"

// dirtyLog 是单个非在线成员的脏区记录：登记其缺失的块。
// 用哈希集合存储，登记与查询都是 O(1)，与卷的总块数无关。
type dirtyLog struct {
	limit   int              // 脏区上限（块数）
	set     map[int]struct{} // 缺失块集合
	dropped bool             // 记录已因超限被丢弃，必须全量重同步
}

func newDirtyLog(limit int) *dirtyLog {
	return &dirtyLog{limit: limit, set: make(map[int]struct{})}
}

// add 登记一个缺失块。登记后不同块数严格大于上限时，
// 丢弃整份记录并置 dropped，直到下一次完成重同步。
func (l *dirtyLog) add(block int) {
	if l.dropped {
		return
	}
	if _, ok := l.set[block]; ok {
		return
	}
	if len(l.set)+1 > l.limit {
		l.set = make(map[int]struct{})
		l.dropped = true
		return
	}
	l.set[block] = struct{}{}
}

// sorted 按块号升序返回缺失块，开销只随脏区内块数增长。
func (l *dirtyLog) sorted() []int {
	blocks := make([]int, 0, len(l.set))
	for b := range l.set {
		blocks = append(blocks, b)
	}
	sort.Ints(blocks)
	return blocks
}
