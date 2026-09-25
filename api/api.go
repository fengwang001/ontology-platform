// Package api 对外接口：校验、哨兵错误、自检。依赖 lsm。
package api

import (
	"errors"
	"fmt"

	"ontology/lsm"
)

// 三类可判定故障，互不相同的哨兵错误。
var (
	ErrEmptyKey   = errors.New("api: empty key")
	ErrKeyTooLong = errors.New("api: key longer than 64 bytes")
	ErrBadMaxMem  = errors.New("api: maxMem must be > 0")
	ErrSelfCheck  = errors.New("api: selfcheck failed")
)

const maxKeyLen = 64

// Store 是对外句柄，并发安全（内部 lsm.Store 已加锁）。
type Store struct{ s *lsm.Store }

// New 建存储；maxMem <= 0 整体失败。
func New(maxMem int) (*Store, error) {
	if maxMem <= 0 {
		return nil, ErrBadMaxMem
	}
	return &Store{s: lsm.NewStore(maxMem)}, nil
}

func validKey(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if len(key) > maxKeyLen {
		return ErrKeyTooLong
	}
	return nil
}

// 三个写读入口都先校验再触及 lsm：被拒操作不改变任何状态（不变量4）。

func (s *Store) Put(key string, val int64) error {
	if err := validKey(key); err != nil {
		return err
	}
	s.s.Put(key, val)
	return nil
}

func (s *Store) Del(key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	s.s.Del(key)
	return nil
}

// Get 返回 (val, ok, deleted, err)：值命中 ok=true；墓碑 deleted=true；从未写入皆 false。
func (s *Store) Get(key string) (val int64, ok bool, deleted bool, err error) {
	if err := validKey(key); err != nil {
		return 0, false, false, err
	}
	v, o, d := s.s.Get(key)
	return v, o, d, nil
}

func (s *Store) Compact()     { s.s.Compact() }
func (s *Store) ReadAmp() int { return s.s.ReadAmp() }

// SelfCheck 对内置写序列核验四条不变量；失败返回包裹 ErrSelfCheck 的错误。
// 只用内部新建的存储，不影响接收方状态。
func (s *Store) SelfCheck() error {
	m, err := New(3)
	if err != nil {
		return err
	}
	type cell struct {
		val int64
		del bool
	}
	model := map[string]cell{}
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	st := uint64(42) // 确定性 LCG，复现同一写序列
	next := func(n uint64) uint64 {
		st = st*6364136223846793005 + 1442695040888963407
		return (st >> 33) % n
	}
	verify := func(phase string) error {
		for _, k := range keys {
			v, ok, del, err := m.Get(k)
			if err != nil {
				return err
			}
			c, seen := model[k]
			switch {
			case !seen && (ok || del):
				return fmt.Errorf("%w: %s: %s should not exist", ErrSelfCheck, phase, k)
			case seen && c.del && !del:
				return fmt.Errorf("%w: %s: %s should be deleted", ErrSelfCheck, phase, k)
			case seen && !c.del && (!ok || v != c.val):
				return fmt.Errorf("%w: %s: %s should be %d", ErrSelfCheck, phase, k, c.val)
			}
		}
		return nil
	}
	for i := 0; i < 500; i++ { // 不变量1+2：与朴素参照逐 key 一致
		k := keys[next(uint64(len(keys)))]
		if next(4) == 0 {
			if err := m.Del(k); err != nil {
				return err
			}
			model[k] = cell{del: true}
		} else {
			v := int64(next(1000))
			if err := m.Put(k, v); err != nil {
				return err
			}
			model[k] = cell{val: v}
		}
	}
	if err := verify("pre-compact"); err != nil {
		return err
	}
	m.Compact() // 不变量3：合并不复活已删除的 key
	if err := verify("post-compact"); err != nil {
		return err
	}
	// 不变量4：被拒操作不留痕
	before := m.ReadAmp()
	if _, err := New(0); !errors.Is(err, ErrBadMaxMem) {
		return fmt.Errorf("%w: New(0) not rejected", ErrSelfCheck)
	}
	if err := m.Put("", 1); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("%w: empty key not rejected", ErrSelfCheck)
	}
	long := string(make([]byte, maxKeyLen+1))
	if err := m.Del(long); !errors.Is(err, ErrKeyTooLong) {
		return fmt.Errorf("%w: long key not rejected", ErrSelfCheck)
	}
	if m.ReadAmp() != before {
		return fmt.Errorf("%w: rejected ops changed state", ErrSelfCheck)
	}
	return verify("post-reject")
}
