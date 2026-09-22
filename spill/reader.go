package spill

import (
	"bufio"
	"bytes"
	"os"

	"ontology/record"
)

// RecoverPrefix 读出最大可恢复前缀：已完整记录一条不丢，半截记录一条不要。
// 文件完好时返回 nil 错误，否则返回四类分类错误之一。
func RecoverPrefix(path string) ([]record.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	count, err := readHeaderFrom(r)
	if err != nil {
		return nil, err
	}
	recs := make([]record.Record, 0, count)
	for i := uint64(0); i < count; i++ {
		rec, err := readEntry(r)
		if err != nil {
			return recs, err
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// ReadAll 严格读回全部记录，任何截断都报错。
func ReadAll(path string) ([]record.Record, error) {
	return RecoverPrefix(path)
}

// Repair 流式扫描 run 文件，截掉损坏尾部并就地修正头里的 count，
// 返回保留的完整记录数。头本身损坏时返回分类错误。
func Repair(path string) (uint64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	count, err := readHeaderFrom(r)
	if err != nil {
		return 0, err
	}
	good := uint64(0)
	offset := int64(HeaderSize)
	for good < count {
		rec, err := readEntry(r)
		if err != nil {
			if terr := f.Truncate(offset); terr != nil {
				return good, terr
			}
			var hdr bytes.Buffer
			if werr := writeHeaderTo(&hdr, good); werr != nil {
				return good, werr
			}
			if _, werr := f.WriteAt(hdr.Bytes(), 0); werr != nil {
				return good, werr
			}
			return good, f.Sync()
		}
		offset += int64(4 + rec.Size() + 4)
		good++
	}
	return good, nil
}

// Iterator 逐条读取 run 文件；容错：遇截断停止并把错误记入 Err。
type Iterator struct {
	f         *os.File
	r         *bufio.Reader
	remaining uint64
	err       error
}

// NewIterator 打开 run 文件并校验头。
func NewIterator(path string) (*Iterator, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	it := &Iterator{f: f, r: bufio.NewReader(f)}
	count, err := readHeaderFrom(it.r)
	if err != nil {
		f.Close()
		return nil, err
	}
	it.remaining = count
	return it, nil
}

// Next 返回下一条记录；ok=false 表示耗尽或出错（查 Err）。
func (it *Iterator) Next() (rec record.Record, ok bool) {
	if it.remaining == 0 || it.err != nil {
		return rec, false
	}
	rec, err := readEntry(it.r)
	if err != nil {
		it.err = err
		return record.Record{}, false
	}
	it.remaining--
	return rec, true
}

// Err 返回迭代中遇到的分类错误。
func (it *Iterator) Err() error { return it.err }

// Close 关闭底层文件。
func (it *Iterator) Close() error { return it.f.Close() }

// TruncateFileForTest 仅用于测试：把文件截断到 size 字节，模拟任意位置崩溃。
func TruncateFileForTest(path string, size int64) error {
	return os.Truncate(path, size)
}
