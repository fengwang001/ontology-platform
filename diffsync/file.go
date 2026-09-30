package diffsync

import (
	"fmt"
	"os"
	"path/filepath"
)

// ApplyFileAtomic applies the patch at patchPath to the old file at
// oldPath and atomically installs the result at dstPath.
//
// The new content is fully rebuilt and verified in memory first, then
// written to a temporary file in the destination directory, fsynced and
// renamed over dstPath. If anything fails, dstPath is left byte-for-byte
// untouched (a pre-existing destination is never partially overwritten).
func ApplyFileAtomic(oldPath, patchPath, dstPath string) error {
	old, err := os.ReadFile(oldPath)
	if err != nil {
		return fmt.Errorf("diffsync: read old file: %w", err)
	}
	patch, err := os.ReadFile(patchPath)
	if err != nil {
		return fmt.Errorf("diffsync: read patch: %w", err)
	}
	result, err := Apply(old, patch)
	if err != nil {
		return err
	}

	dir := filepath.Dir(dstPath)
	tmp, err := os.CreateTemp(dir, ".diffsync-*")
	if err != nil {
		return fmt.Errorf("diffsync: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(result); err != nil {
		tmp.Close()
		return fmt.Errorf("diffsync: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("diffsync: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("diffsync: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dstPath); err != nil {
		return fmt.Errorf("diffsync: rename into place: %w", err)
	}
	return nil
}
