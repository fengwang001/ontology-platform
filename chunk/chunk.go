package chunk

import (
	"crypto/sha256"
	"errors"
)

// ErrBadBlockSize 表示块大小非法。
var ErrBadBlockSize = errors.New("chunk: block size must be positive")

// Block 是一个定长分块（末块可不满）。
type Block struct {
	Index int
	Data  []byte
}

// Split 将数据切成 blockSize 大小的块。
func Split(data []byte, blockSize int) ([]Block, error) {
	if blockSize <= 0 {
		return nil, ErrBadBlockSize
	}
	n := (len(data) + blockSize - 1) / blockSize
	blocks := make([]Block, 0, n)
	for i := 0; i < len(data); i += blockSize {
		end := i + blockSize
		if end > len(data) {
			end = len(data)
		}
		blocks = append(blocks, Block{Index: i / blockSize, Data: data[i:end]})
	}
	return blocks, nil
}

// Weak 是加法型 16 位弱校验和（mod 2^16，自然回绕）。
func Weak(data []byte) uint16 {
	var s uint16
	for _, b := range data {
		s += uint16(b)
	}
	return s
}

// Strong 是 SHA-256 截取前 16 字节的强校验和。
func Strong(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:16]
}

// Roller 维护滑动窗口弱校验和，计数便于复杂度证明。
type Roller struct {
	data      []byte
	size      int
	pos       int
	weak      uint16
	weakOps   int
	strongCnt int
}

// NewRoller 创建窗口大小为 blockSize 的滚动器。
func NewRoller(data []byte, blockSize int) (*Roller, error) {
	if blockSize <= 0 {
		return nil, ErrBadBlockSize
	}
	r := &Roller{data: data, size: blockSize}
	if len(data) >= blockSize {
		r.weak = Weak(data[:blockSize])
		r.weakOps += blockSize
	}
	return r, nil
}

// Valid 报告当前窗口是否完整。
func (r *Roller) Valid() bool { return r.pos+r.size <= len(r.data) }

// Pos 返回当前窗口起始偏移。
func (r *Roller) Pos() int { return r.pos }

// Weak 返回当前窗口弱校验和。
func (r *Roller) Weak() uint16 { return r.weak }

// Advance 前进一步；w1 = w0 - d[pos] + d[pos+size]。
func (r *Roller) Advance() bool {
	if r.pos+r.size >= len(r.data) {
		return false
	}
	r.weak -= uint16(r.data[r.pos])
	r.weakOps++
	r.weak += uint16(r.data[r.pos+r.size])
	r.weakOps++
	r.pos++
	return true
}

// ConfirmStrong 计算当前窗口强校验和（仅在弱命中时由调用方调用）。
func (r *Roller) ConfirmStrong() []byte {
	r.strongCnt++
	return Strong(r.data[r.pos : r.pos+r.size])
}

// WeakOps 返回弱校验和基本运算累计次数。
func (r *Roller) WeakOps() int { return r.weakOps }

// StrongCount 返回强校验和计算次数。
func (r *Roller) StrongCount() int { return r.strongCnt }
