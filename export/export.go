// Package export writes a snapshot to a chunked, self-describing file.
// Each chunk carries its own header and CRC32; the manifest at the file
// head records the snapshot version, the chunk list and an
// order-independent total checksum. Export is atomic (tmp file + rename);
// Resume continues an interrupted export from any chunk boundary.
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

// Classifiable export errors, distinguishable with errors.Is.
var (
	ErrInvalidChunkSize = errors.New("export: chunk size must be positive")
	ErrSnapshotExpired  = errors.New("export: snapshot expired or version mismatch")
)

// Stats reports export resource usage.
type Stats struct {
	Keys           int // keys read (exactly one Get per key)
	Chunks         int // chunks written by this call
	PeakChunkBytes int // largest chunk body buffer held in memory
}

// Result couples the produced manifest with resource stats.
type Result struct {
	Manifest *manifest.Manifest
	Stats    Stats
}

func entrySize(key string, val []byte) int { return 2 + len(key) + 4 + len(val) }

func appendEntry(buf []byte, key string, val []byte) []byte {
	var hdr [6]byte
	binary.LittleEndian.PutUint16(hdr[0:2], uint16(len(key)))
	binary.LittleEndian.PutUint32(hdr[2:6], uint32(len(val)))
	buf = append(buf, hdr[:]...)
	buf = append(buf, key...)
	return append(buf, val...)
}

func writeChunk(w io.Writer, index, keyCount int, body []byte) (manifest.Chunk, error) {
	var hdr [manifest.ChunkHeaderSize]byte
	crc := crc32.ChecksumIEEE(body)
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(index))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(keyCount))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(body)))
	binary.LittleEndian.PutUint32(hdr[12:16], crc)
	if _, err := w.Write(hdr[:]); err != nil {
		return manifest.Chunk{}, err
	}
	_, err := w.Write(body)
	return manifest.Chunk{Index: index, KeyCount: keyCount, BodyLen: len(body), CRC: crc}, err
}

// streamChunks reads keys[skip:] from snap (one Get each) and writes chunks
// numbered from start. It never holds more than one chunk body in memory.
func streamChunks(snap *snapshot.Snapshot, keys []string, skip, start, chunkSize int, w io.Writer) ([]manifest.Chunk, uint32, Stats, error) {
	var metas []manifest.Chunk
	var total uint32
	var stats Stats
	body := make([]byte, 0, chunkSize)
	keyCount, index := 0, start
	flush := func() error {
		if keyCount == 0 {
			return nil
		}
		c, err := writeChunk(w, index, keyCount, body)
		if err != nil {
			return err
		}
		metas, stats.Chunks = append(metas, c), stats.Chunks+1
		body, keyCount, index = body[:0], 0, index+1
		return nil
	}
	for _, key := range keys[skip:] {
		val, ok, err := snap.Get(key)
		if err != nil {
			return nil, 0, stats, err
		}
		if !ok {
			continue
		}
		stats.Keys++
		total ^= crc32.ChecksumIEEE(append(append([]byte(key), 0), val...))
		if keyCount > 0 && len(body)+entrySize(key, val) > chunkSize {
			if err := flush(); err != nil {
				return nil, 0, stats, err
			}
		}
		body = appendEntry(body, key, val)
		keyCount++
		if len(body) > stats.PeakChunkBytes {
			stats.PeakChunkBytes = len(body)
		}
	}
	if err := flush(); err != nil {
		return nil, 0, stats, err
	}
	return metas, total, stats, nil
}

// writeFile writes header + manifest + the first nchunks of bodyPath to
// path, atomically via a temp file and rename.
func writeFile(path string, m *manifest.Manifest, bodyPath string, nchunks int) error {
	mb, err := m.Encode()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var hdr [manifest.FileHeaderSize]byte
	copy(hdr[0:4], manifest.Magic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(mb)))
	if _, err = f.Write(hdr[:]); err == nil {
		_, err = f.Write(mb)
	}
	if err == nil && nchunks > 0 {
		var bf *os.File
		if bf, err = os.Open(bodyPath); err == nil {
			_, err = io.CopyN(f, bf, int64(manifest.ChunksBytes(m.Chunks, nchunks)))
			bf.Close()
		}
	}
	cerr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if cerr != nil {
		os.Remove(tmp)
		return cerr
	}
	return os.Rename(tmp, path)
}

// Export writes the full snapshot to path. order lists the keys to export
// (typically snap.Keys(), any order yields the same content set).
func Export(snap *snapshot.Snapshot, path string, order []string, chunkSize int) (*Result, error) {
	return runExport(snap, path, order, chunkSize, -1)
}

// ExportPartial simulates an interrupted export: the file at path contains
// the full manifest but only the first nchunks chunks.
func ExportPartial(snap *snapshot.Snapshot, path string, order []string, chunkSize, nchunks int) (*Result, error) {
	return runExport(snap, path, order, chunkSize, nchunks)
}

func runExport(snap *snapshot.Snapshot, path string, order []string, chunkSize, limit int) (*Result, error) {
	if chunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}
	bodyPath := path + ".body"
	bf, err := os.Create(bodyPath)
	if err != nil {
		return nil, err
	}
	metas, total, stats, err := streamChunks(snap, order, 0, 1, chunkSize, bf)
	if cerr := bf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(bodyPath)
		return nil, err
	}
	m := &manifest.Manifest{Version: snap.Version(), ChunkSize: chunkSize,
		Keys: stats.Keys, TotalCRC: total, Chunks: metas}
	n := len(metas)
	if limit >= 0 && limit < n {
		n = limit
	}
	err = writeFile(path, m, bodyPath, n)
	os.Remove(bodyPath)
	if err != nil {
		return nil, err
	}
	return &Result{Manifest: m, Stats: stats}, nil
}

// Resume continues an interrupted export at path. It refuses to mix
// moments: the snapshot must be open and match the manifest version.
// Already-exported chunks are verified and left untouched.
func Resume(snap *snapshot.Snapshot, path string, order []string, chunkSize int) (*Result, error) {
	if chunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, mlen, err := parseHead(data)
	if err != nil {
		return nil, err
	}
	if snap.Closed() || snap.Version() != m.Version {
		return nil, ErrSnapshotExpired
	}
	off := manifest.DataOffset(mlen)
	present := 0
	for _, c := range m.Chunks {
		end := off + manifest.ChunkHeaderSize + c.BodyLen
		if end > len(data) || crc32.ChecksumIEEE(data[off+manifest.ChunkHeaderSize:end]) != c.CRC {
			break
		}
		off, present = end, present+1
	}
	if present == len(m.Chunks) {
		return &Result{Manifest: m}, nil
	}
	if err := os.Truncate(path, int64(off)); err != nil {
		return nil, err
	}
	skip := 0
	for _, c := range m.Chunks[:present] {
		skip += c.KeyCount
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	_, _, stats, err := streamChunks(snap, order, skip, present+1, chunkSize, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return &Result{Manifest: m, Stats: stats}, nil
}

func parseHead(data []byte) (*manifest.Manifest, int, error) {
	if len(data) < manifest.FileHeaderSize || string(data[0:4]) != manifest.Magic {
		return nil, 0, manifest.ErrManifestIncomplete
	}
	mlen := int(binary.LittleEndian.Uint32(data[4:8]))
	if len(data) < manifest.DataOffset(mlen) {
		return nil, 0, manifest.ErrManifestIncomplete
	}
	m, err := manifest.Decode(data[manifest.FileHeaderSize:manifest.DataOffset(mlen)])
	if err != nil {
		return nil, 0, manifest.ErrManifestIncomplete
	}
	return m, mlen, nil
}
