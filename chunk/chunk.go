// Package chunk 把数据按定长切块，并提供可滚动的弱校验和与强校验和。
package chunk

import (
	"crypto/sha256"
	"encoding/binary"

	"ontology/verify"
)

const mod = 1 << 16

// Block 是目标端的一个定长块（末块允许短于 BlockSize）。
type Block struct {
	Index  int
	Data   []byte
	Weak   uint32
	Strong [32]byte
}

// Split 按 blockSize 切块并计算每块的弱、强校验和。blockSize 为 0 时报错。
func Split(data []byte, blockSize uint32) ([]Block, error) {
	if blockSize == 0 {
		return nil, verify.ErrBadBlockSize
	}
	n := int(blockSize)
	var blocks []Block
	for start, idx := 0, 0; start < len(data); start, idx = start+n, idx+1 {
		end := start + n
		if end > len(data) {
			end = len(data)
		}
		d := data[start:end]
		blocks = append(blocks, Block{
			Index:  idx,
			Data:   d,
			Weak:   Weak(d),
			Strong: Strong(d),
		})
	}
	return blocks, nil
}

// Weak 返回加法型弱校验和。a、b 均取模 2^16，合并为 uint32。
func Weak(x []byte) uint32 {
	var a, b uint32
	for i, c := range x {
		v := uint32(c)
		a += v
		b += uint32(len(x)-i) * v
	}
	a %= mod
	b %= mod
	return a | (b << 16)
}

// Strong 返回数据的 SHA-256，用于弱和命中后的确认。
func Strong(x []byte) [32]byte {
	return sha256.Sum256(x)
}

// EncodeWeak / DecodeWeak 用于签名与补丁的小端序列化。
func EncodeWeak(w uint32) [4]byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], w)
	return b
}

func DecodeWeak(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// Scanner 在源数据上维护一个 blockSize 长的滚动窗口。
// WeakOps 只统计滚动推进的基本运算次数（每步 4），初始化不计入。
type Scanner struct {
	data        []byte
	n           int
	pos         int
	cons        int
	a, b        uint32
	weakOps     int
	weakHits    int
	strongCalls int
}

// NewScanner 创建首个长度恰为 blockSize 的窗口扫描器。
func NewScanner(data []byte, blockSize uint32) (*Scanner, error) {
	if blockSize == 0 {
		return nil, verify.ErrBadBlockSize
	}
	n := int(blockSize)
	if len(data) < n {
		return &Scanner{data: data, n: n, pos: -1, cons: 0}, nil
	}
	var a, b uint32
	for i := 0; i < n; i++ {
		v := uint32(data[i])
		a += v
		b += uint32(n-i) * v
	}
	return &Scanner{data: data, n: n, pos: 0, cons: 0, a: a % mod, b: b % mod}, nil
}

// Valid 表示当前存在一个长度为 blockSize 的完整窗口。
func (s *Scanner) Valid() bool { return s.pos >= 0 && s.pos+s.n <= len(s.data) }

// Pos 返回当前窗口起点，Done 表示已无完整窗口。
func (s *Scanner) Pos() int { return s.pos }

// Weak 返回当前窗口弱和。
func (s *Scanner) Weak() uint32 { return s.a | (s.b<<16) }

// MarkWeakHit 记录一次弱和命中（调用方据签名表判定）。
func (s *Scanner) MarkWeakHit() { s.weakHits++ }

// ConfirmStrong 计算当前窗口强和并计数，用于弱命中后的确认。
func (s *Scanner) ConfirmStrong() [32]byte {
	s.strongCalls++
	return Strong(s.data[s.pos : s.pos+s.n])
}

// Tail 返回窗口全部扫完后的剩余短块（长度 < blockSize），无则为 nil。
func (s *Scanner) Tail() []byte {
	if s.Valid() {
		return nil
	}
	if s.cons < len(s.data) {
		return s.data[s.cons:]
	}
	return nil
}

// Advance 消费一个字节并把窗口从偏移 i 滚动到 i+1，仅做 4 次基本加减。
func (s *Scanner) Advance() bool {
	if !s.Valid() {
		return false
	}
	if s.pos+s.n >= len(s.data) {
		s.cons = len(s.data)
		s.pos = -1
		return false
	}
	out := uint32(s.data[s.pos])
	inc := uint32(s.data[s.pos+s.n])
	s.a = (s.a - out + inc) % mod
	s.b = (s.b - uint32(s.n)*out + s.a) % mod
	s.pos++
	s.cons = s.pos
	s.weakOps += 4
	return true
}

// SkipWindow 在命中并复用当前块后，一次性跳过整个 blockSize 窗口。
func (s *Scanner) SkipWindow() {
	if !s.Valid() {
		return
	}
	end := s.pos + s.n
	s.cons = end
	if end+s.n > len(s.data) {
		s.pos = -1
		return
	}
	var a, b uint32
	for i := 0; i < s.n; i++ {
		v := uint32(s.data[end+i])
		a += v
		b += uint32(s.n-i) * v
	}
	s.a, s.b, s.pos = a%mod, b%mod, end
}
		}
		return false
	}
	out := uint32(s.data[s.pos])
	inc := uint32(s.data[s.pos+s.n])
	s.a = (s.a - out + inc) % mod
	s.b = (s.b - uint32(s.n)*out + s.a) % mod
	s.pos++
	s.weakOps += 4
	return true
}

// WeakOps 返回滚动推进累计的基本运算次数。
func (s *Scanner) WeakOps() int { return s.weakOps }

// WeakHits 返回弱和命中次数，StrongCalls 返回强和计算次数。
func (s *Scanner) WeakHits() int    { return s.weakHits }
func (s *Scanner) StrongCalls() int { return s.strongCalls }
