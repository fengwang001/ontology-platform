// Package spill 定义 run 文件格式：自描述头 + 长度前缀记录 + 每条 CRC32，
// 并提供写入、读回、逐条校验与截断后的最大可恢复前缀。
package spill

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/record"
)

// HeaderSize 是自描述头的字节数。
const HeaderSize = 20

var magic = [4]byte{'O', 'S', 'R', '1'}

// 截断/损坏的分类错误（哨兵，可用 errors.Is 判定）。
var (
	ErrHeaderIncomplete       = errors.New("spill: header incomplete")
	ErrLengthPrefixIncomplete = errors.New("spill: length prefix incomplete")
	ErrRecordBodyIncomplete   = errors.New("spill: record body incomplete")
	ErrCRCMismatch            = errors.New("spill: crc mismatch")
)

// maxPayload 防止损坏的长度前缀导致巨量分配。
const maxPayload = 1 << 30

func writeHeaderTo(w io.Writer, count uint64) error {
	var hdr [HeaderSize]byte
	copy(hdr[0:4], magic[:])
	binary.LittleEndian.PutUint16(hdr[4:6], 1)
	binary.LittleEndian.PutUint64(hdr[8:16], count)
	binary.LittleEndian.PutUint32(hdr[16:20], crc32.ChecksumIEEE(hdr[0:16]))
	_, err := w.Write(hdr[:])
	return err
}

func readHeaderFrom(r io.Reader) (uint64, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, ErrHeaderIncomplete
	}
	if string(hdr[0:4]) != string(magic[:]) ||
		crc32.ChecksumIEEE(hdr[0:16]) != binary.LittleEndian.Uint32(hdr[16:20]) {
		return 0, ErrHeaderIncomplete
	}
	return binary.LittleEndian.Uint64(hdr[8:16]), nil
}

// readEntry 读一条记录，失败时返回四类分类错误之一。
func readEntry(r io.Reader) (record.Record, error) {
	var rec record.Record
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return rec, ErrLengthPrefixIncomplete
	}
	plen := binary.LittleEndian.Uint32(lenBuf[:])
	if plen > maxPayload {
		return rec, ErrRecordBodyIncomplete
	}
	payload := make([]byte, plen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return rec, ErrRecordBodyIncomplete
	}
	var crcBuf [4]byte
	if _, err := io.ReadFull(r, crcBuf[:]); err != nil {
		return rec, ErrCRCMismatch
	}
	if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(crcBuf[:]) {
		return rec, ErrCRCMismatch
	}
	rec, err := record.Unmarshal(payload)
	if err != nil {
		return rec, ErrRecordBodyIncomplete
	}
	return rec, nil
}

// Writer 以流式方式写 run 文件，记录总数需预先已知。
type Writer struct {
	w   *bufio.Writer
	err error
}

// NewWriter 写出头并返回可逐条 Add 的 Writer。
func NewWriter(w io.Writer, count uint64) *Writer {
	bw := bufio.NewWriter(w)
	return &Writer{w: bw, err: writeHeaderTo(bw, count)}
}

// Add 追加一条记录（长度前缀 + payload + CRC32）。
func (wr *Writer) Add(rec record.Record) error {
	if wr.err != nil {
		return wr.err
	}
	payload := rec.Marshal()
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(payload))
	if _, err := wr.w.Write(lenBuf[:]); err != nil {
		wr.err = err
		return err
	}
	if _, err := wr.w.Write(payload); err != nil {
		wr.err = err
		return err
	}
	if _, err := wr.w.Write(crcBuf[:]); err != nil {
		wr.err = err
		return err
	}
	return nil
}

// Close 冲刷缓冲。
func (wr *Writer) Close() error {
	if wr.err != nil {
		return wr.err
	}
	return wr.w.Flush()
}

// WriteRun 把一批已排序记录原子地写成一个 run 文件。
func WriteRun(path string, recs []record.Record) (err error) {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := NewWriter(f, uint64(len(recs)))
	for _, rec := range recs {
		if err = w.Add(rec); err != nil {
			f.Close()
			return err
		}
	}
	if err = w.Close(); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
