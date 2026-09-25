// Package export 把快照内容分块导出为单个文件，支持按块续传。
package export

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"

	"ontology/manifest"
	"ontology/snapshot"
)

var (
	// ErrInvalidChunkSize 表示块大小非法（<= 0）。
	ErrInvalidChunkSize = errors.New("export: 块大小必须为正")
	// ErrSnapshotExpired 表示续传/导出时快照已关闭。
	ErrSnapshotExpired = errors.New("export: 快照已过期")
	// ErrChunkMismatch 表示重放出的块与清单不符。
	ErrChunkMismatch = errors.New("export: 块与清单不符")
)

// HeaderLen 是块头长度：magic(2)+序号(4)+载荷长(4)+载荷CRC(4)。
const HeaderLen = 14

const chunkMagic = 0x434b // "CK"

// Exporter 在某一快照上执行确定性分块导出。
type Exporter struct {
	snap      *snapshot.Snapshot
	keys      []string
	chunkSize int
	peak      atomic.Int64
	// OnKey 在读完第 i 个键后回调，仅供测试注入故障。
	OnKey func(i int)
}

// New 创建一个导出器；reverse 为 true 时按逆字典序导出。
func New(snap *snapshot.Snapshot, chunkSize int, reverse bool) (*Exporter, error) {
	if chunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}
	if err := snap.Acquire(); err != nil {
		return nil, ErrSnapshotExpired
	}
	keys := snap.Keys()
	if reverse {
		slices.Reverse(keys)
	}
	return &Exporter{snap: snap, keys: keys, chunkSize: chunkSize}, nil
}

// Close 释放快照读取授权。
func (e *Exporter) Close() { e.snap.Release() }

// Peak 返回导出过程中单块缓冲的峰值字节数。
func (e *Exporter) Peak() int64 { return e.peak.Load() }

func encodeEntry(k string, v []byte) []byte {
	b := make([]byte, 8+len(k)+len(v))
	binary.LittleEndian.PutUint32(b[0:4], uint32(len(k)))
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(v)))
	copy(b[8:], k)
	copy(b[8+len(k):], v)
	return b
}

func assemble(idx int, payload []byte) []byte {
	b := make([]byte, HeaderLen+len(payload))
	binary.LittleEndian.PutUint16(b[0:2], chunkMagic)
	binary.LittleEndian.PutUint32(b[2:6], uint32(idx))
	binary.LittleEndian.PutUint32(b[6:10], uint32(len(payload)))
	binary.LittleEndian.PutUint32(b[10:14], crc32.ChecksumIEEE(payload))
	copy(b[HeaderLen:], payload)
	return b
}

// ParseChunk 解析块头；ok=false 表示头不完整或魔数错误。
func ParseChunk(b []byte) (idx, plen int, crc uint32, ok bool) {
	if len(b) < HeaderLen || binary.LittleEndian.Uint16(b[0:2]) != chunkMagic {
		return 0, 0, 0, false
	}
	return int(binary.LittleEndian.Uint32(b[2:6])),
		int(binary.LittleEndian.Uint32(b[6:10])),
		binary.LittleEndian.Uint32(b[10:14]), true
}

// run 确定性重放分块过程，逐块回调 emit；返回重建出的清单。
func (e *Exporter) run(emit func(idx int, chunk []byte, off int64) error) (*manifest.Manifest, error) {
	m := &manifest.Manifest{Version: e.snap.Version(), ChunkSize: e.chunkSize, KeyCount: len(e.keys)}
	var buf []byte
	var off int64
	idx := 0
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		chunk := assemble(idx, buf)
		if err := emit(idx, chunk, off); err != nil {
			return err
		}
		m.Chunks = append(m.Chunks, manifest.Chunk{Index: idx, Offset: off,
			Length: int64(len(chunk)), CRC32: crc32.ChecksumIEEE(buf)})
		off += int64(len(chunk))
		idx++
		buf = buf[:0]
		return nil
	}
	for i, k := range e.keys {
		v, _ := e.snap.Get(k)
		ent := encodeEntry(k, v)
		if len(buf) > 0 && len(buf)+len(ent) > e.chunkSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		buf = append(buf, ent...)
		if cur := int64(len(buf)) + HeaderLen; cur > e.peak.Load() {
			e.peak.Store(cur)
		}
		m.TotalCRC += crc32.ChecksumIEEE(ent)
		if e.OnKey != nil {
			e.OnKey(i)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return m, nil
}

// Export 一次性导出到 path（覆盖写）：先经临时文件收块，再写 清单帧+块区。
func (e *Exporter) Export(path string) (*manifest.Manifest, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".chunks-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	m, err := e.run(func(_ int, chunk []byte, _ int64) error {
		_, err := tmp.Write(chunk)
		return err
	})
	if err != nil {
		tmp.Close()
		return nil, err
	}
	frame, err := m.Encode()
	if err != nil {
		tmp.Close()
		return nil, err
	}
	out, err := os.Create(path)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := out.Write(frame); err != nil {
		out.Close()
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		out.Close()
		tmp.Close()
		return nil, err
	}
	if _, err := io.Copy(out, tmp); err != nil {
		out.Close()
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	return m, out.Close()
}

// Resume 凭清单从第 from 块续传；m 为 nil 时从 path 头部帧自读清单。
func (e *Exporter) Resume(path string, m *manifest.Manifest, from int) error {
	if e.snap.Closed() {
		return ErrSnapshotExpired
	}
	var base int64
	if m == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, base, err = manifest.DecodeFrame(data)
		if err != nil {
			return err
		}
	} else {
		frame, err := m.Encode()
		if err != nil {
			return err
		}
		base = int64(len(frame))
	}
	if from < 0 || from > len(m.Chunks) {
		return fmt.Errorf("%w: 起始块 %d 越界", ErrChunkMismatch, from)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	rebuilt, err := e.run(func(idx int, chunk []byte, off int64) error {
		mc := m.Chunks[idx]
		if mc.Offset != off || mc.Length != int64(len(chunk)) ||
			mc.CRC32 != crc32.ChecksumIEEE(chunk[HeaderLen:]) {
			return fmt.Errorf("%w: 块 %d", ErrChunkMismatch, idx)
		}
		if idx < from {
			return nil
		}
		_, err := f.WriteAt(chunk, base+off)
		return err
	})
	if err != nil {
		return err
	}
	end := base
	if n := len(rebuilt.Chunks); n > 0 {
		end += rebuilt.Chunks[n-1].Offset + rebuilt.Chunks[n-1].Length
	}
	return f.Truncate(end)
}
