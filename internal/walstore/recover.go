package walstore

import (
	"io"
	"os"
)

// replayWAL 顺序回放 wal.log：
//   - 完整且 CRC 正确的记录逐条应用到内存；
//   - 一旦遇到 EOF、不完整记录或校验失败，立即停止，
//     并把文件截断到最后一条好记录之后（丢弃尾部垃圾）；
//   - 随后以追加模式打开该文件，保证新记录不会接在半条记录之后。
func (s *Store) replayWAL() error {
	f, err := os.OpenFile(s.walPath(), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}

	validOffset := int64(0)
	for {
		batch, recLen, err := readRecord(f)
		if err == io.EOF {
			break
		}
		if err != nil {
			// errCorruptRecord：撕裂写或尾部垃圾，停止回放。
			break
		}
		for k, v := range batch {
			s.data[k] = v
		}
		validOffset += int64(recLen)
	}

	// 丢弃尾部垃圾，并同步截断结果，避免崩溃残留重新出现。
	if err := f.Truncate(validOffset); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Seek(validOffset, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	s.wal = f
	return nil
}
