// Package manifest defines the export manifest (chunk list, total checksum,
// snapshot version) and owns the on-disk file format constants and the
// classifiable corruption errors shared by export and verify.
//
// File layout:
//
//	[magic "ONS1"][manifestLen u32][manifest JSON][chunk]*
//	chunk = [index u32][keyCount u32][bodyLen u32][crc32 u32][body]
//	body  = ([keyLen u16][key][valLen u32][value])*
package manifest

import (
	"encoding/json"
	"errors"
)

// File format constants.
const (
	Magic           = "ONS1"
	FileHeaderSize  = 8  // magic(4) + manifestLen(4)
	ChunkHeaderSize = 16 // index, keyCount, bodyLen, crc32
)

// Classifiable export-file errors, distinguishable with errors.Is.
var (
	ErrManifestIncomplete    = errors.New("export: manifest incomplete")
	ErrChunkHeaderIncomplete = errors.New("export: chunk header incomplete")
	ErrChunkBodyIncomplete   = errors.New("export: chunk body incomplete")
	ErrCRCMismatch           = errors.New("export: crc mismatch")
	ErrChunkMissing          = errors.New("export: chunk missing or out of order")
)

// Chunk describes one chunk: its 1-based index, entry count, body length
// and CRC32 of the body.
type Chunk struct {
	Index    int    `json:"index"`
	KeyCount int    `json:"keyCount"`
	BodyLen  int    `json:"bodyLen"`
	CRC      uint32 `json:"crc"`
}

// Manifest is the export manifest: snapshot version, chunking parameters,
// order-independent total checksum and the chunk list.
type Manifest struct {
	Version   uint64  `json:"version"`
	ChunkSize int     `json:"chunkSize"`
	Keys      int     `json:"keys"`
	TotalCRC  uint32  `json:"totalCRC"`
	Chunks    []Chunk `json:"chunks"`
}

// Encode serializes the manifest to JSON.
func (m *Manifest) Encode() ([]byte, error) {
	return json.Marshal(m)
}

// Decode parses a manifest from JSON.
func Decode(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// DataOffset returns the file offset where chunks begin.
func DataOffset(manifestLen int) int { return FileHeaderSize + manifestLen }

// ChunksBytes returns the total byte count of the first n chunks
// (headers + bodies), used to locate chunk boundaries.
func ChunksBytes(chunks []Chunk, n int) int {
	total := 0
	for i := 0; i < n && i < len(chunks); i++ {
		total += ChunkHeaderSize + chunks[i].BodyLen
	}
	return total
}
