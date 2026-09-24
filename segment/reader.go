package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"ontology/event"
)

// Reader 顺序读取一个段，位置可在记录边界上 Seek。
type Reader struct {
	f     *os.File
	path  string
	h     Header
	off   int64
	bytes int64
}

// Path 返回编号 index 的段数据文件路径。
func Path(dir string, index int) string {
	return filepath.Join(dir, fmt.Sprintf("seg-%06d.log", index))
}

// Open 打开一个段用于读取。
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f, path: path}
	hdr := make([]byte, HeaderSize)
	n, err := f.ReadAt(hdr, 0)
	if err != nil || n < HeaderSize || string(hdr[:4]) != magic {
		f.Close()
		return nil, ErrHeaderIncomplete
	}
	r.h = Header{
		FirstSeq: binary.LittleEndian.Uint64(hdr[8:16]),
		Count:    binary.LittleEndian.Uint64(hdr[16:24]),
	}
	r.off = HeaderSize
	return r, nil
}

// Header 返回段头。
func (r *Reader) Header() Header { return r.h }

// Path 返回段数据文件路径。
func (r *Reader) Path() string { return r.path }

// SeekRecord 跳到记录边界偏移（不校验内容）。
func (r *Reader) SeekRecord(off int64) { r.off = off }

// Offset 返回当前读取偏移。
func (r *Reader) Offset() int64 { return r.off }

// BytesRead 返回自打开起实际读取的字节数。
func (r *Reader) BytesRead() int64 { return r.bytes }

// ResetCounters 清零读取字节计数。
func (r *Reader) ResetCounters() { r.bytes = 0 }

// ValidAt 判断偏移处是否能解出一条合法记录（CRC 正确）。
func (r *Reader) ValidAt(off int64) bool {
	saved := r.off
	savedBytes := r.bytes
	r.off = off
	r.bytes = 0
	_, err := r.Next()
	r.off = saved
	r.bytes = savedBytes
	return err == nil
}

// Next 读取下一条记录。
func (r *Reader) Next() (event.Event, error) {
	var lb [4]byte
	n, err := r.f.ReadAt(lb[:], r.off)
	r.bytes += int64(n)
	if n == 0 && errors.Is(err, io.EOF) {
		return event.Event{}, io.EOF
	}
	if n < 4 {
		return event.Event{}, ErrLenPrefixIncomplete
	}
	if err != nil {
		return event.Event{}, err
	}
	bodyLen := int64(binary.LittleEndian.Uint32(lb[:]))
	if bodyLen > 1<<20 {
		return event.Event{}, ErrLenPrefixIncomplete
	}
	rest := make([]byte, bodyLen+4)
	n, err = r.f.ReadAt(rest, r.off+4)
	r.bytes += int64(n)
	if int64(n) < bodyLen+4 {
		return event.Event{}, ErrBodyIncomplete
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return event.Event{}, err
	}
	got := binary.LittleEndian.Uint32(rest[bodyLen:])
	checked := make([]byte, 0, 4+int(bodyLen))
	checked = append(checked, lb[:]...)
	checked = append(checked, rest[:bodyLen]...)
	want := crc32.Checksum(checked, crcTable)
	if got != want {
		return event.Event{}, ErrCRC
	}
	ev, _, derr := event.Decode(rest[:bodyLen])
	if derr != nil {
		return event.Event{}, derr
	}
	r.off += 4 + bodyLen + 4
	return ev, nil
}

// Close 关闭段文件。
func (r *Reader) Close() error { return r.f.Close() }
