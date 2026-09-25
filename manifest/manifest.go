// Package manifest defines the export manifest: chunk list, total checksum
// and snapshot version, persisted atomically as JSON.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the manifest file name inside an export directory.
const FileName = "manifest.json"

// ChunkMeta describes one exported chunk.
type ChunkMeta struct {
	Index int    `json:"index"`
	Keys  int    `json:"keys"`
	Bytes int    `json:"bytes"`
	CRC   uint32 `json:"crc"` // crc32 of the chunk body
	Sum   uint32 `json:"sum"` // sum of per-entry crc32, order-independent
}

// Manifest describes an export.
type Manifest struct {
	Version   uint64      `json:"version"` // snapshot version watermark
	ChunkSize int         `json:"chunk_size"`
	Reverse   bool        `json:"reverse"`
	Chunks    []ChunkMeta `json:"chunks"`
	TotalKeys int         `json:"total_keys"`
	TotalCRC  uint32      `json:"total_crc"` // order-independent aggregate
	Complete  bool        `json:"complete"`
}

// ChunkFile returns the file name of chunk i.
func ChunkFile(i int) string { return fmt.Sprintf("chunk-%06d", i) }

// Load reads the manifest from dir. A missing or unparsable file is an error.
func Load(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Save atomically writes the manifest into dir (tmp file + rename).
func Save(dir string, m *Manifest) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, FileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, FileName))
}
