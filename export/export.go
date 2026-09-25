// Package export 提供分块、可续传、带 CRC 的快照导出。
package export

import (
	"errors"
	"hash/crc32"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"

	"ontology/manifest"
	"ontology/snapshot"
)

// 哨兵错误：四类截断分类及其他可判定错误，均支持 errors.Is。
var (
	ErrManifestIncomplete = errors.New("export: manifest incomplete")
	ErrChunkHeader        = errors.New("export: chunk header incomplete")
	ErrChunkBody          = errors.New("export: chunk body incomplete")
	ErrCRC                = errors.New("export: crc mismatch")
	ErrChunkMissing       = errors.New("export: chunk missing")
	ErrChunkOrder         = errors.New("export: chunk out of order")
	ErrSnapshotExpired    = errors.New("export: snapshot expired")
	ErrInvalidChunkSize   = errors.New("export: chunk size must be > 0")
)

const (
	// ManifestName 与 DataName 是导出目录内的固定文件名。
	ManifestName = "manifest.bin"
	DataName     = "data.bin"
	chunkMagic   = "CHNK"
	chunkHeader  = 16
)

// Options 指定一次导出。
type Options struct {
	Dir       string
	ChunkSize int
	Reverse   bool
}

func orderedKeys(snap *snapshot.Snapshot, reverse bool) []string {
	if reverse {
		return snap.Keys(func(a, b string) bool { return a > b })
	}
	return snap.Keys(nil)
}

// ChunkError 携带块号并包裹分类错误。
type ChunkError struct {
	Index int
	Err   error
}

func (e *ChunkError) Error() string { return e.Err.Error() }
func (e *ChunkError) Unwrap() error { return e.Err }

func chunkErr(i int, err error) error { return &ChunkError{Index: i, Err: err} }

func frame(key string, val []byte) []byte {
	buf := make([]byte, 8+len(key)+len(val))
	putUint32(buf[0:], uint32(len(key)))
	putUint32(buf[4:], uint32(len(val)))
	copy(buf[8:], key)
	copy(buf[8+len(key):], val)
	return buf
}

func entryHash(key string, val []byte) uint64 {
	h := fnv.New64a()
	var lb [4]byte
	putUint32(lb[:], uint32(len(key)))
	h.Write(lb[:])
	putUint32(lb[:], uint32(len(val)))
	h.Write(lb[:])
	io.WriteString(h, key)
	h.Write(val)
	return h.Sum64()
}

func putUint32(b []byte, v uint32) { b[0] = byte(v >> 24); b[1] = byte(v >> 16); b[2] = byte(v >> 8); b[3] = byte(v) }
func getUint32(b []byte) uint32   { return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]) }

// hashSum64 模 2^64 求和天然可交换（顺序无关）。
func addSum(a, b uint64) uint64 { return a + b }

var crcTable = crc32.MakeTable(crc32.IEEE)

type chunker struct {
	f         *os.File
	chunkSize int
	index     int
	buf       []byte
	off       int
	crcs      []uint32
	peak      int
}

func newChunker(f *os.File, size int) *chunker {
	return &chunker{f: f, chunkSize: size, buf: make([]byte, size)}
}

func (c *chunker) write(p []byte) error {
	for len(p) > 0 {
		n := copy(c.buf[c.off:], p)
		c.off += n
		p = p[n:]
		if c.off > c.peak {
			c.peak = c.off
		}
		if c.off == c.chunkSize {
			if err := c.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *chunker) flush() error {
	body := c.buf[:c.off]
	hdr := make([]byte, chunkHeader)
	copy(hdr[0:4], chunkMagic)
	putUint32(hdr[4:], uint32(c.index))
	putUint32(hdr[8:], uint32(c.off))
	hcrc := crc32.Checksum(hdr[:12], crcTable)
	putUint32(hdr[12:], hcrc)
	if _, err := c.f.Write(hdr); err != nil {
		return err
	}
	if _, err := c.f.Write(body); err != nil {
		return err
	}
	c.crcs = append(c.crcs, crc32.Checksum(body, crcTable))
	c.index++
	c.off = 0
	return nil
}

func writePending(dir string, m *manifest.Manifest) error {
	m.Done = false
	tmp := filepath.Join(dir, ManifestName+".tmp")
	if err := os.WriteFile(tmp, m.Marshal(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, ManifestName))
}

func writeFinal(dir string, m *manifest.Manifest) error {
	m.Done = true
	tmp := filepath.Join(dir, ManifestName+".tmp")
	if err := os.WriteFile(tmp, m.Marshal(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, ManifestName))
}
