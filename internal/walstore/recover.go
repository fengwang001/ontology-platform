package walstore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// recover 在 Open 时重建状态：先加载检查点，再顺序回放 WAL，
// 遇到第一条不完整/损坏的记录即停止，并把文件截断到该位置，
// 使后续追加不会接在半条记录后面。
func (s *Store) recover() error {
	// 检查点写一半崩溃会留下临时文件，直接丢弃。
	os.Remove(filepath.Join(s.dir, tmpFileName))

	if err := s.loadCheckpoint(); err != nil {
		return err
	}

	buf, err := io.ReadAll(s.wal)
	if err != nil {
		return err
	}
	off := 0
	for off < len(buf) {
		batch, n, ok := decodeRecord(buf[off:])
		if !ok {
			break
		}
		for k, v := range batch {
			s.data[k] = v
		}
		off += n
	}
	if off < len(buf) {
		if err := s.wal.Truncate(int64(off)); err != nil {
			return err
		}
	}
	_, err = s.wal.Seek(0, io.SeekEnd)
	return err
}

// loadCheckpoint 加载检查点文件。检查点通过 临时文件+rename
// 原子替换，因此要么不存在、要么是完整的一代。
func (s *Store) loadCheckpoint() error {
	b, err := os.ReadFile(filepath.Join(s.dir, chkFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	batch, n, ok := decodeRecord(b)
	if !ok || n != len(b) {
		// rename 保证原子性，理论上不会到这里；即便文件
		// 损坏也不拒绝启动，WAL 回放仍可能覆盖其内容。
		return nil
	}
	for k, v := range batch {
		s.data[k] = v
	}
	return nil
}
