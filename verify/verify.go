// Package verify checks the integrity and consistency of an export file:
// manifest completeness, per-chunk header/body completeness, CRC32 of
// every chunk, chunk ordering (missing or misplaced chunks are named),
// and the order-independent total checksum.
package verify

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/manifest"
)

// Verify reads and verifies the export file at path.
func Verify(path string) (*manifest.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return VerifyBytes(data)
}

// VerifyBytes verifies a complete or truncated export file image and
// classifies any defect into the manifest package's checkable errors.
func VerifyBytes(data []byte) (*manifest.Manifest, error) {
	if len(data) < manifest.FileHeaderSize || string(data[0:4]) != manifest.Magic {
		return nil, manifest.ErrManifestIncomplete
	}
	mlen := int(binary.LittleEndian.Uint32(data[4:8]))
	if len(data) < manifest.DataOffset(mlen) {
		return nil, manifest.ErrManifestIncomplete
	}
	m, err := manifest.Decode(data[manifest.FileHeaderSize:manifest.DataOffset(mlen)])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", manifest.ErrManifestIncomplete, err)
	}
	off := manifest.DataOffset(mlen)
	var total uint32
	keys := 0
	for i, c := range m.Chunks {
		if len(data)-off < manifest.ChunkHeaderSize {
			return nil, fmt.Errorf("%w: chunk %d", manifest.ErrChunkHeaderIncomplete, i+1)
		}
		hdr := data[off : off+manifest.ChunkHeaderSize]
		idx := int(binary.LittleEndian.Uint32(hdr[0:4]))
		keyCount := int(binary.LittleEndian.Uint32(hdr[4:8]))
		bodyLen := int(binary.LittleEndian.Uint32(hdr[8:12]))
		crc := binary.LittleEndian.Uint32(hdr[12:16])
		if idx != i+1 {
			return nil, fmt.Errorf("%w: want chunk %d, found chunk %d",
				manifest.ErrChunkMissing, i+1, idx)
		}
		if keyCount != c.KeyCount || bodyLen != c.BodyLen || crc != c.CRC {
			return nil, fmt.Errorf("%w: chunk %d header disagrees with manifest",
				manifest.ErrCRCMismatch, i+1)
		}
		off += manifest.ChunkHeaderSize
		if len(data)-off < bodyLen {
			return nil, fmt.Errorf("%w: chunk %d", manifest.ErrChunkBodyIncomplete, i+1)
		}
		body := data[off : off+bodyLen]
		if crc32.ChecksumIEEE(body) != crc {
			return nil, fmt.Errorf("%w: chunk %d", manifest.ErrCRCMismatch, i+1)
		}
		n, sum, err := walkBody(body)
		if err != nil {
			return nil, fmt.Errorf("%w: chunk %d: %v", manifest.ErrCRCMismatch, i+1, err)
		}
		keys += n
		total ^= sum
		off += bodyLen
	}
	if keys != m.Keys || total != m.TotalCRC {
		return nil, fmt.Errorf("%w: total checksum", manifest.ErrCRCMismatch)
	}
	return m, nil
}

// walkBody parses chunk entries, returning the entry count and the XOR of
// per-entry checksums (the order-independent total checksum contribution).
func walkBody(body []byte) (int, uint32, error) {
	n := 0
	var sum uint32
	for len(body) > 0 {
		if len(body) < 6 {
			return 0, 0, fmt.Errorf("truncated entry header")
		}
		klen := int(binary.LittleEndian.Uint16(body[0:2]))
		vlen := int(binary.LittleEndian.Uint32(body[2:6]))
		if len(body) < 6+klen+vlen {
			return 0, 0, fmt.Errorf("truncated entry")
		}
		entry := append([]byte(nil), body[6:6+klen]...)
		entry = append(entry, 0)
		entry = append(entry, body[6+klen:6+klen+vlen]...)
		sum ^= crc32.ChecksumIEEE(entry)
		n++
		body = body[6+klen+vlen:]
	}
	return n, sum, nil
}
