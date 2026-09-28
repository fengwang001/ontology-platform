package idempotent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// persistedState 是落盘的完整状态：结果表、各分区水位、累计重复数。
type persistedState struct {
	Results    map[string]int64 `json:"results"`
	Watermarks map[int]int64    `json:"watermarks"`
	Duplicates int64            `json:"duplicates"`
}

// loadState 从文件重建状态；文件不存在时返回空状态（首次启动）。
func loadState(path string) (persistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return persistedState{
				Results:    map[string]int64{},
				Watermarks: map[int]int64{},
			}, nil
		}
		return persistedState{}, fmt.Errorf("idempotent: read state: %w", err)
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return persistedState{}, fmt.Errorf("idempotent: decode state: %w", err)
	}
	if st.Results == nil {
		st.Results = map[string]int64{}
	}
	if st.Watermarks == nil {
		st.Watermarks = map[int]int64{}
	}
	return st, nil
}

// saveState 以“临时文件 + fsync + 原子 rename + 目录 fsync”整体提交，
// 崩溃后要么看到旧状态、要么看到完整新状态，不会出现半个文件。
func saveState(path string, st persistedState) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return fmt.Errorf("idempotent: create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err = enc.Encode(st); err != nil {
		return fmt.Errorf("idempotent: encode state: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("idempotent: fsync state: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("idempotent: close state: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("idempotent: rename state: %w", err)
	}
	if dirFile, dirErr := os.Open(dir); dirErr == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
