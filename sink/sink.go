// Package sink 负责把聚合快照原子落盘：临时文件 + 校验和 + rename。
package sink

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Row 是一个分组的聚合结果。
type Row struct {
	Key string
	Sum int64
	Cnt int64
}

// Sink 是落盘器。Delay>0 时每条记录模拟慢下游，用于验证背压。
type Sink struct {
	dir        string
	delay      time.Duration
	preRename  func()
	failAt     int64
	failCalled bool
}

// New 创建落盘器。failPos>=0 时在写出对应 pos 的文件前返回错误。
func New(dir string, delay time.Duration, preRename func(), failPos int64) *Sink {
	return &Sink{dir: dir, delay: delay, preRename: preRename, failAt: failPos}
}

// Encode 把快照编码为确定性字节：按 key 排序，每行 key=sum,cnt。
func Encode(rows []Row) []byte {
	sorted := append([]Row(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	var b strings.Builder
	for _, r := range sorted {
		fmt.Fprintf(&b, "%s=%d,%d\n", r.Key, r.Sum, r.Cnt)
	}
	return []byte(b.String())
}

// Commit 写 out-<pos>。返回之前模拟每条记录的延迟（用于在屏障处统一计入）。
func (s *Sink) Commit(pos int64, rows []Row) (string, error) {
	if s.failAt >= 0 && pos >= s.failAt && !s.failCalled {
		s.failCalled = true
		return "", fmt.Errorf("sink: injected failure at pos %d", pos)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	payload := Encode(rows)
	sum := sha256.Sum256(payload)
	final := filepath.Join(s.dir, fmt.Sprintf("out-%d", pos))
	tmp := final + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	w := bufio.NewWriter(f)
	w.Write(payload)
	fmt.Fprintf(w, "sha256=%s\n", hex.EncodeToString(sum[:]))
	if err := w.Flush(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if s.preRename != nil {
		s.preRename()
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	return final, nil
}

// DelayPerRecord 返回每条记录的模拟延迟。
func (s *Sink) DelayPerRecord(n int) {
	if s.delay > 0 {
		time.Sleep(s.delay * time.Duration(n))
	}
}

// Verify 校验已落盘文件：必须有 sha256 尾行且与载荷一致。
func Verify(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "sha256=") {
		return fmt.Errorf("sink: %s missing checksum trailer", filepath.Base(path))
	}
	want, err := hex.DecodeString(strings.TrimPrefix(last, "sha256="))
	if err != nil {
		return fmt.Errorf("sink: %s bad checksum encoding", filepath.Base(path))
	}
	body := strings.Join(lines[:len(lines)-1], "\n")
	if len(lines) > 1 {
		body += "\n"
	}
	got := sha256.Sum256([]byte(body))
	if hex.EncodeToString(got[:]) != hex.EncodeToString(want) {
		return fmt.Errorf("sink: %s checksum mismatch", filepath.Base(path))
	}
	return nil
}

// CleanupTemp 删除目录下全部 *.tmp，返回删除数量。
func CleanupTemp(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// LatestOutput 返回目录中 pos 最大的有效输出文件及其位置。
func LatestOutput(dir string) (string, int64, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", -1, nil
	}
	if err != nil {
		return "", -1, err
	}
	bestPath, bestPos := "", int64(-1)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "out-") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		var pos int64
		if _, err := fmt.Sscanf(name, "out-%d", &pos); err != nil {
			continue
		}
		path := filepath.Join(dir, name)
		if Verify(path) != nil {
			continue
		}
		if pos >= bestPos {
			bestPos, bestPath = pos, path
		}
	}
	return bestPath, bestPos, nil
}
