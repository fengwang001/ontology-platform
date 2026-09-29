package committer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// state 是持久化到磁盘的完整状态：已提交位点 + 已写副作用。
type state struct {
	Position int64            `json:"position"`
	Effects  map[int64]string `json:"effects"`
}

// fileStore 以 JSON 文件持久化状态，写入采用临时文件 + rename 保证原子性。
type fileStore struct {
	path string
}

func newFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

func (s *fileStore) load() (state, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return state{Effects: map[int64]string{}}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("读取状态文件失败: %w", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, fmt.Errorf("解析状态文件失败: %w", err)
	}
	if st.Effects == nil {
		st.Effects = map[int64]string{}
	}
	return st, nil
}

func (s *fileStore) save(st state) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".committer-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("落盘临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("原子替换状态文件失败: %w", err)
	}
	return nil
}
