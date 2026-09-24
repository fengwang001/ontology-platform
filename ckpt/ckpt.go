// Package ckpt 写出与恢复检查点：最近两个文件轮转，带 SHA-256 校验和。
package ckpt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Group 是一个分组的中间聚合。
type Group struct {
	Sum int64 `json:"sum"`
	Cnt int64 `json:"cnt"`
}

// State 是检查点完整状态。
type State struct {
	Pos      int64            `json:"pos"`
	Bad      int64            `json:"bad"`
	Groups   map[string]Group `json:"groups"`
	fromFile string
}

// Empty 返回零状态。
func Empty() State {
	return State{Groups: map[string]Group{}}
}

// Pos / Bad / Groups 访问器。
func (s State) Position() int64            { return s.Pos }
func (s State) BadCount() int64            { return s.Bad }
func (s State) Snapshot() map[string]Group { return s.Groups }

// Store 管理两个轮转检查点文件。
type Store struct {
	dir      string
	nextFile int
}

// NewStore 打开目录并根据现存文件决定下一次写入槽位。
func NewStore(dir string) *Store {
	st := &Store{dir: dir}
	if _, err1 := os.Stat(filepath.Join(dir, "ckpt.1")); err1 == nil {
		st.nextFile = 0
	}
	return st
}

// Save 原子写出下一个槽位（临时文件 + rename）。
func (s *Store) Save(st State) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	name := fmt.Sprintf("ckpt.%d", s.nextFile)
	final := filepath.Join(s.dir, name)
	tmp := final + ".tmp"
	content := append(append([]byte{}, payload...),
		[]byte("\nsha256="+hex.EncodeToString(sum[:])+"\n")...)
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	s.nextFile ^= 1
	return nil
}

// Load 依次尝试两个槽位：校验失败则回退到另一个，两个都坏返回零状态。
// 第二个返回值表示是否发生过回退。
func (s *Store) Load() (State, bool, error) {
	var fellBack bool
	order := []int{s.nextFile ^ 1, s.nextFile}
	for _, slot := range order {
		name := fmt.Sprintf("ckpt.%d", slot)
		st, err := readOne(filepath.Join(s.dir, name))
		if err == nil {
			st.fromFile = name
			return st, fellBack, nil
		}
		if !os.IsNotExist(err) {
			fellBack = true
		}
	}
	entries, _ := os.ReadDir(s.dir)
	hasAny := false
	for _, e := range entries {
		if len(e.Name()) >= 5 && e.Name()[:5] == "ckpt." {
			hasAny = true
		}
	}
	if !hasAny {
		return Empty(), false, nil
	}
	return Empty(), true, nil
}

func readOne(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	trimmed := data
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\n' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	idx := -1
	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] == '\n' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return State{}, fmt.Errorf("ckpt: %s no trailer", filepath.Base(path))
	}
	last := string(trimmed[idx+1:])
	bodyTrim := trimmed[:idx]
	want := []byte("sha256=")
	if len(last) < len(want)+64 || string(last[:len(want)]) != string(want) {
		return State{}, fmt.Errorf("ckpt: %s bad trailer", filepath.Base(path))
	}
	got := sha256.Sum256(bodyTrim)
	if hex.EncodeToString(got[:]) != last[len(want):] {
		return State{}, fmt.Errorf("ckpt: %s checksum mismatch", filepath.Base(path))
	}
	var st State
	if err := json.Unmarshal(bodyTrim, &st); err != nil {
		return State{}, err
	}
	if st.Groups == nil {
		st.Groups = map[string]Group{}
	}
	return st, nil
}

// Corrupt 翻转指定槽位文件的一个载荷字节，供故障注入测试使用。
func (s *Store) Corrupt(slot int) error {
	path := filepath.Join(s.dir, fmt.Sprintf("ckpt.%d", slot))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("ckpt: empty file")
	}
	if data[0] == '{' {
		data[0] = 'X'
	} else {
		data[0] ^= 0xFF
	}
	return os.WriteFile(path, data, 0o644)
}
