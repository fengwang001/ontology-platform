package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// The functions in this file mutate an already-written export byte-for-byte to
// simulate on-disk damage. They exist for tests and for demos; production
// code never needs them.

// FlipPayloadByte corrupts one byte inside the authenticated payload without
// touching the checksum field, producing a checksum failure.
func FlipPayloadByte(dir, typ string, offset int) error {
	cf, raw, err := readChunk(dir, typ)
	if err != nil {
		return err
	}
	payload, err := hex.DecodeString(cf.Payload)
	if err != nil {
		return err
	}
	if offset < 0 || offset >= len(payload) {
		return fmt.Errorf("offset %d out of range [0,%d)", offset, len(payload))
	}
	payload[offset] ^= 0xFF
	cf.Payload = hex.EncodeToString(payload)
	return writeChunk(dir, typ, cf, raw)
}

// SetDeclaredCount rewrites only the declared_count header, simulating a
// truncated/inflated block whose payload still authenticates.
func SetDeclaredCount(dir, typ string, count int) error {
	cf, raw, err := readChunk(dir, typ)
	if err != nil {
		return err
	}
	cf.Count = count
	return writeChunk(dir, typ, cf, raw)
}

// RemoveTargetRecord removes one record from a trusted block and rewrites the
// checksum so the block stays checksum-valid and count-consistent; references
// pointing at the removed id become genuinely dangling.
func RemoveTargetRecord(dir, typ, recordID string) error {
	cf, raw, err := readChunk(dir, typ)
	if err != nil {
		return err
	}
	payload, err := hex.DecodeString(cf.Payload)
	if err != nil {
		return err
	}
	var records []Record
	if err := json.Unmarshal(payload, &records); err != nil {
		return err
	}
	kept := records[:0]
	removed := false
	for _, r := range records {
		if r.ID == recordID && !removed {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	if !removed {
		return fmt.Errorf("record %q not found in block %q", recordID, typ)
	}
	newPayload, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(newPayload)
	cf.Payload = hex.EncodeToString(newPayload)
	cf.Checksum = hex.EncodeToString(sum[:])
	cf.Count = len(kept)
	return writeChunk(dir, typ, cf, raw)
}

func readChunk(dir, typ string) (*chunkFile, []byte, error) {
	path := chunkPath(dir, typ)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var cf chunkFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		return nil, nil, err
	}
	return &cf, raw, nil
}

func writeChunk(dir, typ string, cf *chunkFile, _ []byte) error {
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(chunkPath(dir, typ), data, 0o644)
}
