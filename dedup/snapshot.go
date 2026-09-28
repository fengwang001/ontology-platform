package dedup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const snapshotVersion = 1

// snapshotData 是落盘状态的 JSON 表示。结果表、各分区水位与重复数在同一个文件中，
// 通过 临时文件 + fsync + rename 原子替换，保证三者要么全部生效、要么全部不生效。
type snapshotData struct {
	Version    int                    `json:"version"`
	Totals     map[string]int64       `json:"totals"`
	Partitions map[int]PartitionState `json:"partitions"`
}

func loadSnapshot(path string) (map[string]int64, map[int]PartitionState, error) {
	if path == "" {
		return map[string]int64{}, map[int]PartitionState{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, map[int]PartitionState{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read snapshot %q: %w", path, err)
	}
	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, nil, fmt.Errorf("corrupt snapshot %q: %w", path, err)
	}
	if snap.Version != snapshotVersion {
		return nil, nil, fmt.Errorf("unsupported snapshot version %d in %q", snap.Version, path)
	}
	if snap.Totals == nil {
		snap.Totals = map[string]int64{}
	}
	if snap.Partitions == nil {
		snap.Partitions = map[int]PartitionState{}
	}
	return snap.Totals, snap.Partitions, nil
}

func persistSnapshot(path string, totals map[string]int64, partitions map[int]PartitionState) (err error) {
	if path == "" {
		return nil
	}
	snap := snapshotData{Version: snapshotVersion, Totals: totals, Partitions: partitions}
	data, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp snapshot: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("fsync snapshot: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename snapshot: %w", err)
	}

	// fsync 目录，保证 rename 在崩溃后仍然可见。
	dirFile, derr := os.Open(dir)
	if derr == nil {
		syncErr := dirFile.Sync()
		closeErr := dirFile.Close()
		if err == nil && syncErr != nil {
			return fmt.Errorf("fsync snapshot dir: %w", syncErr)
		}
		if err == nil && closeErr != nil {
			return fmt.Errorf("close snapshot dir: %w", closeErr)
		}
	}
	return nil
}
