// Package progress 实现带 CRC32 的进度文件：记录已成功写入的记录区间，
// 支持截断恢复（最大可恢复前缀）与断点续传。
package progress

import (
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"strconv"
	"strings"

	"ontology/batch"
)

// 可判定的截断分类错误。
var (
	ErrHeaderIncomplete   = errors.New("progress: header incomplete")
	ErrIntervalIncomplete = errors.New("progress: interval record incomplete")
	ErrCRCMismatch        = errors.New("progress: crc mismatch")
)

// Class 是进度文件的恢复分类。
type Class int

const (
	// ClassOK 文件完整合法。
	ClassOK Class = iota
	// ClassHeaderIncomplete 头部不完整。
	ClassHeaderIncomplete
	// ClassIntervalIncomplete 区间记录不完整。
	ClassIntervalIncomplete
	// ClassCRCMismatch CRC 不匹配。
	ClassCRCMismatch
)

// Err 返回分类对应的可判定错误，ClassOK 返回 nil。
func (c Class) Err() error {
	switch c {
	case ClassHeaderIncomplete:
		return ErrHeaderIncomplete
	case ClassIntervalIncomplete:
		return ErrIntervalIncomplete
	case ClassCRCMismatch:
		return ErrCRCMismatch
	}
	return nil
}

// File 是一个批次的进度：所属批次与已写入区间列表。
type File struct {
	BatchID   string
	Intervals []batch.Interval
	path      string
}

func crcOf(s string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(s)))
}

func headerLine(id string) string {
	body := "PGR1 " + id
	return body + " " + crcOf(body) + "\n"
}

func intervalLine(iv batch.Interval) string {
	body := fmt.Sprintf("I %d %d", iv.Start, iv.End)
	return body + " " + crcOf(body) + "\n"
}

// Recover 解析 data，返回最大可恢复前缀表示的进度与截断分类。
func Recover(data []byte) (*File, Class) {
	f := &File{}
	text := string(data)
	nl := strings.IndexByte(text, '\n')
	if nl < 0 {
		return f, ClassHeaderIncomplete
	}
	head := strings.Split(text[:nl], " ")
	if len(head) != 3 || head[0] != "PGR1" {
		return f, ClassHeaderIncomplete
	}
	if crcOf(head[0]+" "+head[1]) != head[2] {
		return f, ClassCRCMismatch
	}
	f.BatchID = head[1]
	rest := text[nl+1:]
	for len(rest) > 0 {
		line := rest
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line = rest[:i]
			rest = rest[i+1:]
		} else {
			rest = ""
		}
		if line == "" {
			continue
		}
		parts := strings.Split(line, " ")
		if len(parts) != 4 || parts[0] != "I" {
			return f, ClassIntervalIncomplete
		}
		start, err1 := strconv.Atoi(parts[1])
		end, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			return f, ClassIntervalIncomplete
		}
		if crcOf(fmt.Sprintf("I %d %d", start, end)) != parts[3] {
			return f, ClassCRCMismatch
		}
		f.Intervals = append(f.Intervals, batch.Interval{Start: start, End: end})
	}
	return f, ClassOK
}

// Open 加载 path 的进度文件：不存在视为从头开始（非错误）；
// 截断文件回退到最大可恢复前缀并物理截断该文件。
func Open(path, id string) (*File, Class, error) {
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		f := &File{BatchID: id, path: path}
		return f, ClassOK, os.WriteFile(path, []byte(headerLine(id)), 0o644)
	case err != nil:
		return nil, 0, err
	}
	f, class := Recover(data)
	f.path = path
	if class != ClassOK {
		f.BatchID = id
		if err := f.rewrite(); err != nil {
			return nil, class, err
		}
	}
	return f, class, nil
}

// rewrite 将内存中的进度完整重写回文件（用于截断恢复后收敛）。
func (f *File) rewrite() error {
	var b strings.Builder
	b.WriteString(headerLine(f.BatchID))
	for _, iv := range f.Intervals {
		b.WriteString(intervalLine(iv))
	}
	return os.WriteFile(f.path, []byte(b.String()), 0o644)
}

// Flush 追加一个已写入区间并刷盘（粗粒度：每 N 条调用一次）。
func (f *File) Flush(iv batch.Interval) error {
	file, err := os.OpenFile(f.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(intervalLine(iv)); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	f.Intervals = append(f.Intervals, iv)
	return nil
}

// Path 返回进度文件路径。
func (f *File) Path() string { return f.path }

// Contiguous 返回从 0 起连续覆盖的高水位（已确认写入的记录数）。
func (f *File) Contiguous() int {
	cur := 0
	for _, iv := range f.Intervals {
		if iv.Start > cur {
			break
		}
		if iv.End > cur {
			cur = iv.End
		}
	}
	return cur
}
