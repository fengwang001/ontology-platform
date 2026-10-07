package scheduler

// seg 是队列与在途列表中存放的内部段。
type seg struct {
	seq      int64
	len      int64
	excluded string // 重新注入段被排除的子流标识；新数据段为空
}

// segQueue 是段的先进先出队列，push/popFront/front 均为均摊 O(1)。
// 底层为带头部游标的切片，头部空洞过大时压缩。
type segQueue struct {
	buf  []seg
	head int
}

func (q *segQueue) len() int { return len(q.buf) - q.head }

func (q *segQueue) front() (seg, bool) {
	if q.len() == 0 {
		return seg{}, false
	}
	return q.buf[q.head], true
}

func (q *segQueue) push(s seg) {
	q.buf = append(q.buf, s)
}

func (q *segQueue) popFront() {
	q.head++
	if q.head == len(q.buf) {
		q.buf = q.buf[:0]
		q.head = 0
	} else if q.head >= 64 && q.head*2 >= len(q.buf) {
		copy(q.buf, q.buf[q.head:])
		q.buf = q.buf[:len(q.buf)-q.head]
		q.head = 0
	}
}

// at 返回从头起第 i 个元素（0 基）。
func (q *segQueue) at(i int) seg { return q.buf[q.head+i] }

func (q *segQueue) clear() {
	q.buf = q.buf[:0]
	q.head = 0
}

// snapshot 导出全部元素的副本。
func (q *segQueue) snapshot() []Seg {
	n := q.len()
	out := make([]Seg, 0, n)
	for i := 0; i < n; i++ {
		s := q.at(i)
		out = append(out, Seg{Seq: s.seq, Len: s.len, Excluded: s.excluded})
	}
	return out
}
