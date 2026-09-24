// Package ckpt writes and restores pipeline checkpoints.
//
// The newest checkpoint is ckpt.000, the previous one is ckpt.001.
// Each file is a single JSON line followed by a "sha256=<hex>" line.
// A corrupted newest file causes fallback to the older one.
package ckpt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Agg is the intermediate aggregation state of one group.
type Agg struct {
	Count int64 `json:"count"`
	Sum   int64 `json:"sum"`
}

// State is a pipeline checkpoint.
type State struct {
	Pos    int64          `json:"pos"`
	Bad    int64          `json:"bad"`
	Groups map[string]Agg `json:"groups"`
}

// ErrCorrupt means no checkpoint file passed its checksum.
var ErrCorrupt = errors.New("ckpt: all checkpoints corrupt")

const (
	newFile = "ckpt.000"
	oldFile = "ckpt.001"
)

func path(dir, name string) string { return filepath.Join(dir, name) }

func encode(s State) ([]byte, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	return append(append(body, '\n'), []byte("sha256="+hex.EncodeToString(sum[:])+"\n")...), nil
}

func decodeFile(name string) (State, error) {
	var s State
	raw, err := os.ReadFile(name)
	if err != nil {
		return s, err
	}
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return s, fmt.Errorf("ckpt: malformed file %s", name)
	}
	body, trailer := raw[:nl], raw[nl+1:]
	want := "sha256=" + hex.EncodeToString(func() []byte {
		h := sha256.Sum256(body)
		return h[:]
	}()) + "\n"
	if string(trailer) != want {
		return s, fmt.Errorf("ckpt: checksum mismatch in %s", name)
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return s, fmt.Errorf("ckpt: bad json in %s: %w", name, err)
	}
	if s.Groups == nil {
		s.Groups = map[string]Agg{}
	}
	return s, nil
}

// Save rotates ckpt.000 to ckpt.001 and writes the new state as ckpt.000.
// The new file is fsynced and atomically renamed.
func Save(dir string, s State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := encode(s)
	if err != nil {
		return err
	}
	if err := os.Rename(path(dir, newFile), path(dir, oldFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, "ckpt.tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path(dir, newFile)); err != nil {
		return err
	}
	cleanup = false
	return syncDir(dir)
}

// Load reads the newest valid checkpoint. fallback is true when ckpt.000
// failed verification and ckpt.001 was used instead.
func Load(dir string) (s State, fallback bool, err error) {
	s0, err0 := decodeFile(path(dir, newFile))
	if err0 == nil {
		return s0, false, nil
	}
	if !errors.Is(err0, os.ErrNotExist) {
		s1, err1 := decodeFile(path(dir, oldFile))
		if err1 == nil {
			return s1, true, nil
		}
	} else {
		s1, err1 := decodeFile(path(dir, oldFile))
		if err1 == nil {
			return s1, false, nil
		}
		if errors.Is(err1, os.ErrNotExist) {
			return State{Groups: map[string]Agg{}}, false, nil
		}
	}
	return State{}, false, ErrCorrupt
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	_ = d.Close()
	return err
}
