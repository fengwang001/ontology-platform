// Package store 按内容地址保存块，每块只存一份，用引用计数记录出现次数。
package store

import (
	"errors"

	"ontology/addr"
)

var (
	// ErrNotFound 表示取回了一个不存在（引用计数为 0）的地址。
	ErrNotFound = errors.New("store: address not found")
	// ErrTooManyBlocks 表示块总数将超过上限。
	ErrTooManyBlocks = errors.New("store: block count limit exceeded")
	// ErrTooManyBytes 表示总字节数将超过上限。
	ErrTooManyBytes = errors.New("store: byte count limit exceeded")
)

// Limits 描述容量上限；0 表示该项不限。
type Limits struct {
	MaxBlocks int
	MaxBytes  int64
}

// Store 是进程内存中的去重块存储。
type Store struct {
	limits Limits
	blocks map[addr.Addr][]byte
	refs   map[addr.Addr]int
	nblock int
	bytes  int64
}

// New 创建带容量上限的存储。
func New(l Limits) *Store {
	return &Store{
		limits: l,
		blocks: map[addr.Addr][]byte{},
		refs:   map[addr.Addr]int{},
	}
}

// Put 登记一块：新地址先校验上限再落盘（失败不留痕）；已存在地址只增引用，
// 不重复占内容空间。返回该块地址。
func (s *Store) Put(data []byte) (addr.Addr, error) {
	a := addr.Of(data)
	if s.refs[a] == 0 {
		if s.limits.MaxBlocks > 0 && s.nblock+1 > s.limits.MaxBlocks {
			return a, ErrTooManyBlocks
		}
		if s.limits.MaxBytes > 0 && s.bytes+int64(len(data)) > s.limits.MaxBytes {
			return a, ErrTooManyBytes
		}
		cp := append([]byte(nil), data...)
		s.blocks[a] = cp
		s.bytes += int64(len(cp))
		s.nblock++
	}
	s.refs[a]++
	return a, nil
}

// Get 按地址取回内容；地址不存在返回 ErrNotFound（只读，不改任何状态）。
func (s *Store) Get(a addr.Addr) ([]byte, error) {
	if s.refs[a] == 0 {
		return nil, ErrNotFound
	}
	return s.blocks[a], nil
}

// Release 减少一次引用；引用归零即删除内容，不留零引用残留。
func (s *Store) Release(a addr.Addr) error {
	if s.refs[a] == 0 {
		return ErrNotFound
	}
	s.refs[a]--
	if s.refs[a] == 0 {
		s.bytes -= int64(len(s.blocks[a]))
		delete(s.blocks, a)
		delete(s.refs, a)
		s.nblock--
	}
	return nil
}

// Refs 返回某地址当前引用次数（0 即不存在）。
func (s *Store) Refs(a addr.Addr) int { return s.refs[a] }

// BlockCount 返回去重后的块种类数。
func (s *Store) BlockCount() int { return s.nblock }

// ByteCount 返回去重后的总字节数。
func (s *Store) ByteCount() int64 { return s.bytes }
