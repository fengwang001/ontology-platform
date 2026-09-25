// Package sink 负责聚合快照的原子落盘与校验。
package sink

import (
	"bufio"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Group 是单个分组的中间聚合。
type Group struct {
	Key string
	Sum int64
	Cnt int64
}

// Snapshot 是一次屏障处的完整输出状态。
type Snapshot struct {
	Off  int64
	Bad  int64
	Rows []Group
}

const tmpSuffix = ".tmp"

func tmpPath(path string) string { return path + tmpSuffix }

// WriteCrashTmp 写完临时文件并刷盘，但在原子改名前硬退出（故障注入）。
func WriteCrashTmp(path string, snap Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rows := append([]Group(nil), snap.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	f, err := os.Create(tmpPath(path))
	if err != nil {
		return err
	}
	h := crc32.NewIEEE()
	w := bufio.NewWriter(f)
	mw := io.MultiWriter(w, h)
	fmt.Fprintf(mw, "off=%d bad=%d\n", snap.Off, snap.Bad)
	for _, g := range rows {
		fmt.Fprintf(mw, "%s %d %d\n", g.Key, g.Sum, g.Cnt)
	}
	fmt.Fprintf(w, "crc=%08x\n", h.Sum32())
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	f.Close()
	os.Exit(99)
	return nil
}

// Write 先写临时文件，刷盘后原子改名；末尾附 CRC32 校验行。
// 输出按 key 排序，保证任意重放逐字节一致。
func Write(path string, snap Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rows := append([]Group(nil), snap.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	f, err := os.Create(tmpPath(path))
	if err != nil {
		return err
	}
	tmp := f.Name()
	h := crc32.NewIEEE()
	w := bufio.NewWriter(f)
	mw := io.MultiWriter(w, h)
	fmt.Fprintf(mw, "off=%d bad=%d\n", snap.Off, snap.Bad)
	for _, g := range rows {
		fmt.Fprintf(mw, "%s %d %d\n", g.Key, g.Sum, g.Cnt)
	}
	fmt.Fprintf(w, "crc=%08x\n", h.Sum32())
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// CleanTemp 删除目录中全部 *.tmp 残留，返回清理数量。
func CleanTemp(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// Read 读回正式输出并校验 CRC；不匹配返回错误。
func Read(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var snap Snapshot
	var want uint64
	h := crc32.NewIEEE()
	for i, ln := range lines {
		if i == len(lines)-1 {
			if _, err := fmt.Sscanf(ln, "crc=%08x", &want); err != nil {
				return Snapshot{}, fmt.Errorf("sink: missing checksum")
			}
			break
		}
		h.Write([]byte(ln + "\n"))
		if i == 0 {
			if _, err := fmt.Sscanf(ln, "off=%d bad=%d", &snap.Off, &snap.Bad); err != nil {
				return Snapshot{}, err
			}
			continue
		}
		key, rest, ok := strings.Cut(ln, " ")
		if !ok {
			return Snapshot{}, fmt.Errorf("sink: bad row")
		}
		fields := strings.SplitN(rest, " ", 2)
		if len(fields) != 2 {
			return Snapshot{}, fmt.Errorf("sink: bad row")
		}
		sum, _ := strconv.ParseInt(fields[0], 10, 64)
		cnt, _ := strconv.ParseInt(fields[1], 10, 64)
		snap.Rows = append(snap.Rows, Group{Key: key, Sum: sum, Cnt: cnt})
	}
	if uint64(h.Sum32()) != want {
		return Snapshot{}, fmt.Errorf("sink: checksum mismatch")
	}
	return snap, nil
}
