// Package sink 负责把聚合结果落盘：先写带校验和的临时文件，
// fsync 后原子改名；崩溃残留的 *.tmp 在启动时识别并清理。
package sink

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Agg 是一个分组的中间聚合。
type Agg struct {
	Sum   int64
	Count int64
}

const suffix = ".tmp"

// State 是一次屏障落盘的完整快照。
type State struct {
	Groups map[string]Agg
	Bad    int
}

// Commit 把状态编码到 <path>.tmp，校验通过后原子改名为 path。
func Commit(path string, offset, bad int, groups map[string]Agg) error {
	tmp := path + suffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	w := bufio.NewWriter(f)
	for _, k := range keys {
		line := strconv.Quote(k) + "\t" +
			strconv.FormatInt(groups[k].Sum, 10) + "\t" +
			strconv.FormatInt(groups[k].Count, 10) + "\n"
		if _, err := h.Write([]byte(line)); err != nil {
			f.Close()
			return err
		}
		if _, err := w.WriteString(line); err != nil {
			f.Close()
			return err
		}
	}
	digest := hex.EncodeToString(h.Sum(nil))[:16]
	tail := fmt.Sprintf("#barrier %d %d %s\n", offset, bad, digest)
	if _, err := w.WriteString(tail); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
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
	return os.Rename(tmp, path)
}

// ErrIncomplete 表示文件不是一次完整提交（截断/损坏/缺尾行）。
var ErrIncomplete = errors.New("sink: incomplete output")

// Load 读回一次完整落盘的状态。任何截断或校验不符都返回 ErrIncomplete，
// 绝不把半截文件当作有效输出。
func Load(path string) (offset, bad int, groups map[string]Agg, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return 0, 0, nil, ErrIncomplete
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "#barrier ") {
		return 0, 0, nil, ErrIncomplete
	}
	fields := strings.Fields(last)
	if len(fields) != 4 {
		return 0, 0, nil, ErrIncomplete
	}
	offset, err1 := strconv.Atoi(fields[1])
	bad, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil {
		return 0, 0, nil, ErrIncomplete
	}
	h := sha256.New()
	groups = map[string]Agg{}
	for _, ln := range lines[:len(lines)-1] {
		h.Write([]byte(ln + "\n"))
		col := strings.Split(ln, "\t")
		if len(col) != 3 {
			return 0, 0, nil, ErrIncomplete
		}
		key, kerr := strconv.Unquote(col[0])
		sum, serr := strconv.ParseInt(col[1], 10, 64)
		cnt, cerr := strconv.ParseInt(col[2], 10, 64)
		if kerr != nil || serr != nil || cerr != nil {
			return 0, 0, nil, ErrIncomplete
		}
		groups[key] = Agg{Sum: sum, Count: cnt}
	}
	if hex.EncodeToString(h.Sum(nil))[:16] != fields[3] {
		return 0, 0, nil, ErrIncomplete
	}
	return offset, bad, groups, nil
}

// Sweep 删除目录中一次未完成改名残留的 *.tmp，返回清理的文件数。
func Sweep(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
