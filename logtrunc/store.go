package logtrunc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 磁盘布局：
//
//	dir/log.data          首行为 {"start":N} 头，其后每行一个 Entry（JSON）
//	dir/truncate.marker   截断标记，内容为十进制偏移
const (
	dataFileName   = "log.data"
	markerFileName = "truncate.marker"
)

type dataHeader struct {
	Start uint64 `json:"start"`
}

// encodeData 序列化数据文件：头 + 连续条目。
func encodeData(start uint64, entries []Entry) []byte {
	var buf bytes.Buffer
	head, _ := json.Marshal(dataHeader{Start: start})
	buf.Write(head)
	buf.WriteByte('\n')
	for _, e := range entries {
		line, _ := json.Marshal(e)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// decodeData 解析数据文件，返回实际起始偏移与条目，并校验偏移连贯。
func decodeData(raw []byte) (uint64, []Entry, error) {
	r := bufio.NewReader(bytes.NewReader(raw))
	headLine, err := r.ReadBytes('\n')
	if err != nil && !errorsIsEOF(err, headLine) {
		return 0, nil, fmt.Errorf("logtrunc: read data header: %w", err)
	}
	var head dataHeader
	if err := json.Unmarshal(bytes.TrimSpace(headLine), &head); err != nil {
		return 0, nil, fmt.Errorf("logtrunc: decode data header: %w", err)
	}
	var entries []Entry
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e Entry
			if uerr := json.Unmarshal(bytes.TrimSpace(line), &e); uerr != nil {
				return 0, nil, fmt.Errorf("logtrunc: decode entry: %w", uerr)
			}
			want := head.Start + uint64(len(entries))
			if e.Offset != want {
				return 0, nil, fmt.Errorf("logtrunc: non-contiguous offset: got %d want %d", e.Offset, want)
			}
			entries = append(entries, e)
		}
		if err != nil {
			break
		}
	}
	return head.Start, entries, nil
}

func errorsIsEOF(err error, partial []byte) bool {
	return err == io.EOF && len(partial) > 0
}

// writeFileAtomic 写临时文件、fsync、rename、fsync 目录，保证崩溃安全。
func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// readMarker 读取截断标记；文件不存在时返回实际起始（视为干净）。
func readMarker(dir string, fallback uint64) (uint64, error) {
	raw, err := os.ReadFile(filepath.Join(dir, markerFileName))
	if os.IsNotExist(err) {
		return fallback, nil
	}
	if err != nil {
		return 0, err
	}
	m, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("logtrunc: decode marker: %w", err)
	}
	return m, nil
}
