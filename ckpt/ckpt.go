// Package ckpt 管理检查点状态的原子写出与损坏回退。
package ckpt

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"ontology/sink"
)

// State 是检查点内容：已消费偏移、坏记录数、各组中间聚合。
type State struct {
	Off    int64        `json:"off"`
	Bad    int64        `json:"bad"`
	Groups []sink.Group `json:"groups"`
}

type envelope struct {
	CRC   uint32 `json:"crc"`
	State State  `json:"state"`
}

// Store 在同一目录交替写两份检查点。
type Store struct {
	dir     string
	toggle  int
	FellBack bool
}

// NewStore 打开检查点目录。
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) paths() [2]string {
	return [2]string{
		filepath.Join(s.dir, "ckpt.a"),
		filepath.Join(s.dir, "ckpt.b"),
	}
}

// Save 原子写出下一份检查点（先临时文件再改名）。
func (s *Store) Save(st State) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(st)
	if err != nil {
		return err
	}
	env := envelope{CRC: crc32.ChecksumIEEE(payload), State: st}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	paths := s.paths()
	path := paths[s.toggle%2]
	s.toggle++
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load 从最近一份完好检查点恢复；较新的损坏则回退另一份并标记。
// 两份都不存在返回零状态；一份缺失不影响另一份使用。
func (s *Store) Load() (State, error) {
	paths := s.paths()
	var best State
	found, corrupt := false, false
	for _, path := range paths {
		st, err := readOne(path)
		if err == nil {
			if !found || st.Off > best.Off {
				best, found = st, true
			}
			continue
		}
		if !os.IsNotExist(err) {
			corrupt = true
		}
	}
	if found && corrupt {
		s.FellBack = true
	}
	if !found && corrupt {
		s.FellBack = true
		return State{}, fmt.Errorf("ckpt: both checkpoints unusable")
	}
	return best, nil
}

func readOne(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return State{}, err
	}
	payload, err := json.Marshal(env.State)
	if err != nil {
		return State{}, err
	}
	if crc32.ChecksumIEEE(payload) != env.CRC {
		return State{}, fmt.Errorf("ckpt: checksum mismatch in %s", filepath.Base(path))
	}
	return env.State, nil
}
