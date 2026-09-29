// Package overflow 实现大值溢出存储：值超过字节阈值时写入溢出表，
// 主记录只保存单调递增、永不复用的块号；内联与溢出互斥。
//
// 更新大值的顺序固定为：先写新块 -> 再持久化新引用 -> 最后回收旧块，
// 因此任何一步之间崩溃都不会让引用指向不存在的块（至多留下孤儿块，
// 恢复时回收）。恢复时扫描溢出表，回收无引用的孤儿块，并检出指向
// 不存在块的悬挂引用。
package overflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Config 配置一个 Store。
type Config struct {
	// Dir 持久化目录（主记录与溢出块都落盘在此）。
	Dir string
	// Threshold 内联阈值（字节）：len(value) <= Threshold 内联，否则溢出。
	Threshold int
	// MaxBlocks 溢出块数上限。
	MaxBlocks int
}

func (c Config) validate() error {
	if c.Threshold <= 0 {
		return fmt.Errorf("%w: threshold must be > 0, got %d", ErrInvalidThreshold, c.Threshold)
	}
	if c.MaxBlocks <= 0 {
		return fmt.Errorf("%w: max blocks must be > 0, got %d", ErrInvalidMaxBlocks, c.MaxBlocks)
	}
	if c.Dir == "" {
		return fmt.Errorf("overflow: dir must not be empty")
	}
	return nil
}

// record 主记录：Inline 与 Overflow 互斥。
type record struct {
	Overflow bool
	Inline   []byte // 仅 Overflow == false 时有效
	Block    uint64 // 仅 Overflow == true 时有效
}

// Store 大值溢出存储，所有方法均可并发调用。
type Store struct {
	mu        sync.RWMutex
	dir       string
	blocksDir string
	threshold int
	maxBlocks int

	records map[string]record
	blocks  map[uint64]struct{}
	next    uint64 // 单调递增、永不复用

	recoveredOrphans []uint64
}

// Open 校验配置并打开（必要时创建）持久化目录，加载主记录与溢出表，
// 随后执行崩溃恢复：回收无引用的孤儿块；若检出指向不存在块的悬挂
// 引用，则返回带 ErrDanglingRef 的错误并拒绝打开。
func Open(cfg Config) (*Store, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       cfg.Dir,
		blocksDir: filepath.Join(cfg.Dir, blocksDir),
		threshold: cfg.Threshold,
		maxBlocks: cfg.MaxBlocks,
		records:   make(map[string]record),
		blocks:    make(map[uint64]struct{}),
		next:      1,
	}
	if err := os.MkdirAll(s.blocksDir, 0o755); err != nil {
		return nil, fmt.Errorf("overflow: create dir: %w", err)
	}
	if err := s.loadRecords(); err != nil {
		return nil, err
	}
	if err := s.scanBlocks(); err != nil {
		return nil, err
	}
	dangling, err := s.recoverLocked()
	if err != nil {
		return nil, err
	}
	if len(dangling) > 0 {
		return nil, fmt.Errorf("%w: keys %v reference missing blocks", ErrDanglingRef, dangling)
	}
	return s, nil
}

// RecoveredOrphans 返回本次打开时回收的孤儿块号（升序）。
func (s *Store) RecoveredOrphans() []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]uint64(nil), s.recoveredOrphans...)
}

// Put 写入键值。len(value) <= Threshold 时内联存主记录，否则分配新块
// 写入溢出表并在主记录存块号，二者互斥。更新大值时按 先写新块 ->
// 再持久化新引用 -> 最后回收旧块 的顺序执行。任何校验失败都不会改变
// 主记录、溢出表与块号计数。
func (s *Store) Put(key string, value []byte) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old, exists := s.records[key]
	overflow := len(value) > s.threshold

	// 预先核算块数：溢出新增 1 块，替换旧溢出值随后回收 1 块。
	delta := 0
	if overflow {
		delta++
	}
	if exists && old.Overflow {
		delta--
	}
	if len(s.blocks)+delta > s.maxBlocks {
		return fmt.Errorf("%w: blocks=%d max=%d", ErrBlockLimit, len(s.blocks), s.maxBlocks)
	}

	if overflow {
		n := s.next
		if err := s.writeBlock(n, value); err != nil {
			return err
		}
		s.records[key] = record{Overflow: true, Block: n}
		s.blocks[n] = struct{}{}
		s.next++
		if err := s.persistLocked(); err != nil {
			// 回滚：删除新块、恢复旧记录与计数器，状态与调用前一致。
			os.Remove(s.blockPath(n))
			delete(s.blocks, n)
			s.next--
			if exists {
				s.records[key] = old
			} else {
				delete(s.records, key)
			}
			return err
		}
		if exists && old.Overflow {
			delete(s.blocks, old.Block)
			return s.removeBlock(old.Block)
		}
		return nil
	}

	inline := append([]byte(nil), value...)
	s.records[key] = record{Inline: inline}
	if err := s.persistLocked(); err != nil {
		if exists {
			s.records[key] = old
		} else {
			delete(s.records, key)
		}
		return err
	}
	if exists && old.Overflow {
		delete(s.blocks, old.Block)
		return s.removeBlock(old.Block)
	}
	return nil
}

// Get 读取键值；键不存在时 ok == false。返回值为拷贝，可安全修改。
func (s *Store) Get(key string) (value []byte, ok bool) {
	s.mu.RLock()
	r, exists := s.records[key]
	if !exists {
		s.mu.RUnlock()
		return nil, false
	}
	if !r.Overflow {
		defer s.mu.RUnlock()
		return append([]byte(nil), r.Inline...), true
	}
	block := r.Block
	s.mu.RUnlock()

	// 块一旦写入便不可变，且只有持锁替换引用后才可能被回收，
	// 因此在锁外读块文件是安全的。
	data, err := os.ReadFile(s.blockPath(block))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Delete 删除键并回收其溢出块（如有）。键不存在时为空操作。
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old, exists := s.records[key]
	if !exists {
		return nil
	}
	delete(s.records, key)
	if err := s.persistLocked(); err != nil {
		s.records[key] = old
		return err
	}
	if old.Overflow {
		delete(s.blocks, old.Block)
		return s.removeBlock(old.Block)
	}
	return nil
}

// BlockOf 返回键引用的溢出块号；键不存在或值为内联时 ok == false。
func (s *Store) BlockOf(key string) (block uint64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, exists := s.records[key]
	if !exists || !r.Overflow {
		return 0, false
	}
	return r.Block, true
}

// OverflowBlocks 返回当前溢出表中的块号集合（升序）。
func (s *Store) OverflowBlocks() []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]uint64, 0, len(s.blocks))
	for n := range s.blocks {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Keys 返回全部键（升序）。
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.records))
	for k := range s.records {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Verify 自检：每个引用都必须指向存在的块，且每个块恰被一个引用引用。
// 可与读写并发调用。
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	refCount := make(map[uint64]int, len(s.blocks))
	for k, r := range s.records {
		if !r.Overflow {
			continue
		}
		if _, ok := s.blocks[r.Block]; !ok {
			return fmt.Errorf("%w: key %q references missing block %d", ErrDanglingRef, k, r.Block)
		}
		refCount[r.Block]++
	}
	for n := range s.blocks {
		if c := refCount[n]; c != 1 {
			return fmt.Errorf("%w: block %d referenced %d times", ErrUnreferencedBlock, n, c)
		}
	}
	return nil
}
