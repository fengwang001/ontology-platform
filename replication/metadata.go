package replication

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const metadataName = "slot.json"

// slotMetadata 是两个位点的持久化形式。确认位点先于日志回收落盘，
// 因此崩溃后不会出现“已回收但确认丢失”的情况。
type slotMetadata struct {
	ConfirmedLSN uint64 `json:"confirmed_lsn"`
	RestartLSN   uint64 `json:"restart_lsn"`
}

func writeMetadataAtomic(dir string, meta slotMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, metadataName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, metadataName)); err != nil {
		return err
	}
	return syncDir(dir)
}

func readMetadata(dir string) (slotMetadata, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, metadataName))
	if os.IsNotExist(err) {
		return slotMetadata{}, false, nil
	}
	if err != nil {
		return slotMetadata{}, false, err
	}
	var meta slotMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return slotMetadata{}, false, ErrCorrupted
	}
	if meta.RestartLSN > meta.ConfirmedLSN {
		return slotMetadata{}, false, ErrCorrupted
	}
	return meta, true, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
