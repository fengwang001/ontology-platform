// Package diffsync implements a block-signature based differential
// synchronizer: the receiver builds a signature of its old file, the
// sender uses it to produce a delta patch for the new file, and the
// receiver applies the patch atomically.
package diffsync

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// StrongSize is the byte length of the strong checksum (SHA-256).
const StrongSize = sha256.Size

// ErrInvalidBlockSize is returned when a non-positive block size is used.
var ErrInvalidBlockSize = errors.New("diffsync: block size must be positive")

// BlockSig is the signature of a single block of the old file.
type BlockSig struct {
	Index  int              // block index, 0-based
	Len    int              // block length; the last block may be short
	Weak   uint32           // weak rolling checksum (byte sum)
	Strong [StrongSize]byte // strong checksum (SHA-256)
}

// Signature is the immutable block-signature set of the old file. It is
// safe for concurrent use by any number of delta generators.
type Signature struct {
	BlockSize  int
	FileLen    int64
	FileStrong [StrongSize]byte // strong checksum of the whole old file
	Blocks     []BlockSig

	byWeak map[uint32][]int // weak checksum -> block indices, ascending
}

// weakSum is the weak checksum: a plain byte sum. It is cheap to roll
// (subtract the outgoing byte, add the incoming one) but collision-prone,
// which is exactly why every weak hit must be confirmed by the strong
// checksum before a block may be reused.
func weakSum(b []byte) uint32 {
	var s uint32
	for _, c := range b {
		s += uint32(c)
	}
	return s
}

// strongSum is the strong checksum: SHA-256.
func strongSum(b []byte) [StrongSize]byte {
	return sha256.Sum256(b)
}

// NumBlocks returns the number of blocks a file of length fileLen is
// split into with the given block size.
func NumBlocks(fileLen int64, blockSize int) int {
	if fileLen <= 0 {
		return 0
	}
	return int((fileLen + int64(blockSize) - 1) / int64(blockSize))
}

// BuildSignature splits data into fixed-size blocks (the last block may
// be short) and computes the weak and strong checksum of each block.
func BuildSignature(data []byte, blockSize int) (*Signature, error) {
	if blockSize <= 0 {
		return nil, fmt.Errorf("%w, got %d", ErrInvalidBlockSize, blockSize)
	}
	sig := &Signature{
		BlockSize:  blockSize,
		FileLen:    int64(len(data)),
		FileStrong: strongSum(data),
		byWeak:     make(map[uint32][]int),
	}
	for off := 0; off < len(data); off += blockSize {
		end := off + blockSize
		if end > len(data) {
			end = len(data)
		}
		blk := data[off:end]
		bs := BlockSig{
			Index:  len(sig.Blocks),
			Len:    len(blk),
			Weak:   weakSum(blk),
			Strong: strongSum(blk),
		}
		sig.Blocks = append(sig.Blocks, bs)
		sig.byWeak[bs.Weak] = append(sig.byWeak[bs.Weak], bs.Index)
	}
	return sig, nil
}
