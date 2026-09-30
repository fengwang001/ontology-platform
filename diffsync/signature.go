// Package diffsync implements a block-signature based differential
// synchronizer: the receiver derives a signature from its old file, the
// sender uses it to build a delta patch for the new file, and the receiver
// atomically applies the patch.
package diffsync

import (
	"crypto/sha256"
	"errors"
)

// ErrInvalidBlockSize is returned when a non-positive block size is used.
var ErrInvalidBlockSize = errors.New("diffsync: block size must be positive")

// StrongChecksumSize is the byte length of the strong checksum (SHA-256).
const StrongChecksumSize = sha256.Size

// BlockSignature describes one block of the old file.
type BlockSignature struct {
	Index  int
	Offset int64
	Length int
	Weak   uint32
	Strong [StrongChecksumSize]byte
}

// Signature is the immutable block-signature set of the old file. It is
// safe for concurrent use by any number of senders.
type Signature struct {
	BlockSize    int
	FileLength   int64
	FileChecksum [StrongChecksumSize]byte
	Blocks       []BlockSignature

	weakIndex map[uint32][]int
}

// GenerateSignature splits data into fixed-size blocks (the last block may
// be short) and computes a weak and a strong checksum per block.
func GenerateSignature(data []byte, blockSize int) (*Signature, error) {
	if blockSize <= 0 {
		return nil, ErrInvalidBlockSize
	}
	sig := &Signature{
		BlockSize:    blockSize,
		FileLength:   int64(len(data)),
		FileChecksum: sha256.Sum256(data),
		weakIndex:    make(map[uint32][]int),
	}
	for offset := 0; offset < len(data); offset += blockSize {
		end := offset + blockSize
		if end > len(data) {
			end = len(data)
		}
		block := data[offset:end]
		bs := BlockSignature{
			Index:  len(sig.Blocks),
			Offset: int64(offset),
			Length: len(block),
			Weak:   weakChecksum(block),
			Strong: sha256.Sum256(block),
		}
		sig.Blocks = append(sig.Blocks, bs)
		sig.weakIndex[bs.Weak] = append(sig.weakIndex[bs.Weak], bs.Index)
	}
	return sig, nil
}

// weakChecksum is an rsync-style rolling checksum: a is the plain byte
// sum, b is the position-weighted byte sum, both taken mod 2^16.
func weakChecksum(data []byte) uint32 {
	var a, b uint32
	n := len(data)
	for k := 0; k < n; k++ {
		a += uint32(data[k])
		b += uint32(n-k) * uint32(data[k])
	}
	return packWeak(a, b)
}

// packWeak folds the two 16-bit halves into the 32-bit weak checksum.
func packWeak(a, b uint32) uint32 {
	return (b&0xffff)<<16 | (a & 0xffff)
}

// rollWeak advances a window checksum by one byte: out leaves the window,
// in enters it. n is the window length. All arithmetic is mod 2^16, which
// uint32 wraparound computes correctly because 2^32 is a multiple of 2^16.
func rollWeak(a, b uint32, n int, out, in byte) (newA, newB, weak uint32) {
	newA = (a - uint32(out) + uint32(in)) & 0xffff
	newB = (b - uint32(n)*uint32(out) + newA) & 0xffff
	return newA, newB, packWeak(newA, newB)
}

// splitWeak recovers the a and b halves from a packed weak checksum so a
// freshly computed window can seed subsequent rolling updates.
func splitWeak(weak uint32) (a, b uint32) {
	return weak & 0xffff, weak >> 16
}
