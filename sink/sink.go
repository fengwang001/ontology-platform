package sink

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Group 是一个分组的中间/最终聚合值。
type Group struct {
	Key   string
	Value int64
}

// Snapshot 是一次屏障时刻的确定性全量聚合快照。
type Snapshot struct {
	Offset int64
	Groups []Group
}

var trailerMagic = []byte("\n#cksum=")

// Sink 把快照落盘：先写临时文件，再 fsync，最后原子改名。
type Sink struct {
	path string
	tmp  string
}

func New(dir, name string) *Sink {
	return &Sink{path: filepath.Join(dir, name), tmp: filepath.Join(dir, name+".tmp")}
}

// Marshal 把快照编码为确定性字节：组按 key 排序，每行 key=value，
// 末尾追加 SHA256 校验和 trailer。相同快照必然逐字节相同。
func Marshal(s Snapshot) []byte {
	groups := append([]Group(nil), s.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	var b strings.Builder
	fmt.Fprintf(&b, "offset=%d\n", s.Offset)
	for _, g := range groups {
		fmt.Fprintf(&b, "%s=%d\n", g.Key, g.Value)
	}
	body := b.String()
	sum := sha256.Sum256([]byte(body))
	return []byte(body + string(trailerMagic) + hex.EncodeToString(sum[:]) + "\n")
}

func verify(data []byte) error {
	idx := strings.LastIndex(string(data), string(trailerMagic))
	if idx < 0 {
		return errors.New("sink: no checksum trailer")
	}
	body, tail := data[:idx], data[idx+len(trailerMagic):]
	got := strings.TrimSuffix(string(tail), "\n")
	sum := sha256.Sum256(body)
	if got != hex.EncodeToString(sum[:]) {
		return errors.New("sink: checksum mismatch")
}
	return nil
}

// Commit 写临时文件、fsync、原子改名为正式输出。
func (s *Sink) Commit(snap Snapshot) error {
	data := Marshal(snap)
	f, err := os.OpenFile(s.tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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
	return os.Rename(s.tmp, s.path)
}

// CleanupTemp 删除未完成的临时文件残留；不存在不算错误。
func (s *Sink) CleanupTemp() error {
	if err := os.Remove(s.tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Valid 判断给定字节是否为一份完整且校验通过的输出。
// 任意截断点都因缺 trailer 或校验和不匹配而被判无效。
func Valid(data []byte) bool { return verify(data) == nil }

// Read 读回正式输出并校验；不完整或损坏返回错误。
func (s *Sink) Read() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	if err := verify(data); err != nil {
		return nil, err
	}
	return data, nil
}

var _ io.Reader // keep io import semantics stable
