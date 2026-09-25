package export

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/manifest"
	"ontology/snapshot"
)

const (
	headerSize = 16
	magic0     = 0x4f // O
	magic1     = 0x45 // E
)

var (
	ErrChunkSizeZero         = errors.New("export: chunk size must be > 0")
	ErrSnapshotExpired       = errors.New("export: snapshot expired")
	ErrIncompleteManifest    = errors.New("export: incomplete manifest")
	ErrIncompleteChunkHeader = errors.New("export: incomplete chunk header")
	ErrIncompleteChunkBody   = errors.New("export: incomplete chunk body")
	ErrCRCMismatch           = errors.New("export: chunk crc mismatch")
	ErrChunkMissing          = errors.New("export: missing chunk")
	ErrChunkOutOfOrder       = errors.New("export: chunk out of order")
	ErrInterrupted           = errors.New("export: interrupted by failure injection")
)

// Reader 是导出所需的最小快照读接口。
type Reader interface {
	Begin() error
	End()
	Closed() bool
	Version() uint64
	Keys() []string
	OrderedKeys(lex bool) []string
	Get(key string) (value []byte, present, ok bool)
}

// Order 决定键遍历顺序。
type Order string

const (
	OrderLex    Order = "lex"
	OrderRevLex Order = "revlex"
)

// Exporter 执行分块流式导出，同时统计内存峰值。
type Exporter struct {
	r         Reader
	chunkSize int
	order     Order
	PeakBuf   int
}

// New 创建导出器。
func New(r Reader, chunkSize int, order Order) (*Exporter, error) {
	if chunkSize <= 0 {
		return nil, ErrChunkSizeZero
	}
	if order != OrderRevLex {
		order = OrderLex
	}
	return &Exporter{r: r, chunkSize: chunkSize, order: order}, nil
}

func appendFrame(dst []byte, key string, value []byte, present bool) []byte {
	flag := byte(0)
	if present {
		flag = 1
	}
	dst = append(dst, flag)
	var lb [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lb[:], uint64(len(key)))
	dst = append(dst, lb[:n]...)
	dst = append(dst, key...)
	n = binary.PutUvarint(lb[:], uint64(len(value)))
	dst = append(dst, lb[:n]...)
	return append(dst, value...)
}

func (e *Exporter) keys() []string { return e.r.OrderedKeys(e.order == OrderLex) }

func (e *Exporter) track(buf []byte) {
	if len(buf) > e.PeakBuf {
		e.PeakBuf = len(buf)
	}
}

// writeChunk 写入一个块头与块体，返回块描述与块载荷 CRC。
func writeChunk(f *os.File, idx uint32, body []byte) (manifest.Chunk, uint32, int64) {
	c := crc32.ChecksumIEEE(body)
	hdr := make([]byte, headerSize)
	hdr[0], hdr[1] = magic0, magic1
	binary.LittleEndian.PutUint32(hdr[2:], idx)
	binary.LittleEndian.PutUint32(hdr[6:], uint32(len(body)))
	binary.LittleEndian.PutUint32(hdr[10:], c)
	off, _ := f.Seek(0, io.SeekEnd)
	f.Write(hdr)
	f.Write(body)
	return manifest.Chunk{Index: idx, BodyLen: uint32(len(body)), BodyCRC: c}, c, int64(headerSize + len(body))
}
