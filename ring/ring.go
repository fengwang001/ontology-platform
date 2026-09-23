// Package ring 是对外的多读者环形缓冲区：单写追加、多读者独立游标、覆盖与掉队。
package ring

import (
	"errors"
	"sync"

	"ontology/cursor"
	"ontology/lagstat"
	"ontology/slotring"
)

var (
	// ErrInvalidCapacity 表示容量为 0 或负数。
	ErrInvalidCapacity = errors.New("ring: capacity must be positive")
	// ErrInvalidReaderLimit 表示读者数上限为 0 或负数。
	ErrInvalidReaderLimit = errors.New("ring: reader limit must be positive")
	// ErrTooManyReaders 表示活跃读者数已达上限，注册被拒绝。
	ErrTooManyReaders = errors.New("ring: reader limit reached")
	// ErrReaderClosed 表示用已注销读者的句柄读取或恢复。
	ErrReaderClosed = errors.New("ring: reader is unregistered")
	// ErrNeverRegistered 表示用从未在本缓冲区注册过的句柄。
	ErrNeverRegistered = errors.New("ring: reader was never registered")
	// ErrNoNewRecord 表示游标已追上写入者，暂时没有新记录。
	ErrNoNewRecord = errors.New("ring: no new record")
)

// Stats 是缓冲区的观测快照。
type Stats struct {
	Capacity    int
	Live        int64
	Oldest      int64
	Head        int64
	Readers     int
	SlowestLag  int64
	Behind      int
	Overwritten int64
}

// Buffer 是多读者定容环形缓冲区。
type Buffer struct {
	mu      sync.Mutex
	cap     int
	limit   int
	slots   *slotring.Ring
	next    int64
	live    int64
	stat    *lagstat.Tracker
	readers map[*cursor.Cursor]struct{}

	overwritten int64
}

// New 创建容量为 capacity、活跃读者上限为 readerLimit 的缓冲区。
func New(capacity, readerLimit int) (*Buffer, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if readerLimit <= 0 {
		return nil, ErrInvalidReaderLimit
	}
	return &Buffer{
		cap:     capacity,
		limit:   readerLimit,
		slots:   slotring.New(capacity),
		next:    1,
		stat:    lagstat.New(0),
		readers: make(map[*cursor.Cursor]struct{}),
	}, nil
}

// Capacity 返回缓冲区容量。
func (b *Buffer) Capacity() int { return b.cap }

// Append 追加一条记录，返回该记录全局唯一、严格递增的序号。
// 环满时覆盖最旧记录；持有着该序号的读者被立即标记掉队。
func (b *Buffer) Append(v any) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	seq := b.next
	b.next++
	b.slots.Put(seq, v)
	if b.live < int64(b.cap) {
		b.live++
	} else {
		b.overwritten++
		victim := seq - int64(b.cap)
		for c := range b.readers {
			if c.Status() == cursor.Active && c.Want() <= victim {
				c.MarkBehind()
				b.stat.MarkBehind(c)
			}
		}
	}
	b.stat.SetHead(seq)
	return seq
}
