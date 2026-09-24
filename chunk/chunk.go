// Package chunk 把记录集合切成定长块并提供滚动弱校验和与强校验和。
package chunk

import (
	"crypto/sha256"
	"errors"
)

// ErrBlockSize 表示块大小非法（0）。
var ErrBlockSize = errors.New("chunk: block size must be > 0")

const weakMod = 65536

// Block 是一块记录及其校验和。
type Block struct {
	Index int
	Data  []byte
	Weak  uint16
	Strong [32]byte
}

// Stats 记录非导出运算计数器的对外快照。
type Stats struct {
	WeakOps   int
	StrongOps int
	WeakHits  int
}

// Weak 计算 data 的弱校验和：字节和模 65536。
func Weak(data []byte) uint16 {
	var sum int
	for _, b := range data {
		sum += int(b)
	}
	return uint16(sum % weakMod)
}

// Strong 计算 data 的强校验和（SHA-256）。
func Strong(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// Split 按 blockSize 切块，末块可不满。
func Split(data []byte, blockSize int) ([]Block, error) {
	if blockSize <= 0 {
		return nil, ErrBlockSize
	}
	blocks := make([]Block, 0, (len(data)+blockSize-1)/blockSize)
	for off, idx := 0, 0; off < len(data); off, idx = off+blockSize, idx+1 {
		end := off + blockSize
		if end > len(data) {
			end = len(data)
		}
		part := data[off:end]
		blocks = append(blocks, Block{
			Index: idx, Data: append([]byte(nil), part...),
			Weak: Weak(part), Strong: Strong(part),
		})
	}
	return blocks, nil
}

// Roller 是可逐偏移推进的滚动弱校验窗口。
type Roller struct {
	data      []byte
	size      int
	off       int
	sum       int
	weakOps   int
	strongOps int
	weakHits  int
}

// NewRoller 创建块大小为 blockSize 的滚动窗口。
func NewRoller(data []byte, blockSize int) (*Roller, error) {
	if blockSize <= 0 {
		return nil, ErrBlockSize
	}
	r := &Roller{data: data, size: blockSize}
	if len(data) >= blockSize {
		for i := 0; i < blockSize; i++ {
			r.sum += int(data[i])
		}
		r.weakOps += blockSize // 首块的逐字节加法
	}
	return r, nil
}

// Valid 报告当前是否存在一个完整窗口。
func (r *Roller) Valid() bool { return r.off+r.size <= len(r.data) }

// Offset 返回当前窗口起点。
func (r *Roller) Offset() int { return r.off }

// Window 返回当前窗口数据。
func (r *Roller) Window() []byte { return r.data[r.off : r.off+r.size] }

// Weak 返回当前窗口弱和（不产生强校验运算）。
func (r *Roller) Weak() uint16 { return uint16(r.sum % weakMod) }

// MarkWeakHit 由 diff 在弱和命中候选时回调。
func (r *Roller) MarkWeakHit() { r.weakHits++ }

// Strong 仅在弱命中后调用：计算当前窗口强和并累加强运算计数。
func (r *Roller) Strong() [32]byte {
	r.strongOps++
	return Strong(r.Window())
}

// Advance 把窗口向右推进一个字节。
func (r *Roller) Advance() {
	if !r.Valid() {
		return
	}
	if r.off+r.size < len(r.data) {
		r.sum -= int(r.data[r.off])
		r.sum += int(r.data[r.off+r.size])
		r.sum %= weakMod
		if r.sum < 0 {
			r.sum += weakMod
		}
		r.weakOps += 3 // 减、加、取模
	}
	r.off++
}

// Stats 返回运算计数器快照。
func (r *Roller) Stats() Stats {
	return Stats{WeakOps: r.weakOps, StrongOps: r.strongOps, WeakHits: r.weakHits}
}
