package segment

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"

	"ontology/event"
)

// Writer 向单个段追加事件。
type Writer struct {
	mu        sync.Mutex
	f         *os.File
	firstSeq  uint64
	count     uint64
	offset    int64
	committed bool
}

var crcTable = crc32.MakeTable(crc32.IEEE)

func encodeHeader(h Header) []byte {
	b := make([]byte, HeaderSize)
	copy(b[0:4], magic)
	binary.LittleEndian.PutUint16(b[4:6], version)
	binary.LittleEndian.PutUint64(b[8:16], h.FirstSeq)
	binary.LittleEndian.PutUint64(b[16:24], h.Count)
	return b
}

// Create 在 dir 下创建编号 index 的新段，首序号为 firstSeq。
func Create(dir string, index int, firstSeq uint64) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, fmt.Sprintf("seg-%06d.log", index))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	h := Header{FirstSeq: firstSeq}
	if _, err := f.Write(encodeHeader(h)); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, firstSeq: firstSeq, offset: HeaderSize}, nil
}

// Append 追加一条事件并返回其记录起始偏移。序号必须严格递增。
func (w *Writer) Append(e event.Event) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.count > 0 && e.Seq != w.firstSeq+w.count {
		return 0, fmt.Errorf("segment: non-contiguous seq %d want %d", e.Seq, w.firstSeq+w.count)
	}
	if w.count == 0 && e.Seq != w.firstSeq {
		return 0, fmt.Errorf("segment: first seq %d want %d", e.Seq, w.firstSeq)
	}
	body := e.AppendTo(make([]byte, 0, e.Size()))
	rec := make([]byte, 4+len(body)+4)
	binary.LittleEndian.PutUint32(rec[0:4], uint32(len(body)))
	copy(rec[4:], body)
	csum := crc32.Checksum(rec[:4+len(body)], crcTable)
	binary.LittleEndian.PutUint32(rec[4+len(body):], csum)
	start := w.offset
	if _, err := w.f.Write(rec); err != nil {
		return 0, err
	}
	w.count++
	w.offset += int64(len(rec))
	b := encodeHeader(Header{FirstSeq: w.firstSeq, Count: w.count})
	if _, err := w.f.WriteAt(b[16:24], 16); err != nil {
		return 0, err
	}
	return start, nil
}

// Close 关闭段文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Sync 强制把已提交内容落盘。
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	return w.f.Sync()
}
