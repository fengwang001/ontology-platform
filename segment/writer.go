package segment

import (
	"bytes"
	"hash"
	"hash/crc32"
	"os"
)

// Writer 顺序写出有序段。键必须按非降序加入。
type Writer struct {
	f       *os.File
	off     uint64
	crc     hash.Hash32 // 累积 header 之后的全部字节
	entries uint32
	index   []indexEntry
	lastKey []byte
	closed  bool
}

func NewWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(make([]byte, HeaderSize)); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, off: HeaderSize, crc: crc32.NewIEEE()}, nil
}

func (w *Writer) Add(key, val []byte) error { return w.add(key, val, false) }

func (w *Writer) Delete(key []byte) error { return w.add(key, nil, true) }

func (w *Writer) Entries() int { return int(w.entries) }

func (w *Writer) add(key, val []byte, deleted bool) error {
	if w.closed {
		return os.ErrClosed
	}
	if w.entries > 0 && bytes.Compare(w.lastKey, key) > 0 {
		return ErrUnsorted
	}
	if w.entries%Stride == 0 {
		w.index = append(w.index, indexEntry{key: string(key), off: w.off})
	}
	buf := encodeEntry(nil, key, val, deleted)
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	w.crc.Write(buf)
	w.off += uint64(len(buf))
	w.lastKey = append(w.lastKey[:0], key...)
	w.entries++
	return nil
}

// Close 写出索引与 footer CRC，回填头部并 fsync。
func (w *Writer) Close() error {
	if w.closed {
		return os.ErrClosed
	}
	w.closed = true
	h := header{entries: w.entries, indexOffset: w.off}
	var idx []byte
	for _, e := range w.index {
		idx = encodeIndexEntry(idx, e)
	}
	if _, err := w.f.Write(idx); err != nil {
		w.f.Close()
		return err
	}
	w.crc.Write(idx)
	h.indexLen = uint64(len(idx))
	h.indexCount = uint32(len(w.index))
	footer := w.crc.Sum32()
	if _, err := w.f.Write(binary32(footer)); err != nil {
		w.f.Close()
		return err
	}
	if _, err := w.f.WriteAt(encodeHeader(h), 0); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

func binary32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}
