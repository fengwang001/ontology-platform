package segment

import (
	"io"
	"os"
	"sync"

	"ontology/event"
)

// Writer 向单个段文件只追加事件。并发安全。
type Writer struct {
	mu       sync.Mutex
	f        *os.File
	firstSeq uint64
	count    uint64
	offset   int64 // 已写数据区字节数（头之后）
	closed   bool
}

// Create 新建段文件（文件必须不存在），firstSeq 为本段首序号。
func Create(path string, firstSeq uint64) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(encodeHeader(Header{FirstSeq: firstSeq})); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, firstSeq: firstSeq}, nil
}

// Append 追加一条事件并落盘，返回该记录在数据区中的起始偏移。
func (w *Writer) Append(ev event.Event) (offset int64, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	offset = w.offset
	if _, err = w.f.Write(event.EncodeFrame(nil, ev.Payload)); err != nil {
		return 0, err
	}
	if err = w.f.Sync(); err != nil {
		return 0, err
	}
	w.count++
	w.offset += int64(event.FrameLen(len(ev.Payload)))
	return offset, nil
}

// Count 返回已追加的事件数。
func (w *Writer) Count() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

// FirstSeq 返回本段首序号。
func (w *Writer) FirstSeq() uint64 { return w.firstSeq }

// Close 把实际条数写回段头 count 并关闭文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if _, err := w.f.WriteAt(encodeHeader(Header{FirstSeq: w.firstSeq, Count: w.count})[13:21], 13); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

var _ io.Writer = (*os.File)(nil)
