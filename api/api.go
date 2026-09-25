package api

import (
	"errors"
	"sync"

	"ontology/lsm"
	"ontology/mem"
)

const maxKeyLen = 64

// 三类可判定哨兵错误，互不相同；拒收在加锁前返回，不触碰任何状态。
var (
	ErrEmptyKey      = errors.New("lsm: empty key")
	ErrKeyTooLong    = errors.New("lsm: key longer than 64 bytes")
	ErrInvalidMaxMem = errors.New("lsm: maxMem must be positive")
)

type Store struct {
	mu      sync.Mutex
	mem     *mem.Memtable
	tables  *lsm.LSM
	seq     int64
	lastAmp int
}

// New 创建 memtable 容量为 maxMem 条的存储。
func New(maxMem int) (*Store, error) {
	if maxMem <= 0 {
		return nil, ErrInvalidMaxMem
	}
	return &Store{mem: mem.New(maxMem), tables: lsm.New()}, nil
}

func validateKey(k string) error {
	if k == "" {
		return ErrEmptyKey
	} else if len(k) > maxKeyLen {
		return ErrKeyTooLong
	}
	return nil
}

// write 须持锁调用：满则先冻结清空，再分配 seq 写入本条。
func (s *Store) write(key string, e mem.Entry) {
	if s.mem.Full() {
		s.tables.Freeze(s.mem.Drain())
	}
	s.seq++
	e.Seq = s.seq
	s.mem.Put(key, e)
}

func (s *Store) Put(key string, val int64) error {
	if err := validateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(key, mem.Entry{Value: val})
	return nil
}

// Del 写墓碑，墓碑也占一个全局 seq。
func (s *Store) Del(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(key, mem.Entry{Deleted: true})
	return nil
}

// Get：值 (v,true,false)；墓碑 (_,true,true)；从未写入 (_,false,false)。
// memtable 优先于所有 SSTable。
func (s *Store) Get(key string) (int64, bool, bool, error) {
	if err := validateKey(key); err != nil {
		return 0, false, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.mem.Get(key); ok {
		s.lastAmp = 0
		return e.Value, true, e.Deleted, nil
	}
	e, ok := s.tables.Get(key)
	s.lastAmp = s.tables.ReadAmp()
	if !ok {
		return 0, false, false, nil
	}
	return e.Value, true, e.Deleted, nil
}

// Compact 把所有 SSTable 合并成一个（墓碑规则见 lsm.Compact）。
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tables.Compact()
}

func (s *Store) ReadAmp() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAmp
}

// SelfCheck 用内置写序列核验第二节四条不变量，全过返回 nil。
func (s *Store) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func(m string) error { return errors.New("lsm selfcheck: " + m) }
	c, _ := New(2) // 第三节八步场景
	put, del := func(k string, v int64) { c.write(k, mem.Entry{Value: v}) },
		func(k string) { c.write(k, mem.Entry{Deleted: true}) }
	put("k1", 1)
	put("k2", 2)
	put("k1", 3)
	del("k2")
	put("k3", 4)
	v, ok, d, _ := c.Get("k1") // 第 6 步 → C
	if !ok || d || v != 3 {
		return fail("step6 want C")
	}
	del("k1") // 第 7 步
	if _, ok, d, _ = c.Get("k1"); !ok || !d {
		return fail("step8 want deleted") // 第 8 步 → 已删除
	}
	c.Compact()
	if _, ok, d, _ = c.Get("k2"); !ok || !d || c.tables.TableCount() != 1 {
		return fail("compact: k2 tombstone retained, 1 table")
	}
	t, _ := New(1) // 不变量4：拒收不留痕
	_ = t.Put("x", 9)
	seq, n := t.seq, t.tables.TableCount()
	long := string(make([]byte, maxKeyLen+1))
	if !errors.Is(t.Put("", 1), ErrEmptyKey) || !errors.Is(t.Put(long, 1), ErrKeyTooLong) {
		return fail("empty/long key rejected")
	}
	_, _, _, err := t.Get("")
	if !errors.Is(err, ErrEmptyKey) || t.seq != seq || t.tables.TableCount() != n {
		return fail("reject leaves no trace")
	}
	if _, err = New(0); !errors.Is(err, ErrInvalidMaxMem) {
		return fail("maxMem<=0 rejected")
	}
	return nil
}
