package segment

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ontology/event"
	"ontology/sparse"
)

// SegmentName 返回第 i 个段的文件名。
func SegmentName(i int) string { return fmt.Sprintf("seg-%06d.osl", i) }

// IndexPath 返回段文件对应的索引文件路径。
func IndexPath(segPath string) string { return strings.TrimSuffix(segPath, ".osl") + ".osi" }

// ListSegments 按序号升序列出目录中的全部段文件路径。
func ListSegments(dir string) ([]string, error) {
	names, err := filepath.Glob(filepath.Join(dir, "seg-*.osl"))
	if err != nil {
		return nil, err
	}
	return names, nil // Glob 结果已按字典序排列，零填充命名下即序号序
}

// Writer 向目录追加事件，写满 maxEvents 条自动轮转新段；
// 每 every 条记录一个索引锚点，段定稿时落盘索引。
// 写者协议：先写完整记录，再原地更新段头 count，保证读者只见一致前缀。
type Writer struct {
	dir       string
	maxEvents int
	every     int
	f         *os.File
	seg       int
	firstSeq  uint64
	count     uint64
	off       int64
	nextSeq   uint64
	anchors   []sparse.Anchor
	closed    bool
}

// NewWriter 在 dir 下创建从序号 0 开始的空日志。
func NewWriter(dir string, maxEvents, every int) (*Writer, error) {
	if maxEvents <= 0 || every <= 0 {
		return nil, fmt.Errorf("segment: maxEvents and every must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, maxEvents: maxEvents, every: every}
	if err := w.openSegment(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) openSegment() error {
	f, err := os.Create(filepath.Join(w.dir, SegmentName(w.seg)))
	if err != nil {
		return err
	}
	w.f = f
	w.firstSeq = w.nextSeq
	w.count = 0
	w.off = HeaderSize
	w.anchors = nil
	if _, err = w.f.WriteAt(EncodeHeader(Header{FirstSeq: w.firstSeq}), 0); err != nil {
		return err
	}
	_, err = w.f.Seek(HeaderSize, 0) // WriteAt 不移动文件偏移，记录须从头之后顺序写
	return err
}

// Append 追加一条事件，返回分配给它的序号。
func (w *Writer) Append(payload []byte) (uint64, error) {
	if w.closed {
		return 0, fmt.Errorf("segment: write on closed writer")
	}
	if w.count == uint64(w.maxEvents) {
		if err := w.finalize(); err != nil {
			return 0, err
		}
		w.seg++
		if err := w.openSegment(); err != nil {
			return 0, err
		}
	}
	ev := event.Event{Seq: w.nextSeq, Payload: payload}
	rec := EncodeRecord(ev)
	if w.count%uint64(w.every) == 0 {
		w.anchors = append(w.anchors, sparse.Anchor{Seq: ev.Seq, Offset: w.off})
	}
	if _, err := w.f.Write(rec); err != nil {
		return 0, err
	}
	w.off += int64(len(rec))
	w.count++
	w.nextSeq++
	// 记录已完整落盘（同一进程内写有序），最后更新段头 count。
	_, err := w.f.WriteAt(EncodeHeader(Header{FirstSeq: w.firstSeq, Count: w.count}), 0)
	return ev.Seq, err
}

// finalize 落盘索引并关闭当前段文件。
func (w *Writer) finalize() error {
	if w.f == nil {
		return nil
	}
	idx := sparse.Index{Every: w.every, Anchors: w.anchors}
	if err := sparse.WriteFile(IndexPath(w.f.Name()), idx); err != nil {
		return err
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Close 定稿当前段并关闭写入器。
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.finalize()
}
