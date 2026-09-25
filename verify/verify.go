// Package verify 校验导出文件的完整性与一致性。
package verify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/manifest"
)

var (
	// ErrManifestIncomplete：清单行未完整结束或无法解析。
	ErrManifestIncomplete = errors.New("manifest incomplete")
	// ErrBlockHeaderIncomplete：块头 16 字节被截断。
	ErrBlockHeaderIncomplete = errors.New("block header incomplete")
	// ErrBlockBodyIncomplete：块体或 CRC 尾被截断。
	ErrBlockBodyIncomplete = errors.New("block body incomplete")
	// ErrCRC：块体长度完整但 CRC32 不匹配。
	ErrCRC = errors.New("crc mismatch")
	// ErrBlockOutOfOrder：块索引与清单声明不一致（携带实际/期望块号）。
	ErrBlockOutOfOrder = errors.New("block out of order")
	// ErrMissingBlock：在期望块号处数据结束。
	ErrMissingBlock = errors.New("missing block")
	// ErrRootHash：全部块完好但记录集合总校验和与清单不符。
	ErrRootHash = errors.New("root hash mismatch")
)

// BlockError 指明出问题的块号，支持 errors.Is 匹配上述错误。
type BlockError struct {
	Op  error
	At  int
	Got int
}

func (e *BlockError) Error() string {
	return fmt.Sprintf("%v at block %d (got %d)", e.Op, e.At, e.Got)
}
func (e *BlockError) Unwrap() error { return e.Op }

const headerSize = 16

// Verify 解析并校验导出文件内容，返回解析出的清单。
func Verify(data []byte) (*manifest.Manifest, error) {
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return nil, ErrManifestIncomplete
	}
	m, err := manifest.Decode(data[:nl])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestIncomplete, err)
	}
	pos := int64(nl + 1)
	var root [32]byte
	for i := 0; i < m.BlockCount; i++ {
		rest := int64(len(data)) - pos
		if rest == 0 {
			return m, &BlockError{Op: ErrMissingBlock, At: i}
		}
		if rest < headerSize {
			return m, &BlockError{Op: ErrBlockHeaderIncomplete, At: i}
		}
		hdr := data[pos : pos+headerSize]
		got := int(binary.BigEndian.Uint32(hdr[4:8]))
		dlen := int(binary.BigEndian.Uint32(hdr[8:12]))
		if got != i {
			return m, &BlockError{Op: ErrBlockOutOfOrder, At: i, Got: got}
		}
		if int64(len(data))-pos-headerSize < int64(dlen) {
			return m, &BlockError{Op: ErrBlockBodyIncomplete, At: i}
		}
		body := data[pos+headerSize : pos+headerSize+int64(dlen)]
		if binary.BigEndian.Uint32(hdr[12:16]) != crc32.ChecksumIEEE(body) {
			return m, &BlockError{Op: ErrCRC, At: i}
		}
		if err := accumulate(body, &root); err != nil {
			return m, err
		}
		pos += headerSize + int64(dlen)
	}
	if manifest.RootHash(&root) != m.RootHash {
		return m, ErrRootHash
	}
	return m, nil
}

func accumulate(body []byte, root *[32]byte) error {
	for len(body) > 0 {
		if len(body) < 8 {
			return ErrBlockBodyIncomplete
		}
		klen := int(binary.BigEndian.Uint32(body[0:4]))
		vlen := int(binary.BigEndian.Uint32(body[4:8]))
		recLen := 8 + klen + vlen
		if len(body) < recLen {
			return ErrBlockBodyIncomplete
		}
		key := string(body[8 : 8+klen])
		val := body[8+klen : recLen]
		d := manifest.RecordDigest(key, val)
		manifest.XOR(root, &d)
		body = body[recLen:]
	}
	return nil
}
