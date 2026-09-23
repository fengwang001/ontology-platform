// Package level 管理多层段文件：L0 段间键范围可重叠，L1+ 层内不重叠。
// 胜出规则：层号小者新；同层（仅 L0）段序号大者新。
package level

import (
	"bytes"
	"errors"
	"fmt"
	"ontology/iter"
	"ontology/segment"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrInvalidRange = errors.New("level: scan start > end")

// Meta 描述一个已就位的段。
type Meta struct {
	Path           string
	Level, Seq     int
	MinKey, MaxKey string
	Count          int
}

// Store 是层级存储。读操作持读锁，合并替换持写锁，替换后新段已就位，无读空窗。
type Store struct {
	dir     string
	mu      sync.RWMutex
	levels  map[int][]Meta // L0 按 Seq 升序；L1+ 按 MinKey 升序
	readers map[string]*segment.Reader
	nextSeq int
}

// OpenStore 打开目录，清理合并崩溃残留的 .tmp 文件并加载全部 .seg。
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, levels: map[int][]Meta{}, readers: map[string]*segment.Reader{}}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), ".tmp") {
			os.Remove(p) // 崩溃恢复：半截新段直接清理
			continue
		}
		if !strings.HasSuffix(e.Name(), ".seg") {
			continue
		}
		var lvl, seq int
		if _, err := fmt.Sscanf(e.Name(), "L%d-%d.seg", &lvl, &seq); err != nil {
			continue
		}
		m, r, err := Load(p, lvl, seq)
		if err != nil {
			return nil, err
		}
		s.readers[p] = r
		s.levels[lvl] = insertMeta(s.levels[lvl], m)
		if seq >= s.nextSeq {
			s.nextSeq = seq + 1
		}
	}
	return s, nil
}

// Load 打开一个已就位的段文件并计算其元数据（供 compact 登记新段）。
func Load(path string, lvl, seq int) (Meta, *segment.Reader, error) {
	r, err := segment.Open(path)
	if err != nil {
		return Meta{}, nil, err
	}
	m := Meta{Path: path, Level: lvl, Seq: seq, Count: r.EntryCount()}
	src := iter.NewSource(r, lvl, seq)
	first := true
	for src.Next() {
		e := src.Entry()
		if first {
			m.MinKey = string(e.Key)
			first = false
		}
		m.MaxKey = string(e.Key)
	}
	return m, r, src.Err()
}

func insertMeta(ms []Meta, m Meta) []Meta {
	i := sort.Search(len(ms), func(i int) bool {
		if m.Level == 0 {
			return ms[i].Seq >= m.Seq
		}
		return ms[i].MinKey >= m.MinKey
	})
	return append(ms[:i], append([]Meta{m}, ms[i:]...)...)
}

// Ingest 把一个段文件纳入指定层（原子改名进目录）。
func (s *Store) Ingest(path string, lvl int) (Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.nextSeq
	s.nextSeq++
	dst := filepath.Join(s.dir, fmt.Sprintf("L%d-%06d.seg", lvl, seq))
	if err := os.Rename(path, dst); err != nil {
		return Meta{}, err
	}
	m, r, err := Load(dst, lvl, seq)
	if err != nil {
		return Meta{}, err
	}
	s.readers[dst] = r
	s.levels[lvl] = insertMeta(s.levels[lvl], m)
	return m, nil
}

// Get 点查：先 L0（Seq 降序），再 L1..Ln；首个命中（值或删除标记）即胜。
func (s *Store) Get(key []byte) ([]byte, segment.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ms := range s.ordered() {
		for i := len(ms) - 1; i >= 0; i-- { // L0 后写先查；L1+ 顺序无关
			m := ms[i]
			if string(key) < m.MinKey || string(key) > m.MaxKey {
				continue
			}
			val, st, err := s.readers[m.Path].Get(key)
			if err != nil || st != segment.StateAbsent {
				return val, st, err
			}
		}
	}
	return nil, segment.StateAbsent, nil
}

func (s *Store) ordered() [][]Meta {
	lvls := make([]int, 0, len(s.levels))
	for l := range s.levels {
		lvls = append(lvls, l)
	}
	sort.Ints(lvls)
	out := make([][]Meta, 0, len(lvls))
	for _, l := range lvls {
		out = append(out, s.levels[l])
	}
	return out
}

// Scan 范围扫描 [start, end)，跨段堆归并，删除标记不外泄。
func (s *Store) Scan(start, end []byte) ([]iter.Entry, error) {
	if bytes.Compare(start, end) > 0 {
		return nil, ErrInvalidRange
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var srcs []*iter.Source
	for _, ms := range s.levels {
		for _, m := range ms {
			if string(end) <= m.MinKey || string(start) > m.MaxKey {
				continue
			}
			srcs = append(srcs, iter.NewSource(s.readers[m.Path], m.Level, m.Seq))
		}
	}
	merger := iter.NewMerger(srcs, false)
	var out []iter.Entry
	for e, ok := merger.Next(); ok; e, ok = merger.Next() {
		if bytes.Compare(e.Key, start) >= 0 && bytes.Compare(e.Key, end) < 0 {
			out = append(out, e)
		}
	}
	return out, nil
}
