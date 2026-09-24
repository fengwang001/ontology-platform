package segment

import (
	"errors"
	"io"
	"os"
)

// Report 描述一个段文件的可读情况。
type Report struct {
	Header      Header
	Frames      uint64 // 完整可读帧数
	ValidEnd    int64  // 最后一个完整帧的尾偏移（截断点）
	Err         error  // 首个错误；nil 表示整个快照都是完整帧
	HeaderCount uint64 // 段头声明条数
}

// CountMismatch 在帧流完整但声明条数多于实际帧数时报告 ErrCountMismatch。
func (rep Report) CountMismatch() bool {
	return errors.Is(rep.Err, ErrCountMismatch)
}

// Inspect 读取整个段（以当前大小为快照），分类首个损坏并给出最大可恢复前缀。
func Inspect(path string) (Report, error) {
	r, err := Open(path)
	if err != nil {
		return Report{Err: err}, err
	}
	defer r.Close()

	rep := Report{Header: r.h, HeaderCount: r.h.Count, ValidEnd: int64(HeaderSize)}
	for {
		off, _, nerr := r.Next()
		if nerr == nil {
			rep.Frames = r.Frames()
			rep.ValidEnd = r.off
			continue
		}
		if errors.Is(nerr, io.EOF) {
			rep.Err = nil
			break
		}
		rep.Err = nerr
		_ = off
		break
	}
	if rep.Err == nil && r.h.Count > rep.Frames {
		rep.Err = ErrCountMismatch
	}
	return rep, nil
}

// RewriteHeader 在原地把段头改写为给定内容并同步，文件其余字节不变。
func RewriteHeader(path string, h Header) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteAt(encodeHeader(h), 0); err != nil {
		return err
	}
	return f.Sync()
}

// Truncate 把段截到给定字节偏移（用于修复到最大可恢复前缀）。
func Truncate(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return err
	}
	return f.Sync()
}
