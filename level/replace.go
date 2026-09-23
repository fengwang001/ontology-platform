package level

import (
	"fmt"
	"ontology/segment"
	"os"
	"path/filepath"
)

// AllocSeq 分配下一个段序号（合并写新段前调用）。
func (s *Store) AllocSeq() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.nextSeq
	s.nextSeq++
	return seq
}

// SegName 返回某层某序号对应的正式段文件名。
func (s *Store) SegName(lvl, seq int) string {
	return filepath.Join(s.dir, fmt.Sprintf("L%d-%06d.seg", lvl, seq))
}

// Snapshot 返回当前层级元数据副本（供 verify / compact）。
func (s *Store) Snapshot() map[int][]Meta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[int][]Meta, len(s.levels))
	for l, ms := range s.levels {
		out[l] = append([]Meta(nil), ms...)
	}
	return out
}

// MaxLevel 返回当前最深层号，无段时返回 -1。
func (s *Store) MaxLevel() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	max := -1
	for l := range s.levels {
		if l > max {
			max = l
		}
	}
	return max
}

// Reader 返回指定段的只读句柄（供 compact 建源）。
func (s *Store) Reader(path string) *segment.Reader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readers[path]
}

// Replace 原子替换：写锁内摘除 old、登记新段，锁外再关闭删除旧段文件。
// 读者全程持读锁，看到的快照必然自洽，无「旧删新未就位」空窗。
func (s *Store) Replace(old []Meta, add Meta, r *segment.Reader) {
	s.mu.Lock()
	for _, om := range old {
		ms := s.levels[om.Level]
		for i, m := range ms {
			if m.Path == om.Path {
				s.levels[om.Level] = append(ms[:i], ms[i+1:]...)
				break
			}
		}
		if len(s.levels[om.Level]) == 0 {
			delete(s.levels, om.Level)
		}
	}
	s.readers[add.Path] = r
	s.levels[add.Level] = insertMeta(s.levels[add.Level], add)
	oldReaders := make([]*segment.Reader, 0, len(old))
	for _, om := range old {
		if or := s.readers[om.Path]; or != nil {
			oldReaders = append(oldReaders, or)
		}
		delete(s.readers, om.Path)
	}
	s.mu.Unlock()
	for _, or := range oldReaders {
		or.Close()
	}
	for _, om := range old {
		os.Remove(om.Path)
	}
}

// Close 关闭全部段句柄。
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.readers {
		r.Close()
	}
	s.readers = map[string]*segment.Reader{}
}
