// Package codec gives O(1) random access to fixed-length Base64 blocks.
// It depends only on b64; the dependency direction is codec -> b64.
package codec

import (
	"errors"

	"ontology/b64"
)

// ErrBlockOutOfRange reports that k has no corresponding 4-char block.
var ErrBlockOutOfRange = errors.New("codec: block index out of range")

const blockSize = 4

// blockIndex locates blocks at the direct offset 4*k. lastBlockRead is the
// number of input bytes read while locating/decoding the block in the most
// recent call; it is an unexported field and is never exposed by any
// exported function or method.
type blockIndex struct {
	lastBlockRead int
}

// BlockCount returns the number of complete 4-char blocks in data.
func BlockCount(data []byte) int { return len(data) / blockSize }

// DecodeBlockAt decodes the k-th block (0-based) by jumping straight to
// offset 4*k. The final block may carry padding and yields 1 or 2 bytes;
// every non-final block must be a complete 3-byte block.
func DecodeBlockAt(data []byte, k int) ([]byte, error) {
	var idx blockIndex // counter is call-local: no shared mutable state
	return idx.at(data, k)
}

func (x *blockIndex) at(data []byte, k int) ([]byte, error) {
	x.lastBlockRead = 0
	if len(data)%blockSize != 0 && len(data) != 0 {
		return nil, b64.ErrLength
	}
	n := len(data) / blockSize
	if k < 0 || k >= n {
		return nil, ErrBlockOutOfRange
	}
	off := blockSize * k
	var block [blockSize]byte
	for i := 0; i < blockSize; i++ {
		block[i] = data[off+i] // direct addressing: exactly 4 bytes touched
		x.lastBlockRead++
	}
	if k != n-1 { // padding is legal only in the very last block
		for _, c := range block {
			if c == '=' {
				return nil, b64.ErrPadding
			}
		}
	}
	out, err := b64.Decode(block[:])
	if err != nil {
		return nil, err
	}
	return out, nil
}
