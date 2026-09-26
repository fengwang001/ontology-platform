// Package api 是 CRC-32 校验的对外接口，并发安全。
package api

import (
	"errors"
	"sync"

	"ontology/crc"
	"ontology/poly"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidPoly = poly.ErrInvalid
	ErrEmptyVerify = errors.New("api: 累计 0 字节就调用 Verify")
	ErrMismatch    = errors.New("api: 校验和不匹配")
)

// Checksum 累计字节流并给出 CRC-32 校验和。
type Checksum struct {
	mu    sync.Mutex
	core  *crc.CRC
	bytes int
}

// New 以反射多项式 poly 创建实例；poly 非法时整体失败，返回 ErrInvalidPoly。
func New(p uint32) (*Checksum, error) {
	if err := poly.Check(p); err != nil {
		return nil, ErrInvalidPoly
	}
	return &Checksum{core: crc.New(p)}, nil
}

// Update 表驱动累计一段字节。
func (c *Checksum) Update(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.core.UpdateTable(p)
	c.bytes += len(p)
}

// Value 返回当前校验和。
func (c *Checksum) Value() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.core.Value()
}

// BytesProcessed 返回累计字节数。
func (c *Checksum) BytesProcessed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Verify 校验期望值；只读不写，失败不改变累计状态。
func (c *Checksum) Verify(want uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bytes == 0 {
		return ErrEmptyVerify
	}
	if c.core.Value() != want {
		return ErrMismatch
	}
	return nil
}

// SelfCheck 对内置输入核验四条不变量，全部通过返回 nil。
func (c *Checksum) SelfCheck() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 不变量 2：已知向量。
	cs, err := New(poly.IEEE)
	if err != nil {
		return err
	}
	cs.Update([]byte("123456789"))
	if cs.Value() != 0xCBF43926 {
		return errors.New("api: 自检失败：已知向量")
	}
	// 不变量 1 与 3：表驱动==逐位参照，且任意切分点拼接一致。
	data := []byte("ontology self-check vector 0123456789")
	for cut := 0; cut <= len(data); cut++ {
		a, _ := New(poly.IEEE)
		a.Update(data[:cut])
		a.Update(data[cut:])
		ref := crc.New(poly.IEEE)
		ref.UpdateBitwise(data)
		if a.Value() != ref.Value() {
			return errors.New("api: 自检失败：一致性/拼接")
		}
	}
	// 不变量 4：被拒操作不留痕。
	if _, err := New(0x04C11DB7); !errors.Is(err, ErrInvalidPoly) {
		return errors.New("api: 自检失败：非法多项式未被拒")
	}
	b, _ := New(poly.IEEE)
	if err := b.Verify(0); !errors.Is(err, ErrEmptyVerify) {
		return errors.New("api: 自检失败：空校验未被拒")
	}
	b.Update([]byte("x"))
	v, n := b.Value(), b.BytesProcessed()
	if err := b.Verify(v ^ 1); !errors.Is(err, ErrMismatch) {
		return errors.New("api: 自检失败：不匹配未被拒")
	}
	if b.Value() != v || b.BytesProcessed() != n {
		return errors.New("api: 自检失败：被拒后状态改变")
	}
	return nil
}
