// Package verify defines all sentinel errors and integrity helpers used by the
// differential sync packages so that failure classes are distinguishable via
// errors.Is.
package verify

import (
	"crypto/sha256"
	"errors"
	"hash/crc32"
)

var (
	// ErrBlockSize: block size is zero or otherwise invalid.
	ErrBlockSize = errors.New("invalid block size")
	// ErrHeader: patch/signature header is incomplete or malformed.
	ErrHeader = errors.New("incomplete or malformed header")
	// ErrTruncated: an instruction record is incomplete.
	ErrTruncated = errors.New("incomplete instruction")
	// ErrData: the data segment is incomplete or over-read.
	ErrData = errors.New("incomplete data segment")
	// ErrCRC: the trailing CRC-32 does not match the payload.
	ErrCRC = errors.New("crc mismatch")
	// ErrIndex: a COPY instruction references an out-of-range block.
	ErrIndex = errors.New("block index out of range")
	// ErrCorrupt: a reused target block fails its strong checksum.
	ErrCorrupt = errors.New("target block corrupted")
	// ErrMismatch: assembled result hash differs from the source hash.
	ErrMismatch = errors.New("result does not match source")
	// ErrSig: signature payload is malformed.
	ErrSig = errors.New("malformed signature")
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// CRC32 returns the IEEE CRC-32 of b.
func CRC32(b []byte) uint32 { return crc32.Checksum(b, crcTable) }

// StrongSum returns the SHA-256 digest of b.
func StrongSum(b []byte) [32]byte { return sha256.Sum256(b) }
