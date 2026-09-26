// Package api 对外提供 AES-128 分组加密：NewCipher/EncryptBlock/SelfCheck。
package api

import (
	"bytes"
	"errors"

	"ontology/aes"
)

// 三类可判定哨兵错误（与 aes 包同一实例，errors.Is 可判定，互不相同）。
var (
	ErrKeyLength      = aes.ErrKeyLength
	ErrBlockLength    = aes.ErrBlockLength
	ErrNotInitialized = aes.ErrNotInitialized
)

// ErrSelfCheck 是 SelfCheck 失败时返回的哨兵错误。
var ErrSelfCheck = errors.New("api: 自检未通过")

// Cipher 是 AES-128 加密器；零值未初始化，加密会被拒绝。
type Cipher struct {
	inner *aes.Cipher
}

// NewCipher 用 16 字节密钥构造加密器；密钥长度非法时整体失败。
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != aes.BlockSize {
		return nil, ErrKeyLength
	}
	inner, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Cipher{inner: inner}, nil
}

// EncryptBlock 加密一个 16 字节分组。所有校验先于任何状态写入，
// 被拒绝的调用（未初始化/分组长度非法）不改变任何状态。
func (c *Cipher) EncryptBlock(block []byte) ([]byte, error) {
	if c == nil || c.inner == nil {
		return nil, ErrNotInitialized
	}
	if len(block) != aes.BlockSize {
		return nil, ErrBlockLength
	}
	return c.inner.EncryptBlock(block)
}

// NaiveEncryptBlock 朴素参照：用 aes 导出的四个步骤函数逐轮独立执行，
// 不做任何优化合并，用于与 EncryptBlock 交叉验证。
func NaiveEncryptBlock(key, block []byte) ([]byte, error) {
	w, err := aes.ExpandKey(key)
	if err != nil {
		return nil, err
	}
	if len(block) != aes.BlockSize {
		return nil, ErrBlockLength
	}
	var s [16]byte
	copy(s[:], block)
	aes.AddRoundKey(&s, w[0:4])
	for r := 1; r < 10; r++ {
		aes.SubBytes(&s)
		aes.ShiftRows(&s)
		aes.MixColumns(&s)
		aes.AddRoundKey(&s, w[4*r:4*r+4])
	}
	aes.SubBytes(&s)
	aes.ShiftRows(&s)
	aes.AddRoundKey(&s, w[40:44])
	out := make([]byte, aes.BlockSize)
	copy(out, s[:])
	return out, nil
}

// SelfCheck 对内置输入核验四条不变量：FIPS-197 向量、朴素参照一致、
// 轮密钥缓存、失败不留痕。全部通过返回 nil，否则返回 ErrSelfCheck。
func SelfCheck() error {
	key := make([]byte, aes.BlockSize)
	for i := range key {
		key[i] = byte(i)
	}
	pt := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	want := []byte{0x69, 0xc4, 0xe0, 0xd8, 0x6a, 0x7b, 0x04, 0x30,
		0xd8, 0xcd, 0xb7, 0x80, 0x70, 0xb4, 0xc5, 0x5a}
	c, err := NewCipher(key)
	if err != nil {
		return err
	}
	ct, err := c.EncryptBlock(pt)
	if err != nil || !bytes.Equal(ct, want) { // 不变量 1：FIPS-197 向量
		return ErrSelfCheck
	}
	nt, err := NaiveEncryptBlock(key, pt)
	if err != nil || !bytes.Equal(nt, ct) { // 不变量 2：朴素参照一致
		return ErrSelfCheck
	}
	if !c.inner.CacheHolds(1000) { // 不变量 3：轮密钥缓存、零重算
		return ErrSelfCheck
	}
	if _, e := NewCipher(key[:15]); !errors.Is(e, ErrKeyLength) { // 不变量 4：失败不留痕
		return ErrSelfCheck
	}
	if _, e := c.EncryptBlock(pt[:15]); !errors.Is(e, ErrBlockLength) {
		return ErrSelfCheck
	}
	var zero Cipher
	if _, e := zero.EncryptBlock(pt); !errors.Is(e, ErrNotInitialized) {
		return ErrSelfCheck
	}
	again, err := c.EncryptBlock(pt) // 被拒后状态不变，仍可正常使用
	if err != nil || !bytes.Equal(again, ct) {
		return ErrSelfCheck
	}
	return nil
}
