package ring

import (
	"ontology/cursor"
)

// Register 注册一个新读者：它从「下一条」开始，只看到注册之后追加的记录。
// 达到上限时返回 ErrTooManyReaders，且不留下任何半注册状态。
func (b *Buffer) Register() (*cursor.Cursor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.readers) >= b.limit {
		return nil, ErrTooManyReaders
	}
	c := cursor.New(b.next)
	b.readers[c] = struct{}{}
	b.stat.Add(c)
	return c, nil
}

// Unregister 注销读者并立即把它从统计中剔除、重算最慢落后量。
func (b *Buffer) Unregister(c *cursor.Cursor) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, known := b.readers[c]
	if !known {
		if c != nil && c.Status() == cursor.Closed {
			return ErrReaderClosed
		}
		return ErrNeverRegistered
	}
	behind := c.Status() == cursor.Behind
	b.stat.Remove(c, behind)
	delete(b.readers, c)
	c.Close()
	return nil
}

func (b *Buffer) oldest() (int64, bool) {
	if b.live == 0 {
		return 0, false
	}
	return b.next - 1 - b.live + 1, true
}

// Read 读取读者游标指向的下一条记录，返回其序号与内容并推进游标。
// 掉队时返回 *cursor.FellBehindError（指针类型），携带错过条数。
func (b *Buffer) Read(c *cursor.Cursor) (int64, any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if c == nil {
		return 0, nil, ErrNeverRegistered
	}
	if _, known := b.readers[c]; !known {
		if c.Status() == cursor.Closed {
			return 0, nil, ErrReaderClosed
		}
		return 0, nil, ErrNeverRegistered
	}
	head := b.next - 1
	old, hasOld := b.oldest()
	if c.Status() == cursor.Behind || (hasOld && c.IsBehind(old)) {
		if c.Status() == cursor.Active {
			c.MarkBehind()
			b.stat.MarkBehind(c)
		}
		return 0, nil, &cursor.FellBehindError{Missed: old - c.Want()}
	}
	if c.Want() > head {
		return 0, nil, ErrNoNewRecord
	}
	seq := c.Want()
	v, ok := b.slots.Get(seq)
	if !ok {
		c.MarkBehind()
		b.stat.MarkBehind(c)
		return 0, nil, &cursor.FellBehindError{Missed: old - c.Want()}
	}
	c.Advance(seq)
	b.stat.Update(c)
	return seq, v, nil
}

// Recover 让掉队（或超前）读者重新定位到当前最旧可读记录；返回该序号。
// 恢复后第一次 Read 读到的正是这条最旧记录，序号从此连续。
func (b *Buffer) Recover(c *cursor.Cursor) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if c == nil {
		return 0, ErrNeverRegistered
	}
	if _, known := b.readers[c]; !known {
		if c.Status() == cursor.Closed {
			return 0, ErrReaderClosed
		}
		return 0, ErrNeverRegistered
	}
	old, ok := b.oldest()
	if !ok {
		old = b.next
	}
	wasBehind := c.Status() == cursor.Behind
	c.Recover(old)
	if wasBehind {
		b.stat.Reactivate(c)
	} else {
		b.stat.Update(c)
	}
	return old, nil
}
