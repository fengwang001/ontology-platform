// Package aes 实现 AES-128 分组加密（FIPS-197）。状态为列主序 16 字节。
package aes

import (
	"errors"
	"sync/atomic"

	"ontology/gf256"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrKeyLength      = errors.New("aes: 密钥长度必须为 16 字节")
	ErrBlockLength    = errors.New("aes: 分组长度必须为 16 字节")
	ErrNotInitialized = errors.New("aes: cipher 尚未初始化（密钥未扩展）")
)

// BlockSize 是 AES 分组字节数。
const BlockSize = 16

// sbox 是 FIPS-197 给定的 S 盒（256 字节），下标即输入字节。
const sbox = "\x63\x7c\x77\x7b\xf2\x6b\x6f\xc5\x30\x01\x67\x2b\xfe\xd7\xab\x76\xca\x82\xc9\x7d\xfa\x59\x47\xf0\xad\xd4\xa2\xaf\x9c\xa4\x72\xc0\xb7\xfd\x93\x26\x36\x3f\xf7\xcc\x34\xa5\xe5\xf1\x71\xd8\x31\x15\x04\xc7\x23\xc3\x18\x96\x05\x9a\x07\x12\x80\xe2\xeb\x27\xb2\x75" +
	"\x09\x83\x2c\x1a\x1b\x6e\x5a\xa0\x52\x3b\xd6\xb3\x29\xe3\x2f\x84\x53\xd1\x00\xed\x20\xfc\xb1\x5b\x6a\xcb\xbe\x39\x4a\x4c\x58\xcf\xd0\xef\xaa\xfb\x43\x4d\x33\x85\x45\xf9\x02\x7f\x50\x3c\x9f\xa8\x51\xa3\x40\x8f\x92\x9d\x38\xf5\xbc\xb6\xda\x21\x10\xff\xf3\xd2" +
	"\xcd\x0c\x13\xec\x5f\x97\x44\x17\xc4\xa7\x7e\x3d\x64\x5d\x19\x73\x60\x81\x4f\xdc\x22\x2a\x90\x88\x46\xee\xb8\x14\xde\x5e\x0b\xdb\xe0\x32\x3a\x0a\x49\x06\x24\x5c\xc2\xd3\xac\x62\x91\x95\xe4\x79\xe7\xc8\x37\x6d\x8d\xd5\x4e\xa9\x6c\x56\xf4\xea\x65\x7a\xae\x08" +
	"\xba\x78\x25\x2e\x1c\xa6\xb4\xc6\xe8\xdd\x74\x1f\x4b\xbd\x8b\x8a\x70\x3e\xb5\x66\x48\x03\xf6\x0e\x61\x35\x57\xb9\x86\xc1\x1d\x9e\xe1\xf8\x98\x11\x69\xd9\x8e\x94\x9b\x1e\x87\xe9\xce\x55\x28\xdf\x8c\xa1\x89\x0d\xbf\xe6\x42\x68\x41\x99\x2d\x0f\xb0\x54\xbb\x16"

var rcon = [11]byte{0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1b, 0x36} // rcon[1..10]=01..36，rcon[0] 不用

// Cipher 持有扩展后的轮密钥；零值是未初始化状态。
type Cipher struct {
	w        [44][4]byte // 44 个轮密钥字，构造时一次扩展并缓存
	expanded bool
	expWords atomic.Int32 // 最近一次 EncryptBlock 重新计算的轮密钥字数（非导出）
}

// ExpandKey 把 16 字节密钥扩展为 44 个字 w[0..43]（FIPS-197 密钥编排）。
func ExpandKey(key []byte) ([44][4]byte, error) {
	var w [44][4]byte
	if len(key) != BlockSize {
		return w, ErrKeyLength
	}
	for i := 0; i < 4; i++ {
		copy(w[i][:], key[4*i:4*i+4])
	}
	for i := 4; i < 44; i++ {
		temp := w[i-1]
		if i%4 == 0 {
			temp = subWord(rotWord(temp))
			temp[0] ^= rcon[i/4]
		}
		for j := 0; j < 4; j++ {
			w[i][j] = w[i-4][j] ^ temp[j]
		}
	}
	return w, nil
}

// rotWord 左循环移 1 字节：(a,b,c,d) → (b,c,d,a)。
func rotWord(w [4]byte) [4]byte { return [4]byte{w[1], w[2], w[3], w[0]} }

func subWord(w [4]byte) [4]byte { return [4]byte{sbox[w[0]], sbox[w[1]], sbox[w[2]], sbox[w[3]]} }

// NewCipher 扩展密钥并缓存全部轮密钥；此后加密不再重复扩展。
func NewCipher(key []byte) (*Cipher, error) {
	w, err := ExpandKey(key)
	if err != nil {
		return nil, err
	}
	return &Cipher{w: w, expanded: true}, nil
}

// SubBytes 用 Sbox 逐字节替换状态。
func SubBytes(s *[16]byte) {
	for i := range s {
		s[i] = sbox[s[i]]
	}
}

// ShiftRows 第 r 行循环左移 r 字节（列主序：字节 4c+r 位于 r 行 c 列）。
func ShiftRows(s *[16]byte) {
	var t [16]byte
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			t[4*c+r] = s[4*((c+r)%4)+r]
		}
	}
	*s = t
}

// MixColumns 对每列做 GF(2^8) 矩阵乘（·2/·3 由 gf256 提供，模 0x11B）。
func MixColumns(s *[16]byte) {
	for c := 0; c < 4; c++ {
		a0, a1, a2, a3 := s[4*c], s[4*c+1], s[4*c+2], s[4*c+3]
		s[4*c] = gf256.Mul2(a0) ^ gf256.Mul3(a1) ^ a2 ^ a3
		s[4*c+1] = a0 ^ gf256.Mul2(a1) ^ gf256.Mul3(a2) ^ a3
		s[4*c+2] = a0 ^ a1 ^ gf256.Mul2(a2) ^ gf256.Mul3(a3)
		s[4*c+3] = gf256.Mul3(a0) ^ a1 ^ a2 ^ gf256.Mul2(a3)
	}
}

// AddRoundKey 状态与一轮 4 个字（16 字节）的轮密钥逐字节异或。
func AddRoundKey(s *[16]byte, w [][4]byte) {
	for c := 0; c < 4; c++ {
		for j := 0; j < 4; j++ {
			s[4*c+j] ^= w[c][j]
		}
	}
}

// EncryptBlock 加密一个 16 字节分组。校验先于任何写入，被拒调用不改变状态。
func (c *Cipher) EncryptBlock(block []byte) ([]byte, error) {
	if !c.expanded {
		return nil, ErrNotInitialized
	}
	if len(block) != BlockSize {
		return nil, ErrBlockLength
	}
	c.expWords.Store(0) // 轮密钥已在 NewCipher 缓存，本次重算字数恒为 0
	var s [16]byte
	copy(s[:], block)
	AddRoundKey(&s, c.w[0:4])
	for r := 1; r < 10; r++ {
		SubBytes(&s)
		ShiftRows(&s)
		MixColumns(&s)
		AddRoundKey(&s, c.w[4*r:4*r+4])
	}
	SubBytes(&s)
	ShiftRows(&s)
	AddRoundKey(&s, c.w[40:44]) // 最后一轮无 MixColumns
	out := make([]byte, BlockSize)
	copy(out, s[:])
	return out, nil
}

// CacheHolds 报告加密 n 个分组是否全程零重算轮密钥字（不暴露计数器数值）。
func (c *Cipher) CacheHolds(n int) bool {
	if !c.expanded {
		return false
	}
	blk := make([]byte, BlockSize)
	for i := 0; i < n; i++ {
		if _, err := c.EncryptBlock(blk); err != nil {
			return false
		}
	}
	return c.expWords.Load() == 0
}
