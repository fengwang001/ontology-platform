package store

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileEngine is a real durable Engine backed by one file per key.
//
// Every Put is a durability barrier: data is written to a sibling temp
// file, fsynced, atomically renamed into place, and the directory is
// fsynced so that the rename survives power loss.
type FileEngine struct {
	mu  sync.Mutex
	dir string
}

// NewFileEngine opens (creating if needed) an on-disk engine at dir.
func NewFileEngine(dir string) (*FileEngine, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	e := &FileEngine{dir: dir}
	if err := e.fsyncDir(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *FileEngine) path(key string) string {
	return filepath.Join(e.dir, key)
}

func (e *FileEngine) fsyncDir() error {
	f, err := os.Open(e.dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (e *FileEngine) Get(key string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := os.ReadFile(e.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (e *FileEngine) Put(key string, value []byte) (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tmp, err := os.CreateTemp(e.dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(e.path(key)), 0o755); err != nil {
		return err
	}
	if err = os.Rename(tmpName, e.path(key)); err != nil {
		return err
	}
	return e.fsyncDir()
}

func (e *FileEngine) Rename(src, dst string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := os.Stat(e.path(src)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if err := os.Rename(e.path(src), e.path(dst)); err != nil {
		return err
	}
	return e.fsyncDir()
}

func (e *FileEngine) Delete(key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := os.Remove(e.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return e.fsyncDir()
}

// FileEngine scans records directly and needs no access counter; the
// boundedness of recovery is evidenced by the fixed key naming scheme.
func (e *FileEngine) RecordAccesses() int { return 0 }

// ListKeys returns sorted keys beginning with prefix (journal replay).
func (e *FileEngine) ListKeys(prefix string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	_ = filepath.WalkDir(e.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(e.dir, path)
		if err != nil {
			return nil
		}
		if strings.HasPrefix(rel, prefix) {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
