// Package api 是 CRC-32 校验的对外接口。依赖 crc。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/crc"
	"ontology/poly"
)

const (
	initV  uint32 = 0xFFFFFFFF
	xorout uint32 = 0xFFFFFFFF
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidPoly = errors.New("crc: 非法多项式（为 0 或 bit31 未置位）")
	ErrEmptyVerify = errors.New("crc: 累计 0 字节就校验")
	ErrMismatch    = errors.New("crc: 校验和不匹配")
)

// Checker 累计字节并给出 CRC-32 校验和。并发安全。
type Checker struct {
	mu    sync.RWMutex
	d     *crc.Digest
	bytes int
}

// New 以反射多项式 p 新建 Checker；p 非法时整体失败，不留下任何状态。
func New(p uint32) (*Checker, error) {
	if !poly.Valid(p) {
		return nil, ErrInvalidPoly
	}
	return &Checker{d: crc.NewDigest(crc.NewTable(p), initV)}, nil
}

// Update 追加一段字节。
func (c *Checker) Update(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.d.UpdateTable(p)
	c.bytes += len(p)
}

// Value 返回当前校验和：crc ^ xorout。
func (c *Checker) Value() uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.d.Raw() ^ xorout
}

// BytesProcessed 返回累计字节数。
func (c *Checker) BytesProcessed() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bytes
}

// Verify 比对期望值。空校验、不匹配都整体失败且不改累计状态。
func (c *Checker) Verify(want uint32) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.bytes == 0 {
		return ErrEmptyVerify
	}
	if got := c.d.Raw() ^ xorout; got != want {
		return fmt.Errorf("%w: got 0x%08X want 0x%08X", ErrMismatch, got, want)
	}
	return nil
}

// SelfCheck 对内置输入核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	inputs := [][]byte{[]byte("123456789"), {}, {0x00}, []byte("ontology-crc32-selfcheck")}
	for _, in := range inputs { // 不变量 1：表驱动 == 逐位参照
		tab := crc.NewTable(poly.IEEE)
		a, b := crc.NewDigest(tab, initV), crc.NewDigest(tab, initV)
		a.UpdateTable(in)
		b.UpdateBitwise(in)
		if a.Raw() != b.Raw() {
			return errors.New("selfcheck: 表驱动与逐位参照不一致")
		}
	}
	c, err := New(poly.IEEE) // 不变量 2：已知测试向量
	if err != nil {
		return err
	}
	c.Update([]byte("123456789"))
	if c.Value() != 0xCBF43926 {
		return errors.New("selfcheck: 已知向量 0xCBF43926 不成立")
	}
	whole := []byte("ontology-platform-crc32") // 不变量 3：任意切分点拼接
	for i := 0; i <= len(whole); i++ {
		a, _ := New(poly.IEEE)
		a.Update(whole)
		b, _ := New(poly.IEEE)
		b.Update(whole[:i])
		b.Update(whole[i:])
		if a.Value() != b.Value() {
			return errors.New("selfcheck: 拼接性质不成立")
		}
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidPoly) { // 不变量 4：失败不留痕
		return errors.New("selfcheck: poly==0 未被拒绝")
	}
	if _, err := New(0x04C11DB7); !errors.Is(err, ErrInvalidPoly) {
		return errors.New("selfcheck: bit31 未置位未被拒绝")
	}
	v, _ := New(poly.IEEE)
	if err := v.Verify(0); !errors.Is(err, ErrEmptyVerify) {
		return errors.New("selfcheck: 空校验未被拒绝")
	}
	v.Update([]byte("x"))
	before := v.Value()
	if err := v.Verify(before ^ 1); !errors.Is(err, ErrMismatch) {
		return errors.New("selfcheck: 不匹配未被拒绝")
	}
	if v.Value() != before || v.BytesProcessed() != 1 {
		return errors.New("selfcheck: 被拒后状态被改变")
	}
	return nil
}
