// Package manifest defines the export file manifest and layout constants.
package manifest

import "encoding/json"

// HeaderLen is the fixed block header size: index(4)+length(4)+crc(4).
const HeaderLen = 12

// LenLen is the leading big-endian manifest byte length.
const LenLen = 4

// BlockInfo describes one self-describing block in the file.
type BlockInfo struct {
	Index uint32 `json:"index"`
	Len   uint32 `json:"len"`
	CRC   uint32 `json:"crc"`
}

// Manifest is the export checklist.
type Manifest struct {
	Version  uint64      `json:"version"`
	Keys     int         `json:"keys"`
	Checksum uint32      `json:"checksum"`
	Blocks   []BlockInfo `json:"blocks"`
}

// Encode serializes the manifest to JSON.
func Encode(m *Manifest) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// Decode parses a manifest JSON blob.
func Decode(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
