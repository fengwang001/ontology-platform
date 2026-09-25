// Package export 在并发写入下导出某一时刻的一致快照，支持分块与任意块续传。
package export

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"ontology/manifest"
	"ontology/snapshot"
)

var (
	// ErrBadBlockSize 表示块大小为 0 或负数。
	ErrBadBlockSize = errors.New("bad block size")
	// ErrSnapshotExpired 表示续传所依据的快照已关闭（拒绝用当前数据拼接）。
	ErrSnapshotExpired = errors.New("snapshot expired")
)

const (
	magic      = "ONEX"
	headerSize = 16
)

// Exporter 绑定一个只读快照进行分块导出。
type Exporter struct {
	snap      *snapshot.Snapshot
	blockSize int

	order       []string
	peakPayload int
}

// New 创建导出器；order 为键的导出顺序（nil 用字典序），blockSize 为块目标字节数。
func New(snap *snapshot.Snapshot, order []string, blockSize int) (*Exporter, error) {
	if blockSize <= 0 {
		return nil, ErrBadBlockSize
	}
	keys := snap.Keys()
	want := keys
	if order != nil {
		want = append([]string(nil), order...)
	}
	return &Exporter{snap: snap, blockSize: blockSize, order: want}, nil
}

// PeakPayloadBytes 返回导出过程中单块驻留的 payload 峰值字节数。
func (e *Exporter) PeakPayloadBytes() int { return e.peakPayload }

func encodeRecord(dst []byte, key string, value []byte) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(key)))
	dst = append(dst, buf[:]...)
	dst = append(dst, key...)
	binary.BigEndian.PutUint32(buf[:], uint32(len(value)))
	dst = append(dst, buf[:]...)
	dst = append(dst, value...)
	return dst
}

// plan 按导出顺序读取每个键一次并贪心打包成块，同时汇总集合哈希。
func (e *Exporter) plan() ([][]byte, *manifest.Manifest) {
	blocks := [][]byte{{}}
	var root [32]byte
	cur := 0
	for _, k := range e.order {
		ent := e.snap.Get(k)
		d := manifest.RecordDigest(k, ent.Value)
		manifest.XOR(&root, &d)
		rec := encodeRecord(nil, k, ent.Value)
		if cur > 0 && cur+len(rec) > e.blockSize {
			blocks = append(blocks, []byte{})
			cur = 0
		}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], rec...)
		cur += len(rec)
		if len(rec) > e.peakPayload {
			e.peakPayload = len(rec)
		}
		if cur > e.peakPayload {
			e.peakPayload = cur
		}
	}
	if len(blocks[0]) == 0 {
		blocks = blocks[:0]
	}
	m := &manifest.Manifest{
		Version:    e.snap.Version(),
		KeyCount:   len(e.order),
		BlockSize:  e.blockSize,
		BlockCount: len(blocks),
		RootHash:   manifest.RootHash(&root),
		Blocks:     make([]manifest.Block, len(blocks)),
	}
	var off int64
	for i, b := range blocks {
		m.Blocks[i] = manifest.Block{Index: i, Offset: off, Length: len(b)}
		off += int64(headerSize + len(b))
	}
	return blocks, m
}

func writeHeader(w io.Writer, index int, body []byte) error {
	hdr := make([]byte, headerSize)
	copy(hdr[0:4], magic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(index))
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(body)))
	binary.BigEndian.PutUint32(hdr[12:16], crc32.ChecksumIEEE(body))
	_, err := w.Write(hdr)
	return err
}

// Run 执行导出：dir 为输出目录，name 为目标文件名；存在有效前缀则从其后续传。
func (e *Exporter) Run(dir, name string) (*manifest.Manifest, error) {
	if !e.snap.Live() {
		return nil, ErrSnapshotExpired
	}
	if !e.snap.Begin() {
		return nil, ErrSnapshotExpired
	}
	defer e.snap.Done()

	blocks, m := e.plan()
	line, err := manifest.Encode(m)
	if err != nil {
		return nil, err
	}
	start := 0
	if existing, perr := os.ReadFile(filepath.Join(dir, name)); perr == nil {
		start = validPrefix(existing, line, m)
	}

	tmp, err := os.CreateTemp(dir, ".export-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if start > 0 {
		old, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if _, err := io.CopyN(tmp, old, int64(len(line))+m.Blocks[start-1].Offset+
			int64(headerSize+m.Blocks[start-1].Length)); err != nil {
			old.Close()
			return nil, err
		}
		old.Close()
	} else {
		if _, err := tmp.Write(line); err != nil {
			return nil, err
		}
	}
	for i := start; i < len(blocks); i++ {
		if err := writeHeader(tmp, i, blocks[i]); err != nil {
			return nil, err
		}
		if _, err := tmp.Write(blocks[i]); err != nil {
			return nil, err
		}
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return nil, err
	}
	cleanup = false
	return m, nil
}

// validPrefix 返回已可复用的完整块个数（其后块缺失或 CRC 不符即续传起点）。
func validPrefix(data, line []byte, m *manifest.Manifest) int {
	if len(data) < len(line) {
		return 0
	}
	pos := int64(len(line))
	for i := 0; i < m.BlockCount; i++ {
		b := m.Blocks[i]
		end := pos + int64(headerSize+b.Length)
		if int64(len(data)) < end {
			return i
		}
		body := data[pos+headerSize : pos+int64(headerSize+b.Length)]
		if binary.BigEndian.Uint32(data[pos+12:pos+16]) != crc32.ChecksumIEEE(body) {
			return i
		}
		pos = end
	}
	return m.BlockCount
}
