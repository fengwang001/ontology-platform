package pipeline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const checkpointName = "checkpoint.json"

func checkpointPath(dir string) string { return filepath.Join(dir, checkpointName) }

// saveCheckpoint atomically replaces the checkpoint file and fsyncs it,
// so a crash at any point leaves either the previous or the new state.
func saveCheckpoint(dir string, cp checkpoint) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := checkpointPath(dir) + ".tmp"
	f, err := os.Create(tmp)
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
	return os.Rename(tmp, checkpointPath(dir))
}

func loadCheckpoint(dir string) (checkpoint, bool, error) {
	data, err := os.ReadFile(checkpointPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return checkpoint{}, false, nil
	}
	if err != nil {
		return checkpoint{}, false, err
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return checkpoint{}, false, errors.Join(ErrStateCorrupt, err)
	}
	return cp, true, nil
}
