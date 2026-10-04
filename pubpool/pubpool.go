// Package pubpool manages the pool of public addresses and the per-address
// port-block bitmaps. It contains no allocation policy.
package pubpool

import "errors"

// ErrInvalidParam reports a pool parameter outside its allowed range.
var ErrInvalidParam = errors.New("pubpool: invalid parameter")

const (
	minPort = 1024
	maxPort = 65535
	maxAddr = 4096
)

// Block is one port block of an address. Owner is 0 while the block is free.
type Block struct {
	Owner     int
	Idle      bool
	IdleSince int64
	Used      int
	size      int
	bits      []uint64
}

func newBlock(size int) Block {
	return Block{size: size, bits: make([]uint64, (size+63)/64)}
}

// Get reports whether the port at offset off is in use.
func (b *Block) Get(off int) bool { return b.bits[off/64]&(1<<uint(off%64)) != 0 }

// Set marks the port at offset off as in use.
func (b *Block) Set(off int) { b.bits[off/64] |= 1 << uint(off%64) }

// Clear marks the port at offset off as free.
func (b *Block) Clear(off int) { b.bits[off/64] &^= 1 << uint(off%64) }

// FirstFree returns the smallest free port offset, or -1 if the block is full.
func (b *Block) FirstFree() int {
	for w, word := range b.bits {
		if word == ^uint64(0) {
			continue
		}
		for i := 0; i < 64; i++ {
			off := w*64 + i
			if off >= b.size {
				return -1
			}
			if word&(1<<uint(i)) == 0 {
				return off
			}
		}
	}
	return -1
}

// Reset returns the block to the free, unowned state.
func (b *Block) Reset() {
	b.Owner = 0
	b.Idle = false
	b.IdleSince = 0
	b.Used = 0
	for i := range b.bits {
		b.bits[i] = 0
	}
}

// FreeHeap is a min-heap of free block indices of one address.
type FreeHeap []int

func (h FreeHeap) Len() int            { return len(h) }
func (h FreeHeap) Less(i, j int) bool  { return h[i] < h[j] }
func (h FreeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *FreeHeap) Push(x interface{}) { *h = append(*h, x.(int)) }
func (h *FreeHeap) Pop() interface{} {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// Addr is one public address with its port blocks.
type Addr struct {
	Drained bool
	Bound   int
	Blocks  []Block
	Free    FreeHeap
}

// Pool holds A public addresses, each with K port blocks of size S.
type Pool struct {
	A, L, H, S, K int
	Addrs         []Addr
}

// NewPool validates the geometry and builds a pool with all blocks free.
func NewPool(A, L, H, S int) (*Pool, error) {
	if A < 1 || A > maxAddr || L < minPort || H > maxPort || L > H || S < 1 || (H-L+1)%S != 0 {
		return nil, ErrInvalidParam
	}
	K := (H - L + 1) / S
	p := &Pool{A: A, L: L, H: H, S: S, K: K, Addrs: make([]Addr, A)}
	for a := range p.Addrs {
		ad := &p.Addrs[a]
		ad.Blocks = make([]Block, K)
		ad.Free = make(FreeHeap, 0, K)
		for j := 0; j < K; j++ {
			ad.Blocks[j] = newBlock(S)
			ad.Free = append(ad.Free, j)
		}
	}
	return p, nil
}

// BlockIndex maps a port to its block index on any address.
func (p *Pool) BlockIndex(port int) int { return (port - p.L) / p.S }

// BlockFirst returns the first port of block idx.
func (p *Pool) BlockFirst(idx int) int { return p.L + idx*p.S }

// BlockLast returns the last port of block idx.
func (p *Pool) BlockLast(idx int) int { return p.L + (idx+1)*p.S - 1 }
