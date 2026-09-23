package segment

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"sync/atomic"
)

// Reader 以只读方式打开段文件，并发安全（全部走 ReadAt）。
type Reader struct {
	f        *os.File
	h        header
	index    []indexEntry
	compares atomic.Int64 // 非导出比较计数器，供测试断言上界
}

// Open 打开段并按截断点分类错误：头部不完整 / 条目不完整 /
// 索引段不完整 / CRC 不匹配。
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := fi.Size()
	if size < HeaderSize {
		f.Close()
		return nil, ErrHeaderIncomplete
	}
	hb := make([]byte, HeaderSize)
	if _, err := f.ReadAt(hb, 0); err != nil {
		f.Close()
		return nil, ErrHeaderIncomplete
	}
	h, err := decodeHeader(hb)
	if err != nil {
		f.Close()
		return nil, err
	}
	end := int64(h.indexOffset) + int64(h.indexLen)
	switch {
	case size < int64(h.indexOffset):
		f.Close()
		return nil, ErrEntryTruncated
	case size < end:
		f.Close()
		return nil, ErrIndexIncomplete
	case size < end+4:
		f.Close()
		return nil, ErrCRCMismatch
	}
	crc := crc32.NewIEEE()
	if _, err := io.Copy(crc, io.NewSectionReader(f, HeaderSize, end-HeaderSize)); err != nil {
		f.Close()
		return nil, err
	}
	var fb [4]byte
	if _, err := f.ReadAt(fb[:], end); err != nil {
		f.Close()
		return nil, ErrCRCMismatch
	}
	if binary.LittleEndian.Uint32(fb[:]) != crc.Sum32() {
		f.Close()
		return nil, ErrCRCMismatch
	}
	r := &Reader{f: f, h: h}
	if err := r.loadIndex(); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

func (r *Reader) loadIndex() error {
	buf := make([]byte, r.h.indexLen)
	if _, err := r.f.ReadAt(buf, int64(r.h.indexOffset)); err != nil {
		return ErrIndexIncomplete
	}
	for len(buf) > 0 {
		kl := binary.LittleEndian.Uint32(buf[0:4])
		key := string(buf[4 : 4+kl])
		off := binary.LittleEndian.Uint64(buf[4+kl : 12+kl])
		r.index = append(r.index, indexEntry{key: key, off: off})
		buf = buf[12+kl:]
	}
	return nil
}

func (r *Reader) Close() error     { return r.f.Close() }
func (r *Reader) EntryCount() int  { return int(r.h.entries) }
func (r *Reader) Compares() int64  { return r.compares.Load() }
func (r *Reader) ResetCompares()   { r.compares.Store(0) }
func (r *Reader) DataStart() int64 { return HeaderSize }
func (r *Reader) DataEnd() int64   { return int64(r.h.indexOffset) }

// DecodeAt 解码 off 处的一条条目，返回键、值、删除标记与下一条偏移。
func (r *Reader) DecodeAt(off int64) (key, val []byte, deleted bool, next int64, err error) {
	var hdr [8]byte
	if _, err = r.f.ReadAt(hdr[:], off); err != nil {
		return nil, nil, false, 0, ErrEntryTruncated
	}
	kl := int64(binary.LittleEndian.Uint32(hdr[0:4]))
	vl := int32(binary.LittleEndian.Uint32(hdr[4:8]))
	bodyLen := kl
	if vl >= 0 {
		bodyLen += int64(vl)
	}
	raw := make([]byte, 8+bodyLen+4)
	copy(raw, hdr[:])
	if _, err = r.f.ReadAt(raw[8:], off+8); err != nil {
		return nil, nil, false, 0, ErrEntryTruncated
	}
	if crc32.ChecksumIEEE(raw[:8+bodyLen]) != binary.LittleEndian.Uint32(raw[8+bodyLen:]) {
		return nil, nil, false, 0, ErrCRCMismatch
	}
	body := raw[8:]
	key = body[:kl]
	deleted = vl == tombstone
	if !deleted {
		val = body[kl:bodyLen]
	}
	return key, val, deleted, off + 8 + bodyLen + 4, nil
}

// Get 点查：稀疏索引二分定位 + 块内顺序扫，绝不线性扫全段。
func (r *Reader) Get(key []byte) ([]byte, State, error) {
	pos, lo, hi := -1, 0, len(r.index)-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		r.compares.Add(1)
		if bytes.Compare([]byte(r.index[mid].key), key) <= 0 {
			pos = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	off := int64(HeaderSize)
	if pos >= 0 {
		off = int64(r.index[pos].off)
	}
	for off < int64(r.h.indexOffset) {
		k, v, del, next, err := r.DecodeAt(off)
		if err != nil {
			return nil, StateAbsent, err
		}
		r.compares.Add(1)
		switch c := bytes.Compare(k, key); {
		case c == 0:
			if del {
				return nil, StateDeleted, nil
			}
			return v, StateValue, nil
		case c > 0:
			return nil, StateAbsent, nil
		}
		off = next
	}
	return nil, StateAbsent, nil
}

// KV 是可恢复前缀中的一条完整条目。
type KV struct {
	Key, Val []byte
	Deleted  bool
}

// RecoverPrefix 读出截断段的最大可恢复前缀，半截条目一条不留。
func RecoverPrefix(path string) ([]KV, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() < HeaderSize {
		return nil, ErrHeaderIncomplete
	}
	r := &Reader{f: f}
	var out []KV
	for off := int64(HeaderSize); off < fi.Size(); {
		k, v, del, next, err := r.DecodeAt(off)
		if err != nil {
			break
		}
		out = append(out, KV{Key: k, Val: v, Deleted: del})
		off = next
	}
	return out, nil
}
