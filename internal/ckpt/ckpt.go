// Package ckpt 负责检查点的原子写出与恢复：两份轮转，各附 CRC32。
package ckpt

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"ontology/internal/agg"
)

// ErrCorrupt 表示检查点校验和不匹配或无法解析。
var ErrCorrupt = errors.New("ckpt: corrupt checkpoint")

// State 是检查点内容。
type State struct {
	Seq    int64       `json:"seq"`
	Bad    int64       `json:"bad"`
	Groups []agg.Group `json:"groups"`
}

// Store 管理目录下两份轮转检查点。
type Store struct {
	dir    string
	toggle int
	// FellBack 在最近一次 Load 因一份损坏而回退到另一份时为 true。
	FellBack bool
}

const suffix = ".ckpt"

// New 在 dir 下创建/打开检查点仓库。
func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) paths() [2]string {
	return [2]string{
		filepath.Join(s.dir, "a"+suffix),
		filepath.Join(s.dir, "b"+suffix),
	}
}

// Save 原子写出下一份检查点（tmp+rename），始终保留最近两份。
func (s *Store) Save(st State) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := encode(st)
	if err != nil {
		return err
	}
	p := s.paths()[s.toggle]
	s.toggle ^= 1
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load 取校验完好且 Seq 最大的检查点；不存在则返回零值。
// 恰有一份损坏时回退另一份并置 FellBack。
func (s *Store) Load() (State, error) {
	s.FellBack = false
	var best State
	found, bad := false, 0
	for _, p := range s.paths() {
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return State{}, err
		}
		st, err := decode(data)
		if err != nil {
			bad++
			continue
		}
		if !found || st.Seq > best.Seq {
			best, found = st, true
		}
	}
	if bad > 0 && found {
		s.FellBack = true
	}
	if bad > 0 && !found {
		return State{}, ErrCorrupt
	}
	if best.Groups == nil {
		best.Groups = []agg.Group{}
	}
	return best, nil
}

func encode(st State) ([]byte, error) {
	body, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body)+4)
	copy(out, body)
	binary.LittleEndian.PutUint32(out[len(body):], crc32.ChecksumIEEE(body))
	return out, nil
}

func decode(data []byte) (State, error) {
	if len(data) < 4 {
		return State{}, ErrCorrupt
	}
	body, sum := data[:len(data)-4], binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return State{}, ErrCorrupt
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return st, nil
}
