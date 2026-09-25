// Package export writes a consistent snapshot as self-describing CRC32-protected chunks plus a manifest, resumable from any chunk.
package export

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"

	"ontology/manifest"
	"ontology/snapshot"
)

var (
	ErrChunkSizeZero   = errors.New("export: chunk size must be positive")
	ErrSnapshotExpired = errors.New("export: snapshot expired")
	ErrVersionMismatch = errors.New("export: manifest/version mismatch")
	ErrChunkCorrupt    = errors.New("export: existing chunk corrupt")
)

const Magic = "CHNK"
const HeaderSize, TrailerSize = 16, 4

// Options configures an Exporter. OnKey/OnChunk are test hooks.
type Options struct {
	Dir       string
	ChunkSize int
	Reverse   bool                // export keys in reverse lexicographic order
	OnKey     func(n int)         // called after the n-th key read (1-based)
	OnChunk   func(idx int) error // called after chunk idx is persisted
}

// Stats reports exporter counters; PeakChunkBytes is the peak chunk body buffer.
type Stats struct {
	Keys, Chunks, PeakChunkBytes int
}

// Exporter exports one snapshot into a directory.
type Exporter struct {
	snap  *snapshot.Snapshot
	opts  Options
	stats Stats
}

// New validates opts, creates opts.Dir, and returns an Exporter for snap.
func New(snap *snapshot.Snapshot, opts Options) (*Exporter, error) {
	if opts.ChunkSize <= 0 {
		return nil, ErrChunkSizeZero
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	return &Exporter{snap: snap, opts: opts}, nil
}

// Stats returns the exporter counters.
func (e *Exporter) Stats() Stats { return e.stats }

// Run performs or resumes the export and returns the final manifest.
func (e *Exporter) Run() (*manifest.Manifest, error) {
	keys, err := e.snap.Keys()
	if err != nil {
		return nil, mapErr(err)
	}
	slices.Sort(keys)
	if e.opts.Reverse {
		slices.Reverse(keys)
	}
	m, err := e.resumeState()
	if err != nil {
		return nil, err
	}
	if m.Complete {
		return m, nil
	}
	skip := 0
	for _, c := range m.Chunks {
		skip += c.Keys
	}
	var buf []byte
	nkeys := 0
	var sum uint32
	flush := func() error {
		if nkeys == 0 {
			return nil
		}
		idx := len(m.Chunks)
		if err := writeChunk(e.opts.Dir, idx, nkeys, buf); err != nil {
			return err
		}
		m.Chunks = append(m.Chunks, manifest.ChunkMeta{
			Index: idx, Keys: nkeys, Bytes: HeaderSize + len(buf) + TrailerSize,
			CRC: crc32.ChecksumIEEE(buf), Sum: sum,
		})
		e.stats.Chunks++
		if err := manifest.Save(e.opts.Dir, m); err != nil {
			return err
		}
		buf, nkeys, sum = nil, 0, 0
		if e.opts.OnChunk != nil {
			return e.opts.OnChunk(idx)
		}
		return nil
	}
	for _, k := range keys[skip:] {
		v, _, err := e.snap.Get(k)
		if err != nil {
			return nil, mapErr(err)
		}
		e.stats.Keys++
		if e.opts.OnKey != nil {
			e.opts.OnKey(e.stats.Keys)
		}
		ent := encodeEntry(k, v)
		if len(buf) > 0 && len(buf)+len(ent) > e.opts.ChunkSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		buf = append(buf, ent...)
		sum += crc32.ChecksumIEEE(append(append([]byte(k), 0), v...))
		nkeys++
		if len(buf) > e.stats.PeakChunkBytes {
			e.stats.PeakChunkBytes = len(buf)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	for _, c := range m.Chunks {
		m.TotalKeys += c.Keys
		m.TotalCRC += c.Sum
	}
	m.Complete = true
	if err := manifest.Save(e.opts.Dir, m); err != nil {
		return nil, err
	}
	return m, nil
}

func mapErr(err error) error {
	if errors.Is(err, snapshot.ErrClosed) {
		return ErrSnapshotExpired
	}
	return err
}

func (e *Exporter) resumeState() (*manifest.Manifest, error) {
	m, err := manifest.Load(e.opts.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("export: %w", err)
		}
		return &manifest.Manifest{Version: e.snap.Version(),
			ChunkSize: e.opts.ChunkSize, Reverse: e.opts.Reverse}, nil
	}
	if m.Complete {
		return m, nil
	}
	if m.Version != e.snap.Version() || m.ChunkSize != e.opts.ChunkSize ||
		m.Reverse != e.opts.Reverse {
		return nil, ErrVersionMismatch
	}
	for _, c := range m.Chunks {
		b, err := os.ReadFile(filepath.Join(e.opts.Dir, manifest.ChunkFile(c.Index)))
		if err != nil || len(b) != c.Bytes || string(b[:4]) != Magic ||
			binary.BigEndian.Uint32(b[4:8]) != uint32(c.Index) ||
			crc32.ChecksumIEEE(b[HeaderSize:len(b)-TrailerSize]) != c.CRC {
			return nil, ErrChunkCorrupt
		}
	}
	return m, nil
}

func encodeEntry(key string, val []byte) []byte {
	b := make([]byte, 8+len(key)+len(val))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(key)))
	copy(b[4:], key)
	binary.BigEndian.PutUint32(b[4+len(key):], uint32(len(val)))
	copy(b[8+len(key):], val)
	return b
}

func writeChunk(dir string, idx, nkeys int, body []byte) error {
	b := make([]byte, 0, HeaderSize+len(body)+TrailerSize)
	b = append(b, Magic...)
	b = binary.BigEndian.AppendUint32(b, uint32(idx))
	b = binary.BigEndian.AppendUint32(b, uint32(nkeys))
	b = binary.BigEndian.AppendUint32(b, uint32(len(body)))
	b = append(b, body...)
	b = binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(body))
	tmp := filepath.Join(dir, manifest.ChunkFile(idx)+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, manifest.ChunkFile(idx)))
}
