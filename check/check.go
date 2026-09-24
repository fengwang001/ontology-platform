// Package check atomically reads and writes the 8-byte int64 checkpoint.
// The checkpoint stores "next", the next number to allocate. It depends on
// no other package.
package check

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Sentinel errors. Every failure path in the project maps to one of these,
// and the three classes are intentionally distinct.
var (
	ErrInvalidDir    = errors.New("checkpoint: directory is empty or invalid")
	ErrPersistFailed = errors.New("checkpoint: failed to persist next offset")
	ErrCorrupt       = errors.New("checkpoint: file is corrupt")
)

// FileName is the single scalar checkpoint file inside the data directory.
const FileName = "checkpoint"

const tmpName = ".checkpoint.tmp"

// Store owns the checkpoint file in one directory.
type Store struct {
	dir string
	// recordsRead is the number of checkpoint records parsed by the last
	// Restore: 1 once a checkpoint exists (one int64 scalar), 0 before any
	// successful Next. Unexported by design; it must never leave the package
	// through an exported method.
	recordsRead int
	// persistFault is the test-only fault-injection switch for Persist.
	persistFault bool
}

// NewStore validates and creates a Store rooted at dir. An empty directory
// is rejected and changes no state.
func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, ErrInvalidDir
	}
	return &Store{dir: dir}, nil
}

// SetPersistFault toggles forced Persist failure. When on, Persist fails
// without touching the checkpoint file (failure leaves no trace).
func (s *Store) SetPersistFault(on bool) { s.persistFault = on }

// Path is the checkpoint file path (unexported helper for callers' tests).
func (s *Store) path() string { return filepath.Join(s.dir, FileName) }

// Persist atomically writes next: temp file -> fsync(file) -> rename ->
// fsync(dir). Either the previous value or the new value survives, never a
// torn write.
func (s *Store) Persist(next int64) error {
	if s.persistFault {
		return ErrPersistFailed
	}
	tmp := filepath.Join(s.dir, tmpName)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.Join(ErrPersistFailed, err)
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(next))
	if _, err := f.Write(buf[:]); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return errors.Join(ErrPersistFailed, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return errors.Join(ErrPersistFailed, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return errors.Join(ErrPersistFailed, err)
	}
	// rename is the commit point: afterwards the new scalar is in place and
	// the old file can never be half-overwritten. Every failure above happens
	// before rename and leaves the previous checkpoint byte-for-byte intact.
	if err := os.Rename(tmp, s.path()); err != nil {
		_ = os.Remove(tmp)
		return errors.Join(ErrPersistFailed, err)
	}
	// Best-effort durability of the directory entry itself.
	if df, err := os.Open(s.dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}

// Restore reads and validates the checkpoint. A missing file means no number
// was ever issued, so next == 0. Wrong length or a negative value is
// corruption: ErrCorrupt and no fabricated offset.
func (s *Store) Restore() (next int64, err error) {
	b, err := os.ReadFile(s.path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.recordsRead = 0
			return 0, nil
		}
		return 0, errors.Join(ErrCorrupt, err)
	}
	if len(b) != 8 {
		s.recordsRead = 0
		return 0, ErrCorrupt
	}
	v := int64(binary.LittleEndian.Uint64(b))
	s.recordsRead = 1
	if v < 0 {
		return 0, ErrCorrupt
	}
	return v, nil
}
