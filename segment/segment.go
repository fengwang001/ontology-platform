// Package segment 提供段文件的追加写与顺序读：
// 自描述头 + 逐事件长度前缀 + CRC32。
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"

	"ontology/event"
)

const (
	// HeaderSize 是段头字节数：magic4 + ver2 + flags2 + firstSeq8 + count8。
	HeaderSize = 24
	// CountOffset 是段头内 count 字段的偏移。
	CountOffset = 16
	// MaxEventLen 防止被篡改的长度前缀导致超大分配。
	MaxEventLen = 64 << 20
)

var (
	magic = [4]byte{'O', 'S', 'E', 'G'}

	// ErrHeaderIncomplete 头部不完整（文件不足 24 字节或 magic 不符）。
	ErrHeaderIncomplete = errors.New("segment: header incomplete")
	// ErrLengthIncomplete 长度前缀不完整或不可信。
	ErrLengthIncomplete = errors.New("segment: length prefix incomplete")
	// ErrBodyIncomplete 事件体不完整。
	ErrBodyIncomplete = errors.New("segment: event body incomplete")
	// ErrCRCMismatch CRC 字段不完整或校验不符。
	ErrCRCMismatch = errors.New("segment: crc mismatch")
)

// Header 是段的自描述头。
type Header struct {
	FirstSeq uint64
	Count    uint64
}

// RecordLen 返回一条含长度前缀与 CRC 的记录总字节数。
func RecordLen(eventLen int) int { return 4 + eventLen + 4 }

func encodeHeader(h Header) []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:4], magic[:])
	binary.LittleEndian.PutUint16(buf[4:6], 1)
	binary.LittleEndian.PutUint64(buf[8:16], h.FirstSeq)
	binary.LittleEndian.PutUint64(buf[16:24], h.Count)
	return buf
}

// ReadHeader 从段文件读出头部。
func ReadHeader(f *os.File) (Header, error) {
	buf := make([]byte, HeaderSize)
	n, err := io.ReadFull(f, buf)
	if err != nil || n < HeaderSize {
		return Header{}, fmt.Errorf("%w: %d bytes", ErrHeaderIncomplete, n)
	}
	if string(buf[0:4]) != string(magic[:]) {
		return Header{}, fmt.Errorf("%w: bad magic", ErrHeaderIncomplete)
	}
	return Header{
		FirstSeq: binary.LittleEndian.Uint64(buf[8:16]),
		Count:    binary.LittleEndian.Uint64(buf[16:24]),
	}, nil
}

// Writer 向一个段文件追加事件。
type Writer struct {
	mu       sync.Mutex
	f        *os.File
	firstSeq uint64
	count    uint64
	offset   int64
}

// Create 创建段文件并写入 count=0 的头。
func Create(path string, firstSeq uint64) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(encodeHeader(Header{FirstSeq: firstSeq})); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, firstSeq: firstSeq, offset: HeaderSize}, nil
}

// Append 追加一条事件，随后更新段头 count。
func (w *Writer) Append(e event.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	enc := event.Encode(e)
	rec := make([]byte, RecordLen(len(enc)))
	binary.LittleEndian.PutUint32(rec[0:4], uint32(len(enc)))
	copy(rec[4:4+len(enc)], enc)
	binary.LittleEndian.PutUint32(rec[4+len(enc):], crc32.ChecksumIEEE(enc))
	if _, err := w.f.WriteAt(rec, w.offset); err != nil {
		return err
	}
	w.offset += int64(len(rec))
	w.count++
	var cb [8]byte
	binary.LittleEndian.PutUint64(cb[:], w.count)
	_, err := w.f.WriteAt(cb[:], CountOffset)
	return err
}

// Sync 刷盘。
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

// Close 关闭段。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

// FirstSeq 返回段首序号。
func (w *Writer) FirstSeq() uint64 { return w.firstSeq }

// Count 返回已追加事件数（内存视图）。
func (w *Writer) Count() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

// ScanFunc 对扫描到的每条事件回调：offset=记录起点，recLen=记录总长度。
type ScanFunc func(offset int64, e event.Event, recLen int) error

// Scan 从 startOff 顺序扫描，逐条回调；干净 EOF 返回 nil，
// 截断/损坏返回对应哨兵错误（可 errors.Is 区分）。
func Scan(f *os.File, startOff int64, fn ScanFunc) error {
	var lb [4]byte
	off := startOff
	for {
		n, err := f.ReadAt(lb[:], off)
		if err == io.EOF && n == 0 {
			return nil
		}
		if n < 4 {
			return fmt.Errorf("%w at %d (read %d/4)", ErrLengthIncomplete, off, n)
		}
		L := int(binary.LittleEndian.Uint32(lb[:]))
		if L < event.HeaderLen || L > MaxEventLen {
			return fmt.Errorf("%w at %d (len=%d)", ErrLengthIncomplete, off, L)
		}
		body := make([]byte, L)
		n, err = f.ReadAt(body, off+4)
		if n < L {
			return fmt.Errorf("%w at %d (read %d/%d)", ErrBodyIncomplete, off, n, L)
		}
		var cb [4]byte
		n, _ = f.ReadAt(cb[:], off+4+int64(L))
		if n < 4 {
			return fmt.Errorf("%w at %d (read %d/4)", ErrCRCMismatch, off, n)
		}
		if binary.LittleEndian.Uint32(cb[:]) != crc32.ChecksumIEEE(body) {
			return fmt.Errorf("%w at %d", ErrCRCMismatch, off)
		}
		ev, consumed, derr := event.Decode(body)
		if derr != nil || consumed != L {
			return fmt.Errorf("%w at %d: %v", ErrBodyIncomplete, off, derr)
		}
		recLen := RecordLen(L)
		if err := fn(off, ev, recLen); err != nil {
			return err
		}
		off += int64(recLen)
	}
}
