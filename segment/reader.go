package segment

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"ontology/event"
)

// Reader 顺序读取一个段文件，并统计物理读取字节数。
type Reader struct {
	f         *os.File
	hdr       Header
	dataPos   int64 // 数据区内当前位置（不含段头）
	bytesRead int64
}

// Open 打开段文件并校验段头。
func Open(dir string, firstSeq uint64) (*Reader, error) {
	return openPath(filepath.Join(dir, fmt.Sprintf("seg-%012d.log", firstSeq)))
}

func openPath(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var hbuf [headerLen]byte
	n, err := io.ReadFull(f, hbuf[:])
	if err != nil {
		f.Close()
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return nil, ErrHeaderIncomplete
		}
		return nil, err
	}
	hdr, err := decodeHeader(hbuf[:n])
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{f: f, hdr: hdr, bytesRead: headerLen}, nil
}

// Header 返回段头。
func (r *Reader) Header() Header { return r.hdr }

// BytesRead 返回至今物理读取的字节数（含段头与定位读取）。
func (r *Reader) BytesRead() int64 { return r.bytesRead }

// SeekToData 跳到数据区内 offset 字节处（稀疏索引用）。
func (r *Reader) SeekToData(offset uint32) error {
	if _, err := r.f.Seek(dataOff+int64(offset), io.SeekStart); err != nil {
		return err
	}
	r.dataPos = int64(offset)
	return nil
}

// DataPos 返回当前数据区内偏移。
func (r *Reader) DataPos() int64 { return r.dataPos }

// Next 读取下一条事件。读到段尾返回 io.EOF；
// 被截断/损坏时返回 ErrLenIncomplete / ErrBodyIncomplete /
// ErrBadRecord / ErrCRCMismatch，可用 errors.Is 区分。
func (r *Reader) Next() (event.Event, error) {
	var lenb [lenPrefix]byte
	if err := r.readFull(lenb[:]); err != nil {
		if err == io.EOF {
			return event.Event{}, io.EOF // 恰好停在记录边界
		}
		return event.Event{}, ErrLenIncomplete
	}
	declared := binary.BigEndian.Uint32(lenb[:])
	payloadLen := framePayloadLen(declared)
	if payloadLen < 0 {
		return event.Event{}, ErrBadRecord
	}
	body := make([]byte, declared)
	if err := r.readFull(body); err != nil {
		return event.Event{}, ErrBodyIncomplete
	}
	content := body[:len(body)-crcSize]
	want := binary.BigEndian.Uint32(body[len(body)-crcSize:])
	if crc32.ChecksumIEEE(content) != want {
		return event.Event{}, ErrCRCMismatch
	}
	ev, err := event.Decode(content)
	if err != nil {
		return event.Event{}, ErrBadRecord
	}
	return ev, nil
}

func (r *Reader) readFull(p []byte) error {
	n, err := io.ReadFull(r.f, p)
	r.bytesRead += int64(n)
	r.dataPos += int64(n)
	return err
}

// Close 关闭段文件。
func (r *Reader) Close() error { return r.f.Close() }
