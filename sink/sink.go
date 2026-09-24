// Package sink 负责把聚合状态原子落盘：临时文件 + 校验和 + 改名。
package sink

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Group 是单个分组的中间聚合。
type Group struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
	Sum int64 `json:"sum"`
	N   int64 `json:"n"`
}

// State 是一份完整的聚合状态。
type State struct {
	Groups map[string]Group `json:"groups"`
	Offset int64            `json:"offset"`
}

// ErrCorrupt 表示文件校验和不匹配或内容损坏。
var ErrCorrupt = errors.New("sink: corrupt file")

// Sink 管理一个目录下的输出文件。
type Sink struct {
	dir string
	// CrashBeforeRename>0 时，下一次 Commit 写临时文件后直接崩溃模拟，
	// 不改名、不 fsync 完成；每调用一次减一。
	CrashBeforeRename int
}

// New 创建输出管理器并清理目录中残留的临时文件（未完成输出）。
func New(dir string) (*Sink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := CleanTemp(dir); err != nil {
		return nil, err
	}
	return &Sink{dir: dir}, nil
}

// OutputPath 是最终输出文件路径。
func OutputPath(dir string) string { return filepath.Join(dir, "out.json") }

// TempPath 是输出临时文件路径。
func TempPath(dir string) string { return filepath.Join(dir, "out.json.tmp") }

// IsTemp 判断 name 是否为本管线使用的未完成临时文件名。
func IsTemp(name string) bool { return strings.HasSuffix(name, ".tmp") }

// CleanTemp 删除目录内全部 *.tmp（任何截断长度都不会被当成有效输出）。
func CleanTemp(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
	if os.IsNotExist(err) {
		return nil
	}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && IsTemp(e.Name()) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Encode 生成带 SHA-256 尾行的确定字节序列。
func Encode(st *State) ([]byte, error) {
	body, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	return append(append(body, '\n'), []byte("sha256="+hex.EncodeToString(sum[:])+"\n")...), nil
}

// Decode 校验尾行 SHA-256 并解析状态；任何不符都返回 ErrCorrupt。
func Decode(data []byte) (*State, error) {
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) < 2 {
		return nil, ErrCorrupt
	}
	body, tag := bytes.Join(lines[:len(lines)-2], []byte("\n")), lines[len(lines)-2]
	if !bytes.HasPrefix(tag, []byte("sha256=")) {
		return nil, ErrCorrupt
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != string(tag[len("sha256="):]) {
		return nil, ErrCorrupt
	}
	st := &State{}
	if err := json.Unmarshal(body, st); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return st, nil
}

// Commit 先写临时文件再原子改名。CrashBeforeRename 生效时留下临时文件。
func (s *Sink) Commit(st *State) error {
	data, err := Encode(st)
	if err != nil {
		return err
	}
	tmp := TempPath(s.dir)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if s.CrashBeforeRename > 0 {
		s.CrashBeforeRename--
		return ErrCrashSimulated
	}
	return os.Rename(tmp, OutputPath(s.dir))
}

// Load 读取并校验最终输出；文件不存在返回 (nil, nil)。
func (s *Sink) Load() (*State, error) {
	data, err := os.ReadFile(OutputPath(s.dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// ErrCrashSimulated 表示故障注入点被触发（进程语义上的立即中止）。
var ErrCrashSimulated = errors.New("sink: simulated crash before rename")
