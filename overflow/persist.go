package overflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	recordsFile = "records.json"
	blocksDir   = "blocks"
	blockSuffix = ".blk"
)

// diskRecord 主记录的落盘形式，Overflow 与 Inline 互斥。
type diskRecord struct {
	Overflow bool   `json:"overflow"`
	Inline   []byte `json:"inline,omitempty"`
	Block    uint64 `json:"block,omitempty"`
}

// diskState 主记录文件内容：全部主记录 + 块号计数器。
type diskState struct {
	Next    uint64                `json:"next"`
	Records map[string]diskRecord `json:"records"`
}

func (s *Store) recordsPath() string { return filepath.Join(s.dir, recordsFile) }

func (s *Store) blockPath(n uint64) string {
	return filepath.Join(s.blocksDir, strconv.FormatUint(n, 10)+blockSuffix)
}

// writeBlock 把溢出值写入新块文件并 fsync（含目录），
// 保证在引用被持久化之前块一定已落盘。
func (s *Store) writeBlock(n uint64, data []byte) error {
	f, err := os.OpenFile(s.blockPath(n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("overflow: write block %d: %w", n, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(s.blockPath(n))
		return fmt.Errorf("overflow: write block %d: %w", n, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(s.blockPath(n))
		return fmt.Errorf("overflow: sync block %d: %w", n, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("overflow: close block %d: %w", n, err)
	}
	return syncDir(s.blocksDir)
}

// removeBlock 回收溢出块（删除块文件）。块已不存在视为成功。
func (s *Store) removeBlock(n uint64) error {
	if err := os.Remove(s.blockPath(n)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("overflow: remove block %d: %w", n, err)
	}
	return syncDir(s.blocksDir)
}

// persistLocked 原子地持久化主记录与块号计数器（写临时文件 + rename + fsync）。
// 调用方必须持有写锁。
func (s *Store) persistLocked() error {
	st := diskState{Next: s.next, Records: make(map[string]diskRecord, len(s.records))}
	for k, r := range s.records {
		st.Records[k] = diskRecord{Overflow: r.Overflow, Inline: r.Inline, Block: r.Block}
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("overflow: marshal records: %w", err)
	}
	tmp := s.recordsPath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("overflow: write records: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("overflow: write records: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("overflow: sync records: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("overflow: close records: %w", err)
	}
	if err := os.Rename(tmp, s.recordsPath()); err != nil {
		return fmt.Errorf("overflow: rename records: %w", err)
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("overflow: open dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("overflow: sync dir %s: %w", dir, err)
	}
	return nil
}

// loadRecords 读取主记录文件；不存在时返回空状态。
func (s *Store) loadRecords() error {
	data, err := os.ReadFile(s.recordsPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("overflow: read records: %w", err)
	}
	var st diskState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("overflow: parse records: %w", err)
	}
	s.next = st.Next
	if s.next == 0 {
		s.next = 1
	}
	for k, dr := range st.Records {
		s.records[k] = record{Overflow: dr.Overflow, Inline: dr.Inline, Block: dr.Block}
	}
	return nil
}

// scanBlocks 扫描溢出表目录，返回盘上存在的块号集合。
func (s *Store) scanBlocks() error {
	entries, err := os.ReadDir(s.blocksDir)
	if err != nil {
		return fmt.Errorf("overflow: scan blocks dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, blockSuffix) {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(name, blockSuffix), 10, 64)
		if err != nil {
			continue
		}
		s.blocks[n] = struct{}{}
		// 崩溃可能发生在块已写入但计数器未持久化之间，
		// 用盘上最大块号抬升计数器，保证块号永不复用。
		if n >= s.next {
			s.next = n + 1
		}
	}
	return nil
}

// recoverLocked 回收孤儿块并检出悬挂引用。返回悬挂引用的键列表。
func (s *Store) recoverLocked() ([]string, error) {
	referenced := make(map[uint64]struct{})
	for _, r := range s.records {
		if r.Overflow {
			referenced[r.Block] = struct{}{}
		}
	}
	// 回收无引用的孤儿块。
	for n := range s.blocks {
		if _, ok := referenced[n]; ok {
			continue
		}
		if err := s.removeBlock(n); err != nil {
			return nil, err
		}
		delete(s.blocks, n)
		s.recoveredOrphans = append(s.recoveredOrphans, n)
	}
	sort.Slice(s.recoveredOrphans, func(i, j int) bool {
		return s.recoveredOrphans[i] < s.recoveredOrphans[j]
	})
	// 检出指向不存在块的悬挂引用。
	var dangling []string
	for k, r := range s.records {
		if !r.Overflow {
			continue
		}
		if _, ok := s.blocks[r.Block]; !ok {
			dangling = append(dangling, k)
		}
	}
	sort.Strings(dangling)
	return dangling, nil
}
