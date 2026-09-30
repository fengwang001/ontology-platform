package lsm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// segmentFileName 返回段文件名，层与编号编码在文件名中。
func segmentFileName(id uint64, level int) string {
	return fmt.Sprintf("seg-L%d-%020d.kvs", level, id)
}

// Segment 是一个不可变段：一组按写入时间有序的记录，落盘后不再修改。
type Segment struct {
	ID      uint64
	Level   int
	Records map[string]Record
	MinSeq  uint64
	MaxSeq  uint64
	path    string
}

// Get 在段内查找键。
func (s *Segment) Get(key string) (Record, bool) {
	rec, ok := s.Records[key]
	return rec, ok
}

// buildSegment 把记录写入一个新的段文件并返回加载后的段。
// 先写临时文件再原子重命名，任何失败都不会留下半成品段。
func buildSegment(dir string, id uint64, level int, records []Record) (*Segment, error) {
	final := filepath.Join(dir, segmentFileName(id, level))
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	for _, rec := range records {
		if _, err := f.Write(encodeRecord(rec)); err != nil {
			return nil, err
		}
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	ok = true
	return loadSegment(final, id, level)
}

// loadSegment 从磁盘加载段文件。尾部不完整记录被安全截断；
// 中间出现非法记录则返回 ErrCorruptSegment。
func loadSegment(path string, id uint64, level int) (*Segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seg := &Segment{
		ID:      id,
		Level:   level,
		Records: make(map[string]Record),
		path:    path,
	}
	off := 0
	for off < len(data) {
		rec, n, err := decodeRecord(data[off:])
		if err != nil {
			if errors.Is(err, errIncomplete) {
				// 尾部不完整（例如上次写入中途崩溃）：安全截断，
				// 已解码的记录不受影响。
				break
			}
			return nil, fmt.Errorf("%w: %s: %v", ErrCorruptSegment, filepath.Base(path), err)
		}
		seg.add(rec)
		off += n
	}
	return seg, nil
}

// add 把记录并入段内索引，同键保留序号更大的一条。
func (s *Segment) add(rec Record) {
	key := string(rec.Key)
	if old, ok := s.Records[key]; ok && old.Seq > rec.Seq {
		return
	}
	s.Records[key] = rec
	if len(s.Records) == 1 || rec.Seq < s.MinSeq {
		s.MinSeq = rec.Seq
	}
	if rec.Seq > s.MaxSeq {
		s.MaxSeq = rec.Seq
	}
}

// removeFile 删除段对应的磁盘文件。
func (s *Segment) removeFile() {
	if s.path != "" {
		os.Remove(s.path)
	}
}

// decodeAll 重新解码段文件用于自检。
func (s *Segment) decodeAll() error {
	if s.path == "" {
		return nil
	}
	if _, err := loadSegment(s.path, s.ID, s.Level); err != nil {
		return err
	}
	return nil
}
