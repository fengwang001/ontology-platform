// Package segment 实现段文件的自描述头、追加写与基于大小快照的顺序读。
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"ontology/event"
)

const (
	// HeaderSize 是段头固定字节数。
	HeaderSize = 28
	magic      = "ONSE"
	version    = 1
)

// 段级哨兵错误，全部支持 errors.Is。
var (
	ErrBadHeader       = errors.New("segment: bad header")
	ErrTruncatedHeader = errors.New("segment: truncated header")
	ErrTruncatedLength = errors.New("segment: truncated length prefix")
	ErrTruncatedBody   = errors.New("segment: truncated event body")
	ErrTruncatedCRC    = errors.New("segment: truncated crc")
	ErrCRCMismatch     = event.ErrCRCMismatch
	ErrCountMismatch   = errors.New("segment: header count larger than readable frames")
)

// Header 是段头的可判定内容。
type Header struct {
	FirstSeq uint64 // 本段首事件序号
	Count    uint64 // 段头声明的事件数（修复前可能虚高）
}

func encodeHeader(h Header) []byte {
	b := make([]byte, HeaderSize)
	copy(b[0:4], magic)
	b[4] = version
	binary.BigEndian.PutUint64(b[5:13], h.FirstSeq)
	binary.BigEndian.PutUint64(b[13:21], h.Count)
	binary.BigEndian.PutUint32(b[21:25], crc32.ChecksumIEEE(b[:21]))
	return b
}

func decodeHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, ErrTruncatedHeader
	}
	if string(b[0:4]) != magic || b[4] != version {
		return Header{}, ErrBadHeader
	}
	if crc32.ChecksumIEEE(b[:21]) != binary.BigEndian.Uint32(b[21:25]) {
		return Header{}, ErrBadHeader
	}
	return Header{
		FirstSeq: binary.BigEndian.Uint64(b[5:13]),
		Count:    binary.BigEndian.Uint64(b[13:21]),
	}, nil
}

// DataPath 返回某段号的数据文件路径。
func DataPath(dir string, n int) string {
	return fmt.Sprintf("%s/seg_%05d.log", dir, n)
}

// IndexPath 返回某段号的稀疏索引文件路径。
func IndexPath(dir string, n int) string {
	return fmt.Sprintf("%s/seg_%05d.idx", dir, n)
}

// Writer 只追加写一个段。
type Writer struct {
	f       *os.File
	h       Header
	written uint64
	closed  bool
}

// Create 以首序号创建新段（覆盖同名文件）并写头。
func Create(path string, firstSeq uint64) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(encodeHeader(Header{FirstSeq: firstSeq})); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, h: Header{FirstSeq: firstSeq}}, nil
}

// Append 追加一条事件；调用方保证序号连续。
func (w *Writer) Append(payload []byte) error {
	buf, err := event.Encode(nil, payload)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	w.written++
	return nil
}

// Written 返回已追加条数。
func (w *Writer) Written() uint64 { return w.written }

// Close 回写真实事件数到段头后关闭。
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	h := Header{FirstSeq: w.h.FirstSeq, Count: w.written}
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

// Reader 以打开时的文件大小为快照顺序读段。
type Reader struct {
	f        *os.File
	h        Header
	snapshot int64
	off      int64
	frames   uint64
}

var _ io.Closer = (*Reader)(nil)

// Open 打开段并校验段头；读取范围锁定在当前文件大小快照内。
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	head := make([]byte, HeaderSize)
	if st.Size() < int64(HeaderSize) {
		f.Close()
		return nil, ErrTruncatedHeader
	}
	if _, err := io.ReadFull(f, head); err != nil {
		f.Close()
		return nil, err
	}
	h, err := decodeHeader(head)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{f: f, h: h, snapshot: st.Size(), off: HeaderSize}, nil
}

// Header 返回段头内容。
func (r *Reader) Header() Header { return r.h }

// Snapshot 返回打开时锁定的文件大小。
func (r *Reader) Snapshot() int64 { return r.snapshot }

// Frames 返回已成功读出的帧数。
func (r *Reader) Frames() uint64 { return r.frames }

// Next 读下一帧，返回该帧数据起点偏移与载荷复制。
func (r *Reader) Next() (offset int64, payload []byte, err error) {
	remaining := r.snapshot - r.off
	if remaining == 0 {
		return 0, nil, io.EOF
	}
	var lenBuf [4]byte
	if remaining < 4 {
		return r.off, nil, ErrTruncatedLength
	}
	if _, err := io.ReadFull(r.f, lenBuf[:]); err != nil {
		return r.off, nil, err
	}
	r.off += 4
	remaining -= 4
	length := int64(binary.BigEndian.Uint32(lenBuf[:]))
	if remaining < length {
		return r.off - 4, nil, ErrTruncatedBody
	}
	if remaining < length+4 {
		return r.off - 4, nil, ErrTruncatedCRC
	}
	buf := make([]byte, length+4)
	if _, err := io.ReadFull(r.f, buf); err != nil {
		return r.off - 4, nil, err
	}
	r.off += length + 4
	body, _, perr := event.Consume(append(lenBuf[:], buf...))
	if perr != nil {
		return r.off - (4 + length + 4), nil, perr
	}
	offset = r.off - (4 + length + 4)
	r.frames++
	return offset, body, nil
}

// Close 关闭底层文件。
func (r *Reader) Close() error { return r.f.Close() }
