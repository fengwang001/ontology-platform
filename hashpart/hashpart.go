// Package hashpart 提供分区函数与分区段文件的写出、读回、损坏分类。
package hashpart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 分区文件格式：头 20 字节 + 逐条记录（长度前缀 + 行体 + CRC32）。
// 头：magic(4) | partID(uint32 LE) | segIdx(uint32 LE) | recordCount(uint64 LE)。
const (
	HeaderLen = 20
	magic     = "HAG1"
	tmpSuffix = ".tmp"
)

// 四类可判定损坏 + 写出失败，均可用 errors.Is 区分。
var (
	ErrHeader       = errors.New("hashpart: 头部不完整或非法")
	ErrLengthPrefix = errors.New("hashpart: 长度前缀不完整")
	ErrBody         = errors.New("hashpart: 行体不完整")
	ErrCRC          = errors.New("hashpart: CRC 不匹配")
	ErrWrite        = errors.New("hashpart: 分区写盘失败")
)

// FileError 携带分区号与字节偏移的损坏错误。
type FileError struct {
	Part   int
	Offset int64
	Err    error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("分区 %d 偏移 %d: %v", e.Part, e.Offset, e.Err)
}

func (e *FileError) Unwrap() error { return e.Err }

// Partition 返回键所属分区，取值 [0, numParts)。每行只应调用一次。
func Partition(key string, numParts int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(numParts))
}

// File 是一个完整读回并校验通过的分区段文件。
type File struct {
	Part     int
	Seg      int
	Payloads [][]byte
}

// ReadFile 读取并逐条校验分区段文件；任何截断或 CRC 不符都返回 *FileError。
func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	part := partFromName(path)
	if len(data) < HeaderLen {
		return nil, &FileError{part, int64(len(data)), ErrHeader}
	}
	if string(data[:4]) != magic {
		return nil, &FileError{part, 0, ErrHeader}
	}
	part = int(binary.LittleEndian.Uint32(data[4:]))
	seg := int(binary.LittleEndian.Uint32(data[8:]))
	count := binary.LittleEndian.Uint64(data[12:])
	f := &File{Part: part, Seg: seg}
	off := HeaderLen
	for i := uint64(0); i < count; i++ {
		if off+4 > len(data) {
			return nil, &FileError{part, int64(off), ErrLengthPrefix}
		}
		n := int(binary.LittleEndian.Uint32(data[off:]))
		body := off + 4
		if body+n > len(data) {
			return nil, &FileError{part, int64(body), ErrBody}
		}
		crcOff := body + n
		if crcOff+4 > len(data) {
			return nil, &FileError{part, int64(crcOff), ErrCRC}
		}
		if crc32.ChecksumIEEE(data[body:crcOff]) != binary.LittleEndian.Uint32(data[crcOff:]) {
			return nil, &FileError{part, int64(crcOff), ErrCRC}
		}
		f.Payloads = append(f.Payloads, data[body:crcOff])
		off = crcOff + 4
	}
	return f, nil
}

// partFromName 从文件名 part-%04d-seg-%06d[.tmp] 解析分区号，失败返回 -1。
func partFromName(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), tmpSuffix)
	if !strings.HasPrefix(base, "part-") || len(base) < 9 {
		return -1
	}
	n, err := strconv.Atoi(base[5:9])
	if err != nil {
		return -1
	}
	return n
}
