package truncation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// 磁盘布局（dir 下两个文件）：
//   - log.dat：首行为 header {"type":"header","start":S}，随后每行一个条目；
//   - truncate.marker：{"marker":M}，截断第一步落盘，崩溃恢复的判定依据。

const (
	logFileName    = "log.dat"
	markerFileName = "truncate.marker"
)

type logLine struct {
	Type   string `json:"type"` // "header" 或 "entry"
	Start  uint64 `json:"start,omitempty"`
	Offset uint64 `json:"offset,omitempty"`
	Data   []byte `json:"data,omitempty"`
}

type markerFile struct {
	Marker uint64 `json:"marker"`
}

func logPath(dir string) string    { return filepath.Join(dir, logFileName) }
func markerPath(dir string) string { return filepath.Join(dir, markerFileName) }

// writeFileAtomic 写临时文件 + fsync + rename + fsync 目录，保证崩溃后要么旧要么新。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeMarker 截断第一步：落截断标记。
func writeMarker(dir string, marker uint64) error {
	data, err := json.Marshal(markerFile{Marker: marker})
	if err != nil {
		return err
	}
	return writeFileAtomic(markerPath(dir), data)
}

// readMarker 读取截断标记；文件不存在视为 0（从未截断）。
func readMarker(dir string) (uint64, error) {
	data, err := os.ReadFile(markerPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var mf markerFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return 0, fmt.Errorf("truncation: corrupt marker file: %w", err)
	}
	return mf.Marker, nil
}

// appendEntry 向日志文件追加一个条目；文件不存在时先写 header。
// 故意不 fsync：条目是否落盘由 DeclareDurable 声明驱动。
func appendEntry(dir string, start uint64, e Entry) error {
	path := logPath(dir)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		header, _ := json.Marshal(logLine{Type: "header", Start: start})
		if err := writeFileAtomic(path, append(header, '\n')); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := json.Marshal(logLine{Type: "entry", Offset: e.Offset, Data: e.Data})
	_, err = f.Write(append(line, '\n'))
	return err
}

// syncLog 将日志文件 fsync，用于 DeclareDurable 落盘。
func syncLog(dir string) error {
	f, err := os.OpenFile(logPath(dir), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// rewriteLog 截断第二步：物理删除前缀，重写整个日志文件。
func rewriteLog(dir string, start uint64, entries []Entry) error {
	var buf []byte
	header, _ := json.Marshal(logLine{Type: "header", Start: start})
	buf = append(buf, header...)
	buf = append(buf, '\n')
	for _, e := range entries {
		line, _ := json.Marshal(logLine{Type: "entry", Offset: e.Offset, Data: e.Data})
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return writeFileAtomic(logPath(dir), buf)
}

// readLog 读取实际起始偏移与全部可见条目，并校验偏移连续性。
func readLog(dir string) (uint64, []Entry, error) {
	f, err := os.Open(logPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return 1, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()

	var start uint64
	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	first := true
	for scanner.Scan() {
		var line logLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return 0, nil, fmt.Errorf("truncation: corrupt log file: %w", err)
		}
		switch {
		case first && line.Type == "header":
			start = line.Start
		case line.Type == "entry":
			entries = append(entries, Entry{Offset: line.Offset, Data: line.Data})
		default:
			return 0, nil, fmt.Errorf("truncation: corrupt log file: unexpected line type %q", line.Type)
		}
		first = false
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	}
	if start == 0 {
		start = 1
	}
	for i, e := range entries {
		want := start + uint64(i)
		if e.Offset != want {
			return 0, nil, fmt.Errorf("truncation: corrupt log file: offset gap at index %d, want %d got %d", i, want, e.Offset)
		}
	}
	return start, entries, nil
}
