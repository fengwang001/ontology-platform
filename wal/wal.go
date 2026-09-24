// Package wal 负责批次的自描述编码、追加落盘与读回。
package wal

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

// 磁盘布局：
//
//	文件头：魔数 8 字节
//	批次：头 16B(baseSeq u64|count u32|entriesBytes u32)
//	      + 逐条(len u32|payload) + 批尾 crc u32
const (
	fileHeaderSize  = 8
	batchHeaderSize = 16
	crcSize         = 4
)

// FileHeader 是每个 WAL 文件开头固定的魔数。
var FileHeader = []byte("ONTWAL01")

// EncodeBatch 编码一个批次（批头 + 长度前缀条目 + 批尾 CRC）。
func EncodeBatch(baseSeq uint64, payloads [][]byte) []byte {
	entryBytes := 0
	for _, p := range payloads {
		entryBytes += 4 + len(p)
	}
	buf := make([]byte, batchHeaderSize+entryBytes+crcSize)
	binary.LittleEndian.PutUint64(buf[0:8], baseSeq)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(payloads)))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(entryBytes))
	off := batchHeaderSize
	for _, p := range payloads {
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(len(p)))
		off += 4
		copy(buf[off:off+len(p)], p)
		off += len(p)
	}
	h := crc32.NewIEEE()
	_, _ = h.Write(buf[:batchHeaderSize])
	_, _ = h.Write(buf[batchHeaderSize : batchHeaderSize+entryBytes])
	binary.LittleEndian.PutUint32(buf[off:off+crcSize], h.Sum32())
	return buf
}

// BatchSize 返回一个批次在磁盘上的总字节数（不含文件头）。
func BatchSize(payloads [][]byte) int {
	n := batchHeaderSize + crcSize
	for _, p := range payloads {
		n += 4 + len(p)
	}
	return n
}

// Writer 以追加方式写 WAL。
type Writer struct {
	f            *os.File
	headerWanted bool // 文件全新，首批之前需要写文件头
}

// NewWriter 包装已打开的文件；newFile 为 true 时首批前写文件头。
func NewWriter(f *os.File, newFile bool) *Writer {
	return &Writer{f: f, headerWanted: newFile}
}

// WriteBatch 一次写入整批（调用方随后调用 Sync 才可见）。
func (w *Writer) WriteBatch(baseSeq uint64, payloads [][]byte) error {
	if w.headerWanted {
		if _, err := w.f.Write(FileHeader); err != nil {
			return err
		}
		w.headerWanted = false
	}
	_, err := w.f.Write(EncodeBatch(baseSeq, payloads))
	return err
}

// Sync 执行一次 fsync。
func (w *Writer) Sync() error { return w.f.Sync() }

// Close 关闭文件。
func (w *Writer) Close() error { return w.f.Close() }
