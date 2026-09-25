// Package manifest 定义导出文件格式：自描述块、尾部清单与校验原语。
package manifest

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// 四类格式错误加上快照过期、块缺失、块乱序，全部用 errors.Is 判定。
var (
	ErrManifestIncomplete  = errors.New("manifest: manifest incomplete")
	ErrBlockHeaderIncomp   = errors.New("manifest: block header incomplete")
	ErrBlockBodyIncomplete = errors.New("manifest: block body incomplete")
	ErrCRCMismatch         = errors.New("manifest: block crc mismatch")
	ErrBlockMissing        = errors.New("manifest: block missing")
	ErrBlockOutOfOrder     = errors.New("manifest: block out of order")
	ErrBadBlockSize        = errors.New("manifest: block size must be positive")
)

const (
	lenSize   = 4 // blockLen u32
	noSize    = 4 // blockNo u32
	crcSize   = 4 // crc u32
	trailerSz = 8 // manifestLen u64
	frameHead = lenSize + noSize
)

// BlockInfo 描述一个块在文件中的位置与内容。
type BlockInfo struct {
	No     int      `json:"no"`
	Offset int64    `json:"offset"`
	Length int      `json:"length"` // 整块（头 + payload + crc）字节数
	Keys   []string `json:"keys"`
}

// Manifest 是导出的自描述清单。
type Manifest struct {
	Version    int64       `json:"version"`
	BlockSize  int         `json:"block_size"`
	Blocks     []BlockInfo `json:"blocks"`
	Records    int         `json:"records"`
	PayloadLen int         `json:"payload_len"`
	TotalCRC   uint32      `json:"total_crc"`
	Order      int         `json:"order"`
}

var crcTable = crc32.MakeTable(crc32.IEEE)

// RecordCRC 计算单条记录对总校验和的贡献，与记录顺序无关。
func RecordCRC(key string, value []byte) uint32 {
	buf := make([]byte, len(key)+8+len(value)+1)
	n := copy(buf, key)
	buf[n] = 0
	binary.BigEndian.PutUint64(buf[n+1:], uint64(len(value)))
	copy(buf[n+9:], value)
	return crc32.Checksum(buf, crcTable)
}

// EncodeRecord 将一条记录编码为 payload 片段。
func EncodeRecord(key string, value []byte) []byte {
	buf := make([]byte, 8+len(key)+len(value))
	binary.BigEndian.PutUint32(buf, uint32(len(key)))
	copy(buf[4:], key)
	binary.BigEndian.PutUint32(buf[4+len(key):], uint32(len(value)))
	copy(buf[8+len(key):], value)
	return buf
}

// AppendRecord 向 payload 缓冲追加一条记录。
func AppendRecord(dst []byte, key string, value []byte) []byte {
	return append(dst, EncodeRecord(key, value)...)
}

// DecodeRecord 解析一条记录，返回键、值与消费字节数。
func DecodeRecord(p []byte) (string, []byte, int, error) {
	if len(p) < 8 {
		return "", nil, 0, ErrBlockBodyIncomplete
	}
	kl := int(binary.BigEndian.Uint32(p))
	if 8+kl+4 > len(p) {
		return "", nil, 0, ErrBlockBodyIncomplete
	}
	key := string(p[4 : 4+kl])
	vl := int(binary.BigEndian.Uint32(p[4+kl:]))
	end := 8 + kl + vl
	if end > len(p) {
		return "", nil, 0, ErrBlockBodyIncomplete
	}
	return key, p[8+kl : end], end, nil
}

// EncodeFrame 将块号与 payload 包成自描述帧。
func EncodeFrame(no int, payload []byte) []byte {
	frame := make([]byte, frameHead+len(payload)+crcSize)
	binary.BigEndian.PutUint32(frame, uint32(noSize+len(payload)+crcSize))
	binary.BigEndian.PutUint32(frame[lenSize:], uint32(no))
	copy(frame[frameHead:], payload)
	sum := crc32.Checksum(frame[lenSize:frameHead+len(payload)], crcTable)
	binary.BigEndian.PutUint32(frame[frameHead+len(payload):], sum)
	return frame
}

// FrameFields 解析帧头给出的块号与块长度。
func FrameFields(head []byte) (no int, blockLen int) {
	return int(binary.BigEndian.Uint32(head[lenSize:])),
		int(binary.BigEndian.Uint32(head))
}

// FrameCRC 校验一整块（head 为前 8 字节）。
func FrameCRC(head, rest []byte) bool {
	no, blockLen := FrameFields(head)
	_ = no
	if len(rest) < blockLen-noSize {
		return false
	}
	payloadLen := blockLen - noSize - crcSize
	payload := rest[:payloadLen]
	want := binary.BigEndian.Uint32(rest[payloadLen : payloadLen+crcSize])
	buf := make([]byte, noSize+payloadLen)
	copy(buf, head[lenSize:])
	copy(buf[noSize:], payload)
	return crc32.Checksum(buf, crcTable) == want
}

// MarshalTrailer 返回 JSON manifest 加末尾 u64 长度。
func (m *Manifest) MarshalTrailer() ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data)+trailerSz)
	copy(out, data)
	binary.BigEndian.PutUint64(out[len(data):], uint64(len(data)))
	return out, nil
}

// ReadTrailer 从完整文件字节解析 manifest。
func ReadTrailer(file []byte) (*Manifest, error) {
	if len(file) < trailerSz {
		return nil, ErrManifestIncomplete
	}
	ml := int(binary.BigEndian.Uint64(file[len(file)-trailerSz:]))
	if trailerSz+ml > len(file) {
		return nil, ErrManifestIncomplete
	}
	var m Manifest
	if err := json.Unmarshal(file[len(file)-trailerSz-ml:len(file)-trailerSz], &m); err != nil {
		return nil, ErrManifestIncomplete
	}
	return &m, nil
}
