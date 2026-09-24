package segment

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ontology/event"
)

// Writer 是单段文件的追加写者。每次追加后改写段头事件数并 Sync，
// 使段头声明始终等于已完整落盘的记录数（一致前缀）。
type Writer struct {
	mu     sync.Mutex
	f      *os.File
	hdr    Header
	dataSz int64 // 数据区字节数（不含段头）
}

// Create 以 firstSeq 为段首序号创建新段文件并写入段头。
func Create(dir string, firstSeq uint64) (*Writer, error) {
	path := filepath.Join(dir, fmt.Sprintf("seg-%012d.log", firstSeq))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, hdr: Header{FirstSeq: firstSeq}}
	var hbuf [headerLen]byte
	encodeHeader(hbuf[:], w.hdr)
	if _, err := f.Write(hbuf[:]); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// Append 追加一条事件并提交（更新段头计数 + fsync）。
func (w *Writer) Append(ev event.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	wantSeq := w.hdr.FirstSeq + uint64(w.hdr.Count)
	if ev.Seq != wantSeq {
		return fmt.Errorf("segment: non-contiguous append seq=%d want=%d",
			ev.Seq, wantSeq)
	}
	rec := encodeFrame(ev)
	if _, err := w.f.Write(rec); err != nil {
		return err
	}
	w.dataSz += int64(len(rec))
	w.hdr.Count++
	var hbuf [headerLen]byte
	encodeHeader(hbuf[:], w.hdr)
	if _, err := w.f.WriteAt(hbuf[:], 0); err != nil {
		return err
	}
	return w.f.Sync()
}

// FirstSeq 返回段首序号。
func (w *Writer) FirstSeq() uint64 { return w.hdr.FirstSeq }

// Count 返回已提交事件数。
func (w *Writer) Count() uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hdr.Count
}

// DataSize 返回数据区字节数（供上层决定是否滚动新段）。
func (w *Writer) DataSize() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dataSz
}

// Close 关闭段文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
