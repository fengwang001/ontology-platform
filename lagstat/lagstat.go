// Package lagstat 增量维护各读者落后量：最慢值用「目标序号」最小堆维护。
package lagstat

import "ontology/cursor"

// Tracker 汇总存活读者的落后统计。
type Tracker struct {
	head        int64
	heap        []*entry
	pos         map[*cursor.Cursor]int
	behindCount int
	scanCount   int
}

type entry struct {
	c    *cursor.Cursor
	want int64
}

// New 创建空统计器，head 为当前最新序号。
func New(head int64) *Tracker {
	return &Tracker{head: head, pos: make(map[*cursor.Cursor]int)}
}

// SetHead 同步最新序号（Append 后调用），不触碰堆。
func (t *Tracker) SetHead(head int64) { t.head = head }

func (t *Tracker) less(i, j int) bool { return t.heap[i].want < t.heap[j].want }

func (t *Tracker) swap(i, j int) {
	t.heap[i], t.heap[j] = t.heap[j], t.heap[i]
	t.pos[t.heap[i].c] = i
	t.pos[t.heap[j].c] = j
}

func (t *Tracker) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !t.less(i, p) {
			return
		}
		t.swap(i, p)
		i = p
	}
}

func (t *Tracker) down(i int) {
	for {
		l := 2*i + 1
		if l >= len(t.heap) {
			return
		}
		j := l
		if r := l + 1; r < len(t.heap) && t.less(r, l) {
			j = r
		}
		if !t.less(j, i) {
			return
		}
		t.swap(i, j)
		i = j
	}
}

// Add 注册一个活跃读者。
func (t *Tracker) Add(c *cursor.Cursor) {
	if _, ok := t.pos[c]; ok {
		return
	}
	t.heap = append(t.heap, &entry{c: c, want: c.Want()})
	i := len(t.heap) - 1
	t.pos[c] = i
	t.up(i)
}

// Reactivate 让掉队读者恢复后重新进入活跃堆，并减掉队计数。
func (t *Tracker) Reactivate(c *cursor.Cursor) {
	if t.behindCount > 0 {
		t.behindCount--
	}
	t.Add(c)
}

// Update 在读者 want 变化（读到数据/恢复）后调整堆。
func (t *Tracker) Update(c *cursor.Cursor) {
	i, ok := t.pos[c]
	if !ok {
		return
	}
	t.heap[i].want = c.Want()
	t.up(i)
	t.down(i)
}

// MarkBehind 把读者移出活跃堆并计入掉队数。
func (t *Tracker) MarkBehind(c *cursor.Cursor) {
	i, ok := t.pos[c]
	if !ok {
		return
	}
	delete(t.pos, c)
	last := len(t.heap) - 1
	if i != last {
		t.heap[i] = t.heap[last]
		t.pos[t.heap[i].c] = i
	}
	t.heap = t.heap[:last]
	if i < len(t.heap) {
		t.down(i)
		t.up(i)
	}
	t.behindCount++
}

// Remove 注销读者；掉队读者注销只减掉队计数。
func (t *Tracker) Remove(c *cursor.Cursor, behind bool) {
	if behind {
		if t.behindCount > 0 {
			t.behindCount--
		}
		return
	}
	i, ok := t.pos[c]
	if !ok {
		return
	}
	delete(t.pos, c)
	last := len(t.heap) - 1
	if i != last {
		t.heap[i] = t.heap[last]
		t.pos[t.heap[i].c] = i
	}
	t.heap = t.heap[:last]
	if i < len(t.heap) {
		t.down(i)
		t.up(i)
	}
}

// Slowest 返回最慢活跃读者的落后条数，并记录本查询访问的读者记录数。
func (t *Tracker) Slowest() int64 {
	t.scanCount = 0
	if len(t.heap) == 0 {
		return 0
	}
	t.scanCount = 1
	lag := t.head - t.heap[0].want
	if lag < 0 {
		return 0
	}
	return lag
}

// BehindCount 返回当前处于掉队状态的读者数。
func (t *Tracker) BehindCount() int { return t.behindCount }

// Distribution 返回全部存活（含掉队）游标的落后量，按游标固定遍历顺序。
func (t *Tracker) Distribution(all func() []*cursor.Cursor) []int64 {
	cs := all()
	out := make([]int64, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Lag(t.head))
	}
	return out
}

// scanRecords 是非导出的实测访问记录数，仅供包内测试断言。
func (t *Tracker) scanRecords() int { return t.scanCount }
