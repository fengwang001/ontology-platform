package export

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"ontology/manifest"
)

// File 保存一次导出的上下文。
type File struct {
	Name string
	M    *manifest.Manifest
	exp  *Exporter
}

// Export 从头执行一次完整导出。failAfter>=0 时在写完该块号后注入中断。
func (e *Exporter) Export(path string, failAfter int) (*File, error) {
	if err := e.r.Begin(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSnapshotExpired, err)
	}
	defer e.r.End()

	keys := e.keys()
	reserve := manifest.ReserveSize(e.chunkSize, len(keys))
	m := &manifest.Manifest{
		Version:    e.r.Version(),
		ChunkSize:  e.chunkSize,
		Order:      string(e.order),
		KeyCount:   len(keys),
		Complete:   false,
		Reserve:    reserve,
		Chunks:     []manifest.Chunk{},
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := manifest.WriteRegion(f, m); err != nil {
		return nil, err
	}
	f.Truncate(int64(manifest.RegionSize(reserve)))

	ff := &File{Name: path, M: m, exp: e}
	if err := e.stream(f, m, keys, 0, 0, 0, failAfter); err != nil {
		return ff, err
	}
	return ff, nil
}

func (e *Exporter) stream(f *os.File, m *manifest.Manifest, keys []string,
	fromChunk uint32, totalBytes int64, totalCRC uint32, failAfter int) error {
	var buf []byte
	idx := fromChunk
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		e.track(buf)
		c, ccr, n := writeChunk(f, idx, buf)
		if err := f.Sync(); err != nil {
			return err
		}
		m.Chunks = append(m.Chunks, c)
		m.TotalBytes += n
		m.TotalCRC ^= ccr
		if err := manifest.WriteRegion(f, m); err != nil {
			return err
		}
		buf = buf[:0]
		idx++
		if int(c.Index) == failAfter {
			return ErrInterrupted
		}
		return nil
	}
	for _, key := range keys {
		if e.r.Closed() {
			return ErrSnapshotExpired
		}
		val, present, _ := e.r.Get(key)
		if !present {
			continue
		}
		frame := appendFrame(nil, key, val, true)
		totalBytes += int64(len(frame))
		totalCRC ^= crc32.ChecksumIEEE(frame)
		if len(buf) > 0 && len(buf)+len(frame) > e.chunkSize {
			if err := flush(); err != nil {
				return err
			}
		}
		buf = append(buf, frame...)
		if len(buf) >= e.chunkSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	m.TotalBytes = totalBytes
	m.TotalCRC = totalCRC
	return nil
}

// Resume 凭清单从第 k 块续传。k 必须等于已确认块数。
func (e *Exporter) Resume(path string) (*File, error) {
	if err := e.r.Begin(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSnapshotExpired, err)
	}
	defer e.r.End()

	m, off, err := inspect(path)
	if err != nil {
		return nil, err
	}
	if m.Complete {
		return &File{Name: path, M: m, exp: e}, nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := f.Truncate(off); err != nil {
		return nil, err
	}
	keys := e.keys()
	if err := e.stream(f, m, keys, uint32(len(m.Chunks)), 0, 0, -1); err != nil {
		return &File{Name: path, M: m, exp: e}, err
	}
	m.Complete = true
	if err := manifest.WriteRegion(f, m); err != nil {
		return nil, err
	}
	return &File{Name: path, M: m, exp: e}, nil
}

func inspect(path string) (*manifest.Manifest, int64, error) {
	f, err := os.Open(path)
	if err != nil {
	return nil, 0, err
	}
	defer f.Close()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrIncompleteManifest, err)
	}
	n := int(binary.LittleEndian.Uint32(hdr))
	if n <= 0 {
		return nil, 0, ErrIncompleteManifest
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrIncompleteManifest, err)
	}
	m, err := manifest.Unmarshal(append(hdr, raw...))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrIncompleteManifest, err)
	}
	off := int64(manifest.RegionSize(m.Reserve))
	for i := range m.Chunks {
		next, err := verifyChunkAt(f, off, i, m.Chunks[i])
		if err != nil {
			return nil, off, err
		}
		off = next
	}
	return m, off, nil
}

func verifyChunkAt(f *os.File, off int64, i int, c manifest.Chunk) (int64, error) {
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(f, hdr[:0]); err != nil {
		_ = err
	}
	if n, _ := f.ReadAt(hdr, off); n < headerSize {
		return 0, fmt.Errorf("%w: chunk %d header (%d bytes)", ErrIncompleteChunkHeader, i, n)
	}
	if hdr[0] != magic0 || hdr[1] != magic1 {
		return 0, fmt.Errorf("%w: chunk %d bad magic", ErrChunkOutOfOrder, i)
	}
	idx := binary.LittleEndian.Uint32(hdr[2:])
	blen := binary.LittleEndian.Uint32(hdr[6:])
	crcv := binary.LittleEndian.Uint32(hdr[10:])
	if int(idx) != i {
		return 0, fmt.Errorf("%w: want %d got %d", ErrChunkOutOfOrder, i, idx)
	}
	body := make([]byte, blen)
	n, err := f.ReadAt(body, off+headerSize)
	if err != nil && err != io.EOF && n < int(blen) {
		return 0, fmt.Errorf("%w: chunk %d", ErrIncompleteChunkBody, i)
	}
	if n < int(blen) {
		return 0, fmt.Errorf("%w: chunk %d (%d/%d bytes)", ErrIncompleteChunkBody, i, n, blen)
	}
	if crc32.ChecksumIEEE(body) != crcv || c.BodyCRC != crcv || c.BodyLen != blen {
		return 0, fmt.Errorf("%w: chunk %d", ErrCRCMismatch, i)
	}
	return off + headerSize + int64(blen), nil
}
