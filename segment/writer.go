package segment

import (
	"bufio"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

// Writer 顺序产出一个段文件：条目必须严格按键升序写入，最后 Finish 落盘稀疏索引与尾部。
type Writer struct {
	path string
	f    *os.File
	w    *bufio.Writer

	off     int64
	count   uint32
	index   []IndexEntry
	lastKey []byte

	tmpSuffix string
}

// NewWriter 在 dir 下创建临时段文件；目标名在 Finish 时原子改名为 name。
func NewWriter(dir, name string) (*Writer, error) {
	f, err := os.CreateTemp(dir, ".segbuild-*")
	if err != nil {
		return nil, err
	}
	w := &Writer{
		path:      filepath.Join(dir, name),
		tmpSuffix: f.Name(),
		f:         f,
		w:         bufio.NewWriterSize(f, 1<<16),
	}
	if _, err := f.Write(make([]byte, headerSize)); err != nil {
	return nil, err
	}
	w.off = headerSize
	return w, nil
}

// TempPath 返回构建中临时文件的完整路径（供故障注入截断）。
func (w *Writer) TempPath() string { return w.tmpSuffix }

func (w *Writer) Append(e Entry) error {
	if e.Key == nil {
		return ErrEmptyKeyOrder
	}
	if w.count > 0 && string(e.Key) <= string(w.lastKey) {
		return ErrEmptyKeyOrder
	}
	if w.count%sparseBlockEntries == 0 {
		w.index = append(w.index, IndexEntry{Key: append([]byte(nil), e.Key...), Offset: w.off})
	}
	buf := make([]byte, entryPrefixSize)
	vlen := uint32(len(e.Value))
	if e.Tomb {
		vlen |= tombBit
	}
	putU32(buf[0:4], uint32(len(e.Key)))
	putU32(buf[4:8], vlen)
	h := crc32.NewIEEE()
	h.Write(buf)
	h.Write(e.Key)
	h.Write(e.Value)
	if _, err := w.w.Write(buf); err != nil {
		return err
	}
	if _, err := w.w.Write(e.Key); err != nil {
		return err
	}
	if _, err := w.w.Write(e.Value); err != nil {
		return err
	}
	var crcb [crcSize]byte
	putU32(crcb[:], h.Sum32())
	if _, err := w.w.Write(crcb[:]); err != nil {
		return err
	}
	w.off += int64(e.encodedSize())
	w.count++
	w.lastKey = append(w.lastKey[:0], e.Key...)
	return nil
}

// Finish 写入头部、稀疏索引、尾部，fsync 后原子改名为目标段文件。
func (w *Writer) Finish() error {
	indexOff := w.off
	ibuf := io.Writer(w.w)
	var hb [4]byte
	putU32(hb[:], uint32(len(w.index)))
	if _, err := ibuf.Write(hb[:]); err != nil {
		return err
	}
	icrc := crc32.NewIEEE()
	mw := io.MultiWriter(ibuf, icrc)
	mw.Write(hb[:])
	for _, ie := range w.index {
		var kb [4]byte
		putU32(kb[:], uint32(len(ie.Key)))
		mw.Write(kb[:])
		mw.Write(ie.Key)
		var ob [8]byte
		putU64(ob[:], uint64(ie.Offset))
		mw.Write(ob[:])
	}
	var fb [footerSize]byte
	putU32(fb[0:4], icrc.Sum32())
	putU32(fb[4:8], footerMagic)
	if _, err := w.w.Write(fb[:]); err != nil {
		return err
	}

	header := make([]byte, headerSize)
	copy(header[0:8], magicBytes)
	putU32(header[8:12], version)
	putU32(header[12:16], w.count)
	putU64(header[16:24], uint64(indexOff))
	putU32(header[28:32], crc32.ChecksumIEEE(header[:28]))
	if _, err := w.f.WriteAt(header, 0); err != nil {
	return err
	}
	if err := w.w.Flush(); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return err
	}
	if err := w.f.Close(); err != nil {
		return err
	}
	return os.Rename(w.tmpSuffix, w.path)
}

// Abort 放弃构建并删除临时文件。
func (w *Writer) Abort() {
	_ = w.f.Close()
	_ = os.Remove(w.tmpSuffix)
}
