// Package verify checks the integrity and consistency of an export
// directory: manifest, chunk headers/bodies/CRCs, chunk sequence and the
// order-independent total checksum.
package verify

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"ontology/export"
	"ontology/manifest"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrManifestIncomplete = errors.New("verify: manifest incomplete")
	ErrExportIncomplete   = errors.New("verify: export incomplete")
	ErrHeaderIncomplete   = errors.New("verify: chunk header incomplete")
	ErrBodyIncomplete     = errors.New("verify: chunk body incomplete")
	ErrCRCMismatch        = errors.New("verify: crc mismatch")
	ErrChunkMissing       = errors.New("verify: chunk missing")
	ErrChunkOrder         = errors.New("verify: chunk out of order")
	ErrTotalMismatch      = errors.New("verify: total checksum mismatch")
)

// Dir verifies a finished export directory.
func Dir(dir string) error {
	m, err := manifest.Load(dir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrManifestIncomplete, err)
	}
	if !m.Complete {
		return ErrExportIncomplete
	}
	keys, total := 0, uint32(0)
	for i, meta := range m.Chunks {
		k, sum, err := chunk(dir, i, meta)
		if err != nil {
			return err
		}
		keys += k
		total += sum
	}
	if keys != m.TotalKeys || total != m.TotalCRC {
		return fmt.Errorf("%w: keys %d/%d crc %08x/%08x",
			ErrTotalMismatch, keys, m.TotalKeys, total, m.TotalCRC)
	}
	return nil
}

func chunk(dir string, idx int, meta manifest.ChunkMeta) (int, uint32, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifest.ChunkFile(idx)))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: chunk %d", ErrChunkMissing, idx)
	}
	if len(b) < export.HeaderSize {
		return 0, 0, fmt.Errorf("%w: chunk %d", ErrHeaderIncomplete, idx)
	}
	if string(b[:4]) != export.Magic {
		return 0, 0, fmt.Errorf("%w: chunk %d: bad magic", ErrHeaderIncomplete, idx)
	}
	if got := int(binary.BigEndian.Uint32(b[4:8])); got != idx {
		return 0, 0, fmt.Errorf("%w: chunk %d holds index %d", ErrChunkOrder, idx, got)
	}
	bodyLen := int(binary.BigEndian.Uint32(b[12:16]))
	if len(b) < export.HeaderSize+bodyLen {
		return 0, 0, fmt.Errorf("%w: chunk %d", ErrBodyIncomplete, idx)
	}
	if len(b) < export.HeaderSize+bodyLen+export.TrailerSize {
		return 0, 0, fmt.Errorf("%w: chunk %d: crc trailer cut", ErrCRCMismatch, idx)
	}
	body := b[export.HeaderSize : export.HeaderSize+bodyLen]
	crc := binary.BigEndian.Uint32(b[export.HeaderSize+bodyLen:])
	if crc32.ChecksumIEEE(body) != crc || crc != meta.CRC {
		return 0, 0, fmt.Errorf("%w: chunk %d", ErrCRCMismatch, idx)
	}
	keys, sum, err := entries(body)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: chunk %d: %v", ErrBodyIncomplete, idx, err)
	}
	if keys != meta.Keys {
		return 0, 0, fmt.Errorf("%w: chunk %d: key count %d != %d",
			ErrBodyIncomplete, idx, keys, meta.Keys)
	}
	return keys, sum, nil
}

func entries(body []byte) (int, uint32, error) {
	keys := 0
	sum := uint32(0)
	for len(body) > 0 {
		if len(body) < 4 {
			return 0, 0, errors.New("short key length")
		}
		klen := int(binary.BigEndian.Uint32(body))
		body = body[4:]
		if len(body) < klen+4 {
			return 0, 0, errors.New("short key/value")
		}
		key := body[:klen]
		body = body[klen:]
		vlen := int(binary.BigEndian.Uint32(body))
		body = body[4:]
		if len(body) < vlen {
			return 0, 0, errors.New("short value")
		}
		sum += crc32.ChecksumIEEE(append(append([]byte(string(key)), 0), body[:vlen]...))
		body = body[vlen:]
		keys++
	}
	return keys, sum, nil
}
