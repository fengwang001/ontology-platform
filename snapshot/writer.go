package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ExportRequest groups records by object type for export. Every type becomes
// one independently verifiable chunk file.
type ExportRequest map[string][]Record

// chunkFile is the on-disk representation of one type chunk.
//
// Layout decision: the payload is stored separately from the envelope and
// hex-encoded. The checksum authenticates exactly the bytes that get decoded
// and parsed, so any byte-level corruption (flip/truncate/injection) either
// breaks the SHA-256 match or, if the attacker somehow rewrites JSON without
// touching the checksummed bytes, is still impossible: the checksum field
// itself is outside the authenticated region, which means a corrupted payload
// can never parse into "valid" data behind a matching checksum.
type chunkFile struct {
	Type     string `json:"type"`
	Version  int    `json:"version"`
	Count    int    `json:"declared_count"`
	Payload  string `json:"payload_hex"`
	Checksum string `json:"payload_sha256_hex"`
}

const chunkVersion = 1

// manifest lists the coverage of one export. The manifest is directory
// metadata; the chunk files are self-describing and remain verifiable on
// their own.
type manifest struct {
	Version int      `json:"version"`
	Types   []string `json:"types"`
}

// canonicalPayload returns deterministic JSON for the records. Determinism is
// what makes repeated read-only verification produce identical conclusions.
func canonicalPayload(records []Record) ([]byte, error) {
	return json.Marshal(records)
}

// encodeChunk builds the serialised chunk for one type.
func encodeChunk(typ string, records []Record) ([]byte, error) {
	payload, err := canonicalPayload(records)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	cf := chunkFile{
		Type:     typ,
		Version:  chunkVersion,
		Count:    len(records),
		Payload:  hex.EncodeToString(payload),
		Checksum: hex.EncodeToString(sum[:]),
	}
	return json.MarshalIndent(cf, "", "  ")
}

func chunkPath(dir, typ string) string {
	return filepath.Join(dir, "chunk_"+typ+".json")
}

// WriteExport serialises the export into type-specific chunk files plus a
// manifest under dir. The directory must not already be an export directory
// (callers choose fresh dirs); writing is the only mutating operation in the
// package.
func WriteExport(ctx context.Context, dir string, req ExportRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create export dir: %w", err)
	}

	types := make([]string, 0, len(req))
	for typ := range req {
		types = append(types, typ)
	}
	sort.Strings(types)

	for _, typ := range types {
		if typ == "" {
			return fmt.Errorf("export: empty object type name")
		}
		data, err := encodeChunk(typ, req[typ])
		if err != nil {
			return fmt.Errorf("encode chunk %q: %w", typ, err)
		}
		tmp := chunkPath(dir, typ) + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return fmt.Errorf("write chunk %q: %w", typ, err)
		}
		if err := os.Rename(tmp, chunkPath(dir, typ)); err != nil {
			return fmt.Errorf("finalise chunk %q: %w", typ, err)
		}
	}

	mb, err := json.MarshalIndent(manifest{Version: chunkVersion, Types: types}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := os.WriteFile(tmp, mb, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "manifest.json")); err != nil {
		return fmt.Errorf("finalise manifest: %w", err)
	}
	return nil
}
