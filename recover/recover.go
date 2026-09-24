// Package recover 回放 WAL，丢弃不完整或 CRC 不符的批次，并分类截断错误。
package recover

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/wal"
)

var (
	// ErrHeaderIncomplete：文件头缺失或被截断。
	ErrHeaderIncomplete = errors.New("recover: file header incomplete")
	// ErrBatchHeaderIncomplete：批头（16B）被截断。
	ErrBatchHeaderIncomplete = errors.New("recover: batch header incomplete")
	// ErrEntryIncomplete：条目区域被截断。
	ErrEntryIncomplete = errors.New("recover: entry incomplete")
	// ErrCRCMismatch：CRC 字段不完整或与内容不符，整批丢弃。
	ErrCRCMismatch = errors.New("recover: batch crc mismatch")
)

// Batch 是一个回放可见的完整批次。
type Batch struct {
	BaseSeq  uint64
	Payloads [][]byte
}

// EndSeq 返回该批最后一条的序号。
func (b Batch) EndSeq() uint64 {
	return b.BaseSeq + uint64(len(b.Payloads)) - 1
}

// Result 是回放结果。
type Result struct {
	Batches  []Batch // 所有 CRC 正确的批次（坏批已整体跳过）
	LastSeq  uint64  // 最后一个完整批次的末序号，无完整批次为 0
	ValidLen int64   // 结构解析停止处偏移，打开方据此截断尾部残片
	Err      error   // 第一个分类错误；文件干净为 nil
}

// Replay 从磁盘读取并回放；文件不存在视为空日志。
func Replay(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Parse(data), nil
}

// Parse 解析 WAL 字节。
func Parse(data []byte) Result {
	r := Result{}
	if len(data) < len(wal.FileHeader) ||
		!bytes.Equal(data[:len(wal.FileHeader)], wal.FileHeader) {
		r.Err = fmt.Errorf("%w (len=%d)", ErrHeaderIncomplete, len(data))
		return r
	}
	off := len(wal.FileHeader)
	setErr := func(e error) {
		if r.Err == nil {
			r.Err = e
		}
	}
	for off < len(data) {
		start := off
		if len(data)-off < 16 {
			setErr(fmt.Errorf("%w at %d", ErrBatchHeaderIncomplete, start))
			break
		}
		base := binary.LittleEndian.Uint64(data[off:])
		count := binary.LittleEndian.Uint32(data[off+8:])
		entryBytes := int(binary.LittleEndian.Uint32(data[off+12:]))
		entryEnd := off + 16 + entryBytes
		end := entryEnd + 4
		if entryBytes < 0 || end > len(data) {
			if entryEnd > len(data) {
				setErr(fmt.Errorf("%w at %d", ErrEntryIncomplete, start))
			} else {
				setErr(fmt.Errorf("%w at %d", ErrCRCMismatch, start))
			}
			break
		}
		h := crc32.NewIEEE()
		_, _ = h.Write(data[off : off+16])
		_, _ = h.Write(data[off+16 : entryEnd])
		if h.Sum32() != binary.LittleEndian.Uint32(data[entryEnd:end]) {
			setErr(fmt.Errorf("%w at %d", ErrCRCMismatch, start))
			off = end
			continue
		}
		payloads, ok := parseEntries(data[off+16:entryEnd], int(count))
		if !ok {
			setErr(fmt.Errorf("%w at %d", ErrCRCMismatch, start))
			off = end
			continue
		}
		b := Batch{BaseSeq: base, Payloads: payloads}
		r.Batches = append(r.Batches, b)
		r.LastSeq = b.EndSeq()
		off = end
	}
	r.ValidLen = int64(off)
	return r
}

func parseEntries(region []byte, count int) ([][]byte, bool) {
	payloads := make([][]byte, 0, count)
	for len(region) > 0 {
		if len(region) < 4 {
			return nil, false
		}
		n := int(binary.LittleEndian.Uint32(region))
		region = region[4:]
		if n > len(region) {
			return nil, false
		}
		payloads = append(payloads, append([]byte(nil), region[:n]...))
		region = region[n:]
	}
	return payloads, len(payloads) == count
}
